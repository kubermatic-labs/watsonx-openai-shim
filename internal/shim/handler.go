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

// Package shim serves the watsonx chat API as the OpenAI chat completion API.
package shim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/kubermatic-labs/watsonx-openai-shim/internal/watsonx"
)

const (
	errorTypeInvalidRequest = "invalid_request_error"
	errorTypeUpstream       = "upstream_error"
)

// Client calls the watsonx API.
type Client interface {
	Chat(ctx context.Context, req *watsonx.ChatRequest) (*watsonx.ChatResponse, error)
	ChatStream(ctx context.Context, req *watsonx.ChatRequest) (*watsonx.Stream, error)
	ListModels(ctx context.Context) ([]watsonx.FoundationModel, error)
}

// Options configures the handler.
type Options struct {
	// DefaultMaxTokens applies when a request sets no token limit.
	DefaultMaxTokens int
}

// Handler serves the OpenAI-compatible API.
type Handler struct {
	client Client
	opts   Options
	now    func() time.Time
}

// NewHandler creates a handler.
func NewHandler(client Client, opts Options) (*Handler, error) {
	if opts.DefaultMaxTokens <= 0 {
		return nil, errors.New("default max tokens must be positive")
	}

	return &Handler{client: client, opts: opts, now: time.Now}, nil
}

// Routes returns the HTTP routes of the shim.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", h.handleChatCompletions)
	mux.HandleFunc("GET /v1/models", h.handleModels)
	mux.HandleFunc("GET /v1/models/{id...}", h.handleModel)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	})

	return withRequestLogger(mux)
}

func (h *Handler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	req, err := decodeRequest(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, errorTypeInvalidRequest, err.Error())
		return
	}

	wxReq, err := toWatsonxRequest(req, h.opts.DefaultMaxTokens)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, errorTypeInvalidRequest, err.Error())
		return
	}

	if req.Stream {
		h.stream(w, r, req, wxReq)
		return
	}

	h.complete(w, r, req, wxReq)
}

func (h *Handler) complete(w http.ResponseWriter, r *http.Request, req *ChatCompletionRequest, wxReq *watsonx.ChatRequest) {
	start := h.now()

	resp, err := h.client.Chat(r.Context(), wxReq)
	if err != nil {
		h.writeUpstreamError(w, r, req, err)
		return
	}
	loggerFrom(r.Context()).Debug("Received watsonx response", "data", resp.Raw)

	completion, err := toChatCompletion(resp)
	if err != nil {
		h.writeUpstreamError(w, r, req, err)
		return
	}

	logCompletion(r.Context(), completion.ID, req, h.now().Sub(start))
	writeJSON(w, r, http.StatusOK, completion)
}

// writeUpstreamError passes client errors reported by watsonx on, and reports all others as 502.
// Authentication errors are the shim's fault, not the client's, so they become 502 as well.
func (h *Handler) writeUpstreamError(w http.ResponseWriter, r *http.Request, req *ChatCompletionRequest, err error) {
	logger := loggerFrom(r.Context())
	if r.Context().Err() != nil {
		logger.Info("Client canceled chat completion", "model", req.Model)
		return
	}

	logger.Error("Chat completion failed", "model", req.Model, "stream", req.Stream, "error", err)

	var apiErr *watsonx.APIError
	if errors.As(err, &apiErr) && isClientError(apiErr.StatusCode) {
		writeError(w, r, apiErr.StatusCode, errorTypeInvalidRequest, apiErr.Message)
		return
	}

	writeError(w, r, http.StatusBadGateway, errorTypeUpstream, err.Error())
}

func isClientError(status int) bool {
	return status >= http.StatusBadRequest && status < http.StatusInternalServerError &&
		status != http.StatusUnauthorized && status != http.StatusForbidden
}

func logCompletion(ctx context.Context, id string, req *ChatCompletionRequest, duration time.Duration) {
	loggerFrom(ctx).Info("Chat completion",
		"id", id,
		"model", req.Model,
		"stream", req.Stream,
		"duration", duration,
	)
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		loggerFrom(r.Context()).Warn("Write response", "error", err)
	}
}

func writeError(w http.ResponseWriter, r *http.Request, status int, errType, message string) {
	writeJSON(w, r, status, ErrorResponse{Error: ErrorDetail{Message: message, Type: errType}})
}
