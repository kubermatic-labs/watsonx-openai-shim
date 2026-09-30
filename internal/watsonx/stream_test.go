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
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStream(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		expectedIDs   []string
		expectedError error
	}{
		{
			name:        "events without trailing blank line",
			body:        "id: 1\nevent: message\ndata: {\"id\":\"a\"}\n\nid: 2\ndata: {\"id\":\"b\"}",
			expectedIDs: []string{"a", "b"},
		},
		{
			name:        "comments and events without data are skipped",
			body:        ": keep-alive\n\nevent: ping\n\ndata:{\"id\":\"a\"}\n\n",
			expectedIDs: []string{"a"},
		},
		{
			name:        "data split over several lines",
			body:        "data: {\"id\":\ndata: \"a\"}\n\n",
			expectedIDs: []string{"a"},
		},
		{
			name:        "done marker ends the stream",
			body:        "data: {\"id\":\"a\"}\n\ndata: [DONE]\n\ndata: {\"id\":\"b\"}\n\n",
			expectedIDs: []string{"a"},
		},
		{
			name:        "error event",
			body:        "data: {\"id\":\"a\"}\n\nevent: error\ndata: {\"errors\":[{\"code\":\"oom\",\"message\":\"out of memory\"}]}\n\n",
			expectedIDs: []string{"a"},
			expectedError: &APIError{
				StatusCode: http.StatusInternalServerError, Code: "oom", Message: "out of memory",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := NewStream(io.NopCloser(strings.NewReader(tt.body)))
			defer stream.Close()

			var ids []string
			var streamErr error
			for {
				chunk, err := stream.Next()
				if err != nil {
					streamErr = err
					break
				}
				ids = append(ids, chunk.ID)
			}

			assert.Equal(t, tt.expectedIDs, ids)
			if tt.expectedError == nil {
				require.True(t, errors.Is(streamErr, io.EOF), "unexpected error: %v", streamErr)
				return
			}

			assert.Equal(t, tt.expectedError, streamErr)
		})
	}
}

func TestStreamRejectsInvalidJSON(t *testing.T) {
	stream := NewStream(io.NopCloser(strings.NewReader("data: {\n\n")))

	_, err := stream.Next()

	require.ErrorContains(t, err, "decode watsonx stream event")
}
