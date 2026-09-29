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

package watsonx

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testProjectID = "project-1"
	testUsername  = "admin"
	testAPIKey    = "secret-key"
	testModel     = "ibm/granite-3-8b-instruct"
	testMessage   = `{"role":"user","content":"Hello"}`
	testChatID    = "chat-1"
)

// fakeWatsonx serves the CPD authorize, IBM Cloud IAM, and chat endpoints.
type fakeWatsonx struct {
	t *testing.T

	mu             sync.Mutex
	tokensIssued   int
	rejectTokens   int
	requests       []recordedRequest
	chatStatus     int
	chatBody       string
	chatStreamBody string
	// modelPages and customModelPages map the start query parameter to a response.
	modelPages       map[string]string
	customModelPages map[string]string
}

type recordedRequest struct {
	path          string
	query         string
	accept        string
	authorization string
	body          map[string]any
}

func (f *fakeWatsonx) issueToken() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokensIssued++

	claims := fmt.Sprintf(`{"exp":%d}`, time.Now().Add(time.Hour).Unix())

	return fmt.Sprintf("header.%s.token-%d", base64.RawURLEncoding.EncodeToString([]byte(claims)), f.tokensIssued)
}

func (f *fakeWatsonx) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case cpdAuthorizePath:
		var req map[string]string
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&req))
		if req["username"] != testUsername || req["api_key"] != testAPIKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"bad credentials"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": f.issueToken()})
	case iamTokenPath:
		require.NoError(f.t, r.ParseForm())
		if r.PostForm.Get("apikey") != testAPIKey {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": f.issueToken(),
			"expiration":   time.Now().Add(time.Hour).Unix(),
		})
	case chatPath, chatStreamPath:
		f.serveChat(w, r)
	case modelSpecsPath:
		f.serveModels(w, r, f.modelPages)
	case customModelsPath:
		f.serveModels(w, r, f.customModelPages)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeWatsonx) serveChat(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var body map[string]any
	require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
	f.requests = append(f.requests, recordedRequest{
		path:          r.URL.Path,
		query:         r.URL.RawQuery,
		accept:        r.Header.Get("Accept"),
		authorization: r.Header.Get("Authorization"),
		body:          body,
	})

	if f.rejectTokens > 0 {
		f.rejectTokens--
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if f.chatStatus != 0 {
		w.WriteHeader(f.chatStatus)
		_, _ = w.Write([]byte(f.chatBody))
		return
	}

	if r.URL.Path == chatStreamPath {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(f.chatStreamBody))
		return
	}

	_, _ = w.Write([]byte(f.chatBody))
}

func (f *fakeWatsonx) serveModels(w http.ResponseWriter, r *http.Request, pages map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.requests = append(f.requests, recordedRequest{
		path:          r.URL.Path,
		query:         r.URL.RawQuery,
		accept:        r.Header.Get("Accept"),
		authorization: r.Header.Get("Authorization"),
	})

	page, ok := pages[r.URL.Query().Get("start")]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	_, _ = w.Write([]byte(page))
}

func newTestClient(t *testing.T, mode AuthMode) (*Client, *fakeWatsonx) {
	t.Helper()

	fake := &fakeWatsonx{t: t}
	srv := httptest.NewTLSServer(fake)
	t.Cleanup(srv.Close)

	client, err := NewClient(Config{
		URL:       srv.URL,
		ProjectID: testProjectID,
		AuthMode:  mode,
		IAMHost:   strings.TrimPrefix(srv.URL, "https://"),
		Username:  testUsername,
		APIKey:    testAPIKey,
		TLSConfig: &tls.Config{RootCAs: srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs},
	})
	require.NoError(t, err)

	return client, fake
}

func testRequest() *ChatRequest {
	maxTokens := 10
	return &ChatRequest{ModelID: testModel, Messages: []json.RawMessage{json.RawMessage(testMessage)}, MaxTokens: &maxTokens}
}

