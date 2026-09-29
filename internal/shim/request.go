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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/kubermatic-labs/watsonx-openai-shim/internal/watsonx"
)

// maxRequestBytes bounds the request body size.
const maxRequestBytes = 10 << 20

var validRoles = []string{roleSystem, roleDeveloper, roleUser, roleAssistant, roleTool}

// errInvalidRequest marks errors caused by the client's request.
var errInvalidRequest = errors.New("invalid request")

func invalidRequest(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errInvalidRequest, fmt.Sprintf(format, args...))
}

func decodeRequest(body io.Reader) (*ChatCompletionRequest, error) {
	dec := json.NewDecoder(body)

	var req ChatCompletionRequest
	if err := dec.Decode(&req); err != nil {
		return nil, invalidRequest("decode body: %v", err)
	}

	// Decode stops after the first value, so anything but whitespace after it is rejected here.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, invalidRequest("decode body: unexpected data after the request object")
	}

	if req.Model == "" {
		return nil, invalidRequest("model is required")
	}

	if len(req.Messages) == 0 {
		return nil, invalidRequest("messages must not be empty")
	}

	for i, m := range req.Messages {
		if !slices.Contains(validRoles, m.Role) {
			return nil, invalidRequest("messages[%d]: unsupported role %q", i, m.Role)
		}
	}

	return &req, nil
}

// toWatsonxRequest converts the request into a watsonx chat request.
// The format of both APIs matches mostly, so most fields are passed on unchanged.
func toWatsonxRequest(req *ChatCompletionRequest, defaultMaxTokens int) (*watsonx.ChatRequest, error) {
	messages := make([]json.RawMessage, 0, len(req.Messages))
	for i, m := range req.Messages {
		message, err := toWatsonxMessage(m)
		if err != nil {
			return nil, invalidRequest("messages[%d]: %v", i, err)
		}
		messages = append(messages, message)
	}

	toolChoiceOption, toolChoice, err := splitToolChoice(req.ToolChoice)
	if err != nil {
		return nil, err
	}

	// watsonx knows max_completion_tokens only in newer releases, so max_tokens is used.
	maxTokens := defaultMaxTokens
	switch {
	case req.MaxCompletionTokens != nil:
		maxTokens = *req.MaxCompletionTokens
	case req.MaxTokens != nil:
		maxTokens = *req.MaxTokens
	}

	return &watsonx.ChatRequest{
		ModelID:          req.Model,
		Messages:         messages,
		Tools:            req.Tools,
		ToolChoice:       toolChoice,
		ToolChoiceOption: toolChoiceOption,
		MaxTokens:        &maxTokens,
		Temperature:      req.Temperature,
		TopP:             req.TopP,
		FrequencyPenalty: req.FrequencyPenalty,
		PresencePenalty:  req.PresencePenalty,
		Stop:             req.Stop,
		N:                req.N,
		Seed:             req.Seed,
		ResponseFormat:   req.ResponseFormat,
		Logprobs:         req.Logprobs,
		TopLogprobs:      req.TopLogprobs,
		LogitBias:        req.LogitBias,
	}, nil
}

func toWatsonxMessage(m ChatMessage) (json.RawMessage, error) {
	// watsonx has no developer role, which OpenAI introduced as the successor of system.
	if m.Role == roleDeveloper {
		m.Role = roleSystem
	}

	content, err := toWatsonxContent(m.Role, m.Content)
	if err != nil {
		return nil, err
	}
	m.Content = content

	message, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encode message: %w", err)
	}

	return message, nil
}

// toWatsonxContent drops null content and flattens content parts of non-user messages.
// watsonx accepts a list of content parts only for user messages.
func toWatsonxContent(role string, content json.RawMessage) (json.RawMessage, error) {
	if len(content) == 0 || bytes.Equal(content, []byte("null")) {
		return nil, nil
	}

	if role == roleUser || content[0] != '[' {
		return content, nil
	}

	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &parts); err != nil {
		return nil, errors.New("content must be a string or a list of content parts")
	}

	var sb strings.Builder
	for _, part := range parts {
		if part.Type != "text" {
			return nil, fmt.Errorf("content part type %q is only supported in user messages", part.Type)
		}
		sb.WriteString(part.Text)
	}

	text, err := json.Marshal(sb.String())
	if err != nil {
		return nil, fmt.Errorf("encode content: %w", err)
	}

	return text, nil
}

// splitToolChoice maps OpenAI's tool_choice to the two fields watsonx uses:
// tool_choice_option for "auto", "none" and "required", and tool_choice for a named function.
func splitToolChoice(raw json.RawMessage) (string, json.RawMessage, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil, nil
	}

	var option string
	if err := json.Unmarshal(raw, &option); err == nil {
		return option, nil, nil
	}

	if raw[0] != '{' {
		return "", nil, invalidRequest("tool_choice must be a string or an object")
	}

	return "", raw, nil
}
