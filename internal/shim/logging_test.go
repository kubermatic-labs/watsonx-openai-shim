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
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kubermatic-labs/watsonx-openai-shim/internal/watsonx"
)

// Log levels as the JSON handler writes them.
var (
	levelDebug = slog.LevelDebug.String()
	levelInfo  = slog.LevelInfo.String()
	levelError = slog.LevelError.String()
)

// logRecord is the part of a JSON log record the tests check.
type logRecord struct {
	Level     string `json:"level"`
	Msg       string `json:"msg"`
	RequestID string `json:"requestID"`
	Data      string `json:"data"`
}

// syncBuffer is a buffer the server goroutines can write logs to concurrently.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) records(t *testing.T) []logRecord {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()

	var records []logRecord
	scanner := bufio.NewScanner(bytes.NewReader(b.buf.Bytes()))
	for scanner.Scan() {
		var record logRecord
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &record))
		records = append(records, record)
	}
	require.NoError(t, scanner.Err())

	return records
}

// captureLogs sends the default logger's debug output to a buffer for the duration of the test.
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()

	buf := &syncBuffer{}
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(original) })

	return buf
}

func TestRequestLogging(t *testing.T) {
	const (
		wxChunk = `{"id":"chat-1","model_id":"m","created":1,"choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},` +
			`"finish_reason":"stop"}]}`
		wxResponse = `{"id":"chat-1","choices":[{"index":0,"message":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}]}`
	)

	receivedRequest := logRecord{Level: levelDebug, Msg: "Received request"}

	tests := []struct {
		name            string
		method          string
		path            string
		body            string
		client          *fakeClient
		expectedRecords []logRecord
	}{
		{
			name:   "streamed completion logs every chunk",
			method: http.MethodPost,
			path:   "/v1/chat/completions",
			body:   testStreamRequest,
			client: &fakeClient{stream: sseEvent(wxChunk)},
			expectedRecords: []logRecord{
				receivedRequest,
				{Level: levelDebug, Msg: "Received watsonx chunk", Data: wxChunk},
				{Level: levelInfo, Msg: "Chat completion"},
			},
		},
		{
			name:   "completion logs the response",
			method: http.MethodPost,
			path:   "/v1/chat/completions",
			body:   testRequest,
			client: &fakeClient{resp: &watsonx.ChatResponse{
				ID:      "chat-1",
				Choices: []watsonx.ChatChoice{{Message: watsonx.Message{Role: roleAssistant, Content: "Hi"}, FinishReason: "stop"}},
				Raw:     wxResponse,
			}},
			expectedRecords: []logRecord{
				receivedRequest,
				{Level: levelDebug, Msg: "Received watsonx response", Data: wxResponse},
				{Level: levelInfo, Msg: "Chat completion"},
			},
		},
		{
			name:   "failed request",
			method: http.MethodGet,
			path:   "/v1/models",
			client: &fakeClient{err: errors.New("call watsonx: connection refused")},
			expectedRecords: []logRecord{
				receivedRequest,
				{Level: levelError, Msg: "List models failed"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			srv := newTestServer(t, tt.client)

			req, err := http.NewRequestWithContext(t.Context(), tt.method, srv.URL+tt.path, bytes.NewBufferString(tt.body))
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())

			requestID := resp.Header.Get(requestIDHeader)
			require.Regexp(t, "^[0-9a-f]{16}$", requestID)

			expected := make([]logRecord, 0, len(tt.expectedRecords))
			for _, record := range tt.expectedRecords {
				record.RequestID = requestID
				expected = append(expected, record)
			}
			assert.Equal(t, expected, logs.records(t))
		})
	}
}

func TestRequestIDsDiffer(t *testing.T) {
	srv := newTestServer(t, &fakeClient{})

	first := doRequest(t, http.MethodGet, srv.URL+"/healthz", "")
	second := doRequest(t, http.MethodGet, srv.URL+"/healthz", "")

	assert.NotEmpty(t, first.header.Get(requestIDHeader))
	assert.NotEqual(t, first.header.Get(requestIDHeader), second.header.Get(requestIDHeader))
}