func TestClientChat(t *testing.T) {
	for _, mode := range []AuthMode{AuthModeIAM, AuthModeCPD} {
		t.Run(string(mode), func(t *testing.T) {
			client, fake := newTestClient(t, mode)
			fake.chatBody = `{"id":"` + testChatID + `","model_id":"` + testModel + `","created":1790000000,` +
				`"choices":[{"index":0,"message":{"role":"assistant","content":"Hi",` +
				`"tool_calls":[{"id":"call-1","type":"function","function":{"name":"weather","arguments":"{}"}}]},` +
				`"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`

			resp, err := client.Chat(t.Context(), testRequest())
			require.NoError(t, err)

			assert.Equal(t, &ChatResponse{
				ID:      testChatID,
				ModelID: testModel,
				Created: 1790000000,
				Choices: []ChatChoice{{
					Message: Message{
						Role:    "assistant",
						Content: "Hi",
						ToolCalls: []ToolCall{
							{ID: "call-1", Type: "function", Function: FunctionCall{Name: "weather", Arguments: "{}"}},
						},
					},
					FinishReason: FinishReasonToolCalls,
				}},
				Usage: &Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
				Raw:   fake.chatBody,
			}, resp)

			require.Len(t, fake.requests, 1)
			req := fake.requests[0]
			assert.Equal(t, chatPath, req.path)
			assert.Equal(t, "version="+chatAPIVersion, req.query)
			assert.Equal(t, "application/json", req.accept)
			assert.True(t, strings.HasPrefix(req.authorization, "Bearer header."))
			assert.Equal(t, map[string]any{
				"model_id":   testModel,
				"project_id": testProjectID,
				"messages":   []any{map[string]any{"role": "user", "content": "Hello"}},
				"max_tokens": float64(10),
			}, req.body)
		})
	}
}

func TestClientChatStream(t *testing.T) {
	client, fake := newTestClient(t, AuthModeCPD)
	firstEvent := `{"id":"` + testChatID + `","model_id":"m","created":1,"choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"}}]}`
	secondEvent := `{"id":"` + testChatID + `","model_id":"m","created":1,"choices":[{"index":0,"delta":{"content":"!"},"finish_reason":"stop"}]}`
	fake.chatStreamBody = "id: 1\nevent: message\ndata: " + firstEvent + "\n\nid: 2\nevent: message\ndata: " + secondEvent + "\n\n"

	stream, err := client.ChatStream(t.Context(), testRequest())
	require.NoError(t, err)
	defer stream.Close()

	var chunks []*ChatChunk
	for {
		chunk, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		chunks = append(chunks, chunk)
	}

	assert.Equal(t, []*ChatChunk{
		{ID: testChatID, ModelID: "m", Created: 1, Choices: []ChunkChoice{{Delta: Message{Role: "assistant", Content: "Hi"}}}, Raw: firstEvent},
		{ID: testChatID, ModelID: "m", Created: 1, Choices: []ChunkChoice{{Delta: Message{Content: "!"}, FinishReason: FinishReasonStop}}, Raw: secondEvent},
	}, chunks)
	require.Len(t, fake.requests, 1)
	assert.Equal(t, chatStreamPath, fake.requests[0].path)
	assert.Equal(t, "text/event-stream", fake.requests[0].accept)
}

func TestClientChatErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		expected *APIError
	}{
		{
			name:     "watsonx error body",
			status:   http.StatusNotFound,
			body:     `{"errors":[{"code":"model_not_supported","message":"Model 'x' is not supported"}],"status_code":404}`,
			expected: &APIError{StatusCode: http.StatusNotFound, Code: "model_not_supported", Message: "Model 'x' is not supported"},
		},
		{
			name:     "plain text body",
			status:   http.StatusBadGateway,
			body:     "upstream unavailable\n",
			expected: &APIError{StatusCode: http.StatusBadGateway, Message: "upstream unavailable"},
		},
		{
			name:     "empty body",
			status:   http.StatusServiceUnavailable,
			expected: &APIError{StatusCode: http.StatusServiceUnavailable, Message: "Service Unavailable"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, fake := newTestClient(t, AuthModeCPD)
			fake.chatStatus = tt.status
			fake.chatBody = tt.body

			_, err := client.Chat(t.Context(), testRequest())

			var apiErr *APIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, tt.expected, apiErr)
		})
	}
}

func TestClientTokens(t *testing.T) {
	tests := []struct {
		name             string
		rejectTokens     int
		expectedTokens   int
		expectedRequests int
		expectedErr      string
	}{
		{name: "token is reused", expectedTokens: 1, expectedRequests: 2},
		{name: "rejected token is replaced once", rejectTokens: 1, expectedTokens: 2, expectedRequests: 3},
		{name: "token rejected twice fails", rejectTokens: 2, expectedTokens: 2, expectedRequests: 2, expectedErr: "status 401"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, fake := newTestClient(t, AuthModeCPD)
			fake.chatBody = `{"id":"` + testChatID + `","choices":[]}`
			fake.rejectTokens = tt.rejectTokens

			_, err := client.Chat(t.Context(), testRequest())
			if tt.expectedErr != "" {
				require.ErrorContains(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
				_, err = client.Chat(t.Context(), testRequest())
				require.NoError(t, err)
			}

			assert.Equal(t, tt.expectedTokens, fake.tokensIssued)
			assert.Len(t, fake.requests, tt.expectedRequests)
		})
	}
}

