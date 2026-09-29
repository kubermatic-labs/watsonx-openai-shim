/*
Copyright 2026 The Kubermatic Kubernetes Platform contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package shim

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kubermatic-labs/watsonx-openai-shim/internal/watsonx"
)

const (
	testModel   = "ibm/granite-3-8b-instruct"
	testRequest = `{"model":"` + testModel + `","messages":[{"role":"user","content":"Hi"}]}`

	testStreamRequest            = `{"model":"m","messages":[{"role":"user","content":"Hi"}],"stream":true}`
	testMultiChoiceStreamRequest = `{"model":"m","messages":[{"role":"user","content":"Hi"}],"stream":true,"n":2}`
	doneEvent                    = "[DONE]"
)

// fakeClient returns canned watsonx responses and records the last request.
type fakeClient struct {
	resp   *watsonx.ChatResponse
	stream string
	models []watsonx.FoundationModel
	err    error

	req *watsonx.ChatRequest
}

func (f *fakeClient) Chat(_ context.Context, req *watsonx.ChatRequest) (*watsonx.ChatResponse, error) {
	f.req = req
	return f.resp, f.err
}

func (f *fakeClient) ChatStream(_ context.Context, req *watsonx.ChatRequest) (*watsonx.Stream, error) {
	f.req = req
	if f.err != nil {
		return nil, f.err
	}

	return watsonx.NewStream(io.NopCloser(strings.NewReader(f.stream))), nil
}

func (f *fakeClient) ListModels(_ context.Context) ([]watsonx.FoundationModel, error) {
	return f.models, f.err
}

func newTestServer(t *testing.T, client Client) *httptest.Server {
	t.Helper()

	h, err := NewHandler(client, Options{DefaultMaxTokens: 100})
	require.NoError(t, err)
	h.now = func() time.Time { return time.Unix(1790000000, 0) }

	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)

	return srv
}

// testResponse is a fully read HTTP response.
type testResponse struct {
	status int
	header http.Header
	body   []byte
}

func doRequest(t *testing.T, method, url, body string) testResponse {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return testResponse{status: resp.StatusCode, header: resp.Header, body: respBody}
}

func postCompletion(t *testing.T, srv *httptest.Server, body string) testResponse {
	t.Helper()

	return doRequest(t, http.MethodPost, srv.URL+"/v1/chat/completions", body)
}

func TestChatCompletion(t *testing.T) {
	tests := []struct {
		name     string
		resp     *watsonx.ChatResponse
		expected string
	}{
		{
			name: "text",
			resp: &watsonx.ChatResponse{
				ID: "chat-1", ModelID: testModel, Created: 1790000000,
				Choices: []watsonx.ChatChoice{{
					Message:      watsonx.Message{Role: "assistant", Content: "Hello", ReasoningContent: "Greet back."},
					FinishReason: watsonx.FinishReasonStop,
				}},
				Usage: &watsonx.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
			},
			expected: `{"id":"chat-1","object":"chat.completion","created":1790000000,"model":"` + testModel + `",
				"choices":[{"index":0,"message":{"role":"assistant","content":"Hello","reasoning_content":"Greet back."},
				"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`,
		},
		{
			name: "tool calls and time limit",
			resp: &watsonx.ChatResponse{
				ID: "chat-2", ModelID: testModel, Created: 1790000000,
				Choices: []watsonx.ChatChoice{{
					Message: watsonx.Message{Role: "assistant", ToolCalls: []watsonx.ToolCall{
						{ID: "call-1", Type: "function", Function: watsonx.FunctionCall{Name: "weather", Arguments: `{"city":"Berlin"}`}},
					}},
					FinishReason: watsonx.FinishReasonTimeLimit,
				}},
			},
			expected: `{"id":"chat-2","object":"chat.completion","created":1790000000,"model":"` + testModel + `",
				"choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[
				{"id":"call-1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Berlin\"}"}}]},
				"finish_reason":"length"}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeClient{resp: tt.resp}
			resp := postCompletion(t, newTestServer(t, client), testRequest)

			require.Equal(t, http.StatusOK, resp.status)
			assert.JSONEq(t, tt.expected, string(resp.body))
			require.NotNil(t, client.req)
			assert.Equal(t, testModel, client.req.ModelID)
		})
	}
}

func TestChatCompletionErrors(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		client         *fakeClient
		expectedStatus int
		expectedType   string
		expectedMsg    string
	}{
		{
			name:           "invalid request",
			body:           `{"messages":[{"role":"user","content":"Hi"}]}`,
			expectedStatus: http.StatusBadRequest,
			expectedType:   errorTypeInvalidRequest,
			expectedMsg:    "model is required",
		},
		{
			name:           "watsonx rejects the request",
			client:         &fakeClient{err: &watsonx.APIError{StatusCode: http.StatusNotFound, Message: "Model 'x' is not supported"}},
			expectedStatus: http.StatusNotFound,
			expectedType:   errorTypeInvalidRequest,
			expectedMsg:    "Model 'x' is not supported",
		},
		{
			name:           "watsonx rejects the shim credentials",
			client:         &fakeClient{err: &watsonx.APIError{StatusCode: http.StatusUnauthorized, Message: "invalid token"}},
			expectedStatus: http.StatusBadGateway,
			expectedType:   errorTypeUpstream,
			expectedMsg:    "invalid token",
		},
		{
			name:           "watsonx is unreachable",
			client:         &fakeClient{err: errors.New("call watsonx: connection refused")},
			expectedStatus: http.StatusBadGateway,
			expectedType:   errorTypeUpstream,
			expectedMsg:    "connection refused",
		},
		{
			name: "generation error",
			client: &fakeClient{resp: &watsonx.ChatResponse{
				Choices: []watsonx.ChatChoice{{FinishReason: watsonx.FinishReasonError}},
			}},
			expectedStatus: http.StatusBadGateway,
			expectedType:   errorTypeUpstream,
			expectedMsg:    `finish reason "error"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := tt.client
			if client == nil {
				client = &fakeClient{}
			}

			body := tt.body
			if body == "" {
				body = testRequest
			}

			resp := postCompletion(t, newTestServer(t, client), body)
			require.Equal(t, tt.expectedStatus, resp.status)

			var got ErrorResponse
			require.NoError(t, json.Unmarshal(resp.body, &got))
			assert.Equal(t, tt.expectedType, got.Error.Type)
			assert.Contains(t, got.Error.Message, tt.expectedMsg)
		})
	}
}

// sseEvent formats watsonx stream events.
func sseEvent(data ...string) string {
	var sb strings.Builder
	for i, d := range data {
		sb.WriteString("id: " + string(rune('1'+i)) + "\nevent: message\ndata: " + d + "\n\n")
	}

	return sb.String()
}

// readEvents returns the data of all server-sent events.
func readEvents(t *testing.T, body []byte) []string {
	t.Helper()

	var events []string
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
			events = append(events, data)
		}
	}
	require.NoError(t, scanner.Err())

	return events
}

func TestChatCompletionStream(t *testing.T) {
	const prefix = `{"id":"chat-1","object":"chat.completion.chunk","created":1,"model":"m",`
	const wxPrefix = `{"id":"chat-1","model_id":"m","created":1,`

	tests := []struct {
		name           string
		body           string
		stream         string
		expectedEvents []string
	}{
		{
			name: "content with usage",
			body: `{"model":"m","messages":[{"role":"user","content":"Hi"}],"stream":true,"stream_options":{"include_usage":true}}`,
			stream: sseEvent(
				wxPrefix+`"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`,
				wxPrefix+`"choices":[{"index":0,"delta":{"content":"lo"}}]}`,
				wxPrefix+`"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],`+
					`"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`,
			),
			expectedEvents: []string{
				prefix + `"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}`,
				prefix + `"choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":null}]}`,
				prefix + `"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				prefix + `"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`,
				doneEvent,
			},
		},
		{
			name: "tool call fragments get an index",
			body: testStreamRequest,
			stream: sseEvent(
				wxPrefix+`"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[`+
					`{"id":"call-1","type":"function","function":{"name":"weather","arguments":""}}]}}]}`,
				wxPrefix+`"choices":[{"index":0,"delta":{"tool_calls":[`+
					`{"id":"","type":"function","function":{"name":"","arguments":"{\"city\":"}}]}}]}`,
				wxPrefix+`"choices":[{"index":0,"delta":{"tool_calls":[`+
					`{"id":"","type":"function","function":{"name":"","arguments":"\"Berlin\"}"}}]}}]}`,
				wxPrefix+`"choices":[{"index":0,"delta":{"tool_calls":[`+
					`{"id":"call-2","type":"function","function":{"name":"time","arguments":"{}"}}]}}]}`,
				wxPrefix+`"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],`+
					`"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`,
			),
			expectedEvents: []string{
				prefix + `"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[` +
					`{"index":0,"id":"call-1","type":"function","function":{"name":"weather"}}]},"finish_reason":null}]}`,
				prefix + `"choices":[{"index":0,"delta":{"tool_calls":[` +
					`{"index":0,"function":{"arguments":"{\"city\":"}}]},"finish_reason":null}]}`,
				prefix + `"choices":[{"index":0,"delta":{"tool_calls":[` +
					`{"index":0,"function":{"arguments":"\"Berlin\"}"}}]},"finish_reason":null}]}`,
				prefix + `"choices":[{"index":0,"delta":{"tool_calls":[` +
					`{"index":1,"id":"call-2","type":"function","function":{"name":"time","arguments":"{}"}}]},"finish_reason":null}]}`,
				prefix + `"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
				doneEvent,
			},
		},
		{
			name: "stream ends without finish reason",
			body: testStreamRequest,
			stream: sseEvent(
				wxPrefix + `"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`,
			),
			expectedEvents: []string{
				prefix + `"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}`,
				`{"error":{"message":"` + errStreamIncomplete.Error() + `","type":"upstream_error"}}`,
				doneEvent,
			},
		},
		{
			name: "all of two choices finish",
			body: testMultiChoiceStreamRequest,
			stream: sseEvent(
				wxPrefix+`"choices":[{"index":0,"delta":{"role":"assistant","content":"A"}},`+
					`{"index":1,"delta":{"role":"assistant","content":"B"}}]}`,
				wxPrefix+`"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				wxPrefix+`"choices":[{"index":1,"delta":{},"finish_reason":"length"}]}`,
			),
			expectedEvents: []string{
				prefix + `"choices":[{"index":0,"delta":{"role":"assistant","content":"A"},"finish_reason":null},` +
					`{"index":1,"delta":{"role":"assistant","content":"B"},"finish_reason":null}]}`,
				prefix + `"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				prefix + `"choices":[{"index":1,"delta":{},"finish_reason":"length"}]}`,
				doneEvent,
			},
		},
		{
			name: "one of two choices does not finish",
			body: testMultiChoiceStreamRequest,
			stream: sseEvent(
				wxPrefix+`"choices":[{"index":0,"delta":{"role":"assistant","content":"A"}},`+
					`{"index":1,"delta":{"role":"assistant","content":"B"}}]}`,
				wxPrefix+`"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			),
			expectedEvents: []string{
				prefix + `"choices":[{"index":0,"delta":{"role":"assistant","content":"A"},"finish_reason":null},` +
					`{"index":1,"delta":{"role":"assistant","content":"B"},"finish_reason":null}]}`,
				prefix + `"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				`{"error":{"message":"` + errStreamIncomplete.Error() + `","type":"upstream_error"}}`,
				doneEvent,
			},
		},
		{
			name: "stream without choices",
			body: testStreamRequest,
			stream: sseEvent(
				wxPrefix + `"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":0,"total_tokens":7}}`,
			),
			expectedEvents: []string{
				`{"error":{"message":"` + errStreamIncomplete.Error() + `","type":"upstream_error"}}`,
				doneEvent,
			},
		},
		{
			name: "error event",
			body: testStreamRequest,
			stream: sseEvent(
				wxPrefix+`"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`,
				`{"errors":[{"code":"oom","message":"out of memory"}],"status_code":500}`,
			),
			expectedEvents: []string{
				prefix + `"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}`,
				`{"error":{"message":"watsonx returned status 500 (oom): out of memory","type":"upstream_error"}}`,
				doneEvent,
			},
		},
		{
			name: "cancelled generation",
			body: testStreamRequest,
			stream: sseEvent(
				wxPrefix + `"choices":[{"index":0,"delta":{},"finish_reason":"cancelled"}]}`,
			),
			expectedEvents: []string{
				`{"error":{"message":"watsonx ended the generation with finish reason \"cancelled\"","type":"upstream_error"}}`,
				doneEvent,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := postCompletion(t, newTestServer(t, &fakeClient{stream: tt.stream}), tt.body)
			require.Equal(t, http.StatusOK, resp.status)
			assert.Equal(t, "text/event-stream", resp.header.Get("Content-Type"))

			assert.Equal(t, tt.expectedEvents, readEvents(t, resp.body))
		})
	}
}

func TestChatCompletionStreamRejected(t *testing.T) {
	client := &fakeClient{err: &watsonx.APIError{StatusCode: http.StatusBadRequest, Message: "tools are not supported"}}
	resp := postCompletion(t, newTestServer(t, client), testStreamRequest)

	require.Equal(t, http.StatusBadRequest, resp.status)
	assert.JSONEq(t, `{"error":{"message":"tools are not supported","type":"invalid_request_error"}}`, string(resp.body))
}

func TestNewHandlerRejectsInvalidMaxTokens(t *testing.T) {
	_, err := NewHandler(&fakeClient{}, Options{})

	require.EqualError(t, err, "default max tokens must be positive")
}
