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
	"encoding/json"
	"fmt"
)

// Finish reasons of a chat choice.
const (
	FinishReasonStop      = "stop"
	FinishReasonLength    = "length"
	FinishReasonToolCalls = "tool_calls"
	FinishReasonTimeLimit = "time_limit"
	FinishReasonCancelled = "cancelled"
	FinishReasonError     = "error"
)

// ChatRequest is the body of a watsonx chat request.
// The client sets the project ID.
type ChatRequest struct {
	ModelID   string `json:"model_id"`
	ProjectID string `json:"project_id"`
	// Messages are passed on as is, since their format matches the OpenAI format.
	Messages         []json.RawMessage `json:"messages"`
	Tools            json.RawMessage   `json:"tools,omitempty"`
	ToolChoice       json.RawMessage   `json:"tool_choice,omitempty"`
	ToolChoiceOption string            `json:"tool_choice_option,omitempty"`
	MaxTokens        *int              `json:"max_tokens,omitempty"`
	Temperature      *float64          `json:"temperature,omitempty"`
	TopP             *float64          `json:"top_p,omitempty"`
	FrequencyPenalty *float64          `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64          `json:"presence_penalty,omitempty"`
	Stop             []string          `json:"stop,omitempty"`
	N                *int              `json:"n,omitempty"`
	Seed             *int64            `json:"seed,omitempty"`
	ResponseFormat   json.RawMessage   `json:"response_format,omitempty"`
	Logprobs         *bool             `json:"logprobs,omitempty"`
	TopLogprobs      *int              `json:"top_logprobs,omitempty"`
	LogitBias        json.RawMessage   `json:"logit_bias,omitempty"`
}

// ChatResponse is a non-streamed chat response.
type ChatResponse struct {
	ID      string       `json:"id"`
	ModelID string       `json:"model_id"`
	Created int64        `json:"created"`
	Choices []ChatChoice `json:"choices"`
	Usage   *Usage       `json:"usage,omitempty"`
	// Raw is the response body as received from watsonx, for debugging.
	Raw string `json:"-"`
}

// ChatChoice is a completion alternative of a chat response.
type ChatChoice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// ChatChunk is a single event of a streamed chat response.
type ChatChunk struct {
	ID      string        `json:"id"`
	ModelID string        `json:"model_id"`
	Created int64         `json:"created"`
	Choices []ChunkChoice `json:"choices"`
	Usage   *Usage        `json:"usage,omitempty"`
	// Raw is the event data as received from watsonx, for debugging.
	Raw string `json:"-"`
}

// ChunkChoice carries an incremental update of a choice.
type ChunkChoice struct {
	Index        int     `json:"index"`
	Delta        Message `json:"delta"`
	FinishReason string  `json:"finish_reason"`
}

// Message is a generated message, or the change a chunk applies to it.
type Message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content"`
	Refusal          string     `json:"refusal"`
	ToolCalls        []ToolCall `json:"tool_calls"`
}

// ToolCall is a function call requested by the model.
// Streamed follow-up chunks of a call have an empty ID and name.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall names the function and its JSON-encoded arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Usage reports token counts.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// APIError is an error response of watsonx.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("watsonx returned status %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}

	return fmt.Sprintf("watsonx returned status %d: %s", e.StatusCode, e.Message)
}

// errorBody is the watsonx error format, sent as a response body or a stream event.
type errorBody struct {
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	StatusCode int `json:"status_code"`
}

func (b *errorBody) apiError(statusCode int) *APIError {
	if b.StatusCode != 0 {
		statusCode = b.StatusCode
	}

	return &APIError{StatusCode: statusCode, Code: b.Errors[0].Code, Message: b.Errors[0].Message}
}