func TestClientRejectsBadCredentials(t *testing.T) {
	client, fake := newTestClient(t, AuthModeCPD)
	client.tokens.fetch = cpdTokenFetcher(client.httpClient, client.baseURL, testUsername, "wrong")

	_, err := client.Chat(t.Context(), testRequest())

	require.ErrorContains(t, err, "obtain watsonx token: CPD authorize: watsonx returned status 401")
	assert.Empty(t, fake.requests)
}

func TestNewClient(t *testing.T) {
	valid := Config{URL: "https://cpd.example.com", ProjectID: testProjectID, APIKey: testAPIKey, AuthMode: AuthModeIAM}

	tests := []struct {
		name        string
		modify      func(*Config)
		expectedErr string
	}{
		{name: "valid", modify: func(*Config) {}},
		{name: "trailing slash and port", modify: func(c *Config) { c.URL = "https://cpd.example.com:8443/" }},
		{name: "http scheme", modify: func(c *Config) { c.URL = "http://cpd.example.com" }, expectedErr: "must be an https:// URL"},
		{name: "path", modify: func(c *Config) { c.URL = "https://cpd.example.com/ml" }, expectedErr: "must not have a path"},
		{name: "missing project", modify: func(c *Config) { c.ProjectID = "" }, expectedErr: "project ID is required"},
		{name: "missing API key", modify: func(c *Config) { c.APIKey = "" }, expectedErr: "API key is required"},
		{name: "CPD without username", modify: func(c *Config) { c.AuthMode = AuthModeCPD }, expectedErr: "requires a username"},
		{name: "unknown auth mode", modify: func(c *Config) { c.AuthMode = "oauth" }, expectedErr: `unknown auth mode "oauth"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.modify(&cfg)

			client, err := NewClient(cfg)
			if tt.expectedErr != "" {
				require.ErrorContains(t, err, tt.expectedErr)
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, client.tokens)
		})
	}
}

func TestTokenCache(t *testing.T) {
	now := time.Unix(1790000000, 0)

	tests := []struct {
		name           string
		lifetime       time.Duration
		elapsed        time.Duration
		expectedTokens int
	}{
		{name: "reused before margin", lifetime: time.Hour, elapsed: 54 * time.Minute, expectedTokens: 1},
		{name: "refreshed within margin", lifetime: time.Hour, elapsed: 56 * time.Minute, expectedTokens: 2},
		{name: "short lifetime refreshed at half", lifetime: 4 * time.Minute, elapsed: 2 * time.Minute, expectedTokens: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := now
			fetched := 0
			cache := &tokenCache{
				now: func() time.Time { return current },
				fetch: func(_ context.Context) (string, time.Time, error) {
					fetched++
					return fmt.Sprintf("token-%d", fetched), current.Add(tt.lifetime), nil
				},
			}

			first, err := cache.get(t.Context())
			require.NoError(t, err)
			assert.Equal(t, "token-1", first)

			current = current.Add(tt.elapsed)
			second, err := cache.get(t.Context())
			require.NoError(t, err)

			assert.Equal(t, fmt.Sprintf("token-%d", tt.expectedTokens), second)
			assert.Equal(t, tt.expectedTokens, fetched)
		})
	}
}

func TestTokenExpiry(t *testing.T) {
	exp := time.Unix(1790000000, 0)
	encode := func(claims string) string {
		return "h." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".s"
	}

	tests := []struct {
		name       string
		token      string
		isFallback bool
	}{
		{name: "exp claim", token: encode(fmt.Sprintf(`{"exp":%d}`, exp.Unix()))},
		{name: "no exp claim", token: encode(`{"sub":"admin"}`), isFallback: true},
		{name: "not a JWT", token: "opaque", isFallback: true},
		{name: "invalid claims", token: "h.!!!.s", isFallback: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenExpiry(tt.token)
			if tt.isFallback {
				assert.WithinDuration(t, time.Now().Add(fallbackTokenLifetime), got, time.Minute)
				return
			}

			assert.Equal(t, exp, got)
		})
	}
}
