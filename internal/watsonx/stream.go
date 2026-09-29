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
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	// maxEventBytes bounds the size of a single stream event.
	maxEventBytes = 1 << 20

	initialEventBufferBytes = 64 << 10

	doneMarker = "[DONE]"
)

// Stream reads the server-sent events of a streamed chat response.
type Stream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
}

// NewStream reads chat chunks from a server-sent event stream.
func NewStream(body io.ReadCloser) *Stream {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, initialEventBufferBytes), maxEventBytes)

	return &Stream{body: body, scanner: scanner}
}

// Next returns the next chunk, or io.EOF at the end of the stream.
// An error event is returned as an APIError.
func (s *Stream) Next() (*ChatChunk, error) {
	data, err := s.nextEventData()
	if err != nil {
		return nil, err
	}

	if data == doneMarker {
		return nil, io.EOF
	}

	var event struct {
		ChatChunk
		errorBody
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil, fmt.Errorf("decode watsonx stream event: %w", err)
	}

	if len(event.Errors) > 0 {
		return nil, event.apiError(http.StatusInternalServerError)
	}

	event.Raw = data

	return &event.ChatChunk, nil
}

// Close releases the stream and aborts the generation if it is still running.
func (s *Stream) Close() error {
	return s.body.Close()
}

// nextEventData returns the joined data lines of the next event that has any.
func (s *Stream) nextEventData() (string, error) {
	var lines []string
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" {
			if len(lines) > 0 {
				return strings.Join(lines, "\n"), nil
			}
			continue
		}

		if data, ok := strings.CutPrefix(line, "data:"); ok {
			lines = append(lines, strings.TrimPrefix(data, " "))
		}
	}

	if err := s.scanner.Err(); err != nil {
		return "", fmt.Errorf("read watsonx stream: %w", err)
	}

	if len(lines) > 0 {
		return strings.Join(lines, "\n"), nil
	}

	return "", io.EOF
}
