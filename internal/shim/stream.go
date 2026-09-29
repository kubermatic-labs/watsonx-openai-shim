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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/kubermatic-labs/watsonx-openai-shim/internal/watsonx"
)

// errStreamIncomplete is reported when the watsonx stream ends before every choice finished.
var errStreamIncomplete = errors.New("watsonx stream ended before every choice finished")

// sseWriter writes server-sent events.
type sseWriter struct {
	w      http.ResponseWriter
	rc     *http.ResponseController
	logger *slog.Logger
}

func (h *Handler) stream(w http.ResponseWriter, r *http.Request, req *ChatCompletionRequest, wxReq *watsonx.ChatRequest) {
	start := h.now()

	stream, err := h.client.ChatStream(r.Context(), wxReq)
	if err != nil {
		h.writeUpstreamError(w, r, req, err)
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	logger := loggerFrom(r.Context())
	sse := &sseWriter{w: w, rc: http.NewResponseController(w), logger: logger}
	includeUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage

	id, err := forward(sse, stream, includeUsage)
	if err != nil {
		if r.Context().Err() != nil {
			logger.Info("Client canceled chat completion", "model", req.Model)
			return
		}

		logger.Error("Chat completion stream failed", "id", id, "model", req.Model, "error", err)
		sse.fail(err)
		return
	}

	logCompletion(r.Context(), id, req, h.now().Sub(start))
}

// forward relays the converted chunks and ends the stream.
// It returns the completion ID.
func forward(sse *sseWriter, stream *watsonx.Stream, includeUsage bool) (string, error) {
	conv := newChunkConverter()

	for {
		chunk, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return conv.last.ID, err
		}
		sse.logger.Debug("Received watsonx chunk", "data", chunk.Raw)

		out, err := conv.convert(chunk)
		if err != nil {
			return conv.last.ID, err
		}

		if out != nil {
			if err := sse.event(out); err != nil {
				return conv.last.ID, err
			}
		}
	}

	if !conv.isComplete() {
		return conv.last.ID, errStreamIncomplete
	}

	if usage := conv.usageChunk(); includeUsage && usage != nil {
		if err := sse.event(usage); err != nil {
			return conv.last.ID, err
		}
	}

	return conv.last.ID, sse.done()
}

// fail sends an error event followed by the end of the stream.
func (s *sseWriter) fail(cause error) {
	var apiErr *watsonx.APIError
	errType := errorTypeUpstream
	if errors.As(cause, &apiErr) && isClientError(apiErr.StatusCode) {
		errType = errorTypeInvalidRequest
	}

	err := s.event(ErrorResponse{Error: ErrorDetail{Message: cause.Error(), Type: errType}})
	if err == nil {
		err = s.done()
	}

	if err != nil {
		s.logger.Warn("Write stream error event", "error", err)
	}
}

func (s *sseWriter) done() error {
	return s.write("[DONE]")
}

func (s *sseWriter) event(payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}

	return s.write(string(data))
}

func (s *sseWriter) write(data string) error {
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", data); err != nil {
		return fmt.Errorf("write event: %w", err)
	}

	if err := s.rc.Flush(); err != nil {
		return fmt.Errorf("flush event: %w", err)
	}

	return nil
}
