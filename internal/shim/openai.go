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
)

// Message roles.
const (
	roleSystem    = "system"
	roleDeveloper = "developer"
	roleUser      = "user"
	roleAssistant = "assistant"
	roleTool      = "tool"
)

const (
	objectChatCompletion      = "chat.completion"
	objectChatCompletionChunk = "chat.completion.chunk"
)

// ChatCompletionRequest is the subset of the OpenAI chat completion request the shim forwards.
// Other fields are ignored.
type ChatCompletionRequest struct {
	Model               string          `json:"model"`
	Messages            []ChatMessage   `json:"messages"`
	Stream              bool            `json:"stream"`
	StreamOptions       *StreamOptions  `json:"stream_options,omitempty"`
	Tools               json.RawMessage `json:"tools,omitempty"`
	ToolChoice          json.RawMessage `json:"tool_choice,omitempty"`
	MaxTokens           *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	FrequencyPenalty    *float64        `json:"frequency_penalty,omitempty"`
	PresencePenalty     *float64        `json:"presence_penalty,omitempty"`
	Stop                StopSequences   `json:"stop,omitempty"`
	N                   *int            `json:"n,omitempty"`
	Seed                *int64          `json:"seed,omitempty"`
	ResponseFormat      json.RawMessage `json:"response_format,omitempty"`
	Logprobs            *bool           `json:"logprobs,omitempty"`
	TopLogprobs         *int            `json:"top_logprobs,omitempty"`
	LogitBias           json.RawMessage `json:"logit_bias,omitempty"`
}

// StreamOptions controls streamed responses.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ChatMessage is a message of the conversation.
// It holds the fields watsonx accepts; other fields are dropped.
type ChatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// StopSequences accepts OpenAI's stop field, which is either a string or a list of strings.
type StopSequences []string

func (s *StopSequences) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*s = nil
		return nil
	}

	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = StopSequences{single}
		return nil
	}

	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return errors.New("stop must be a string or a list of strings")
	}

	*s = list

	return nil
}

// ChatCompletionResponse is a non-streamed chat completion.
type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Choice is a completion alternative.
type Choice struct {
	Index        int              `json:"index"`
	Message      AssistantMessage `json:"message"`
	FinishReason string           `json:"finish_reason"`
}

// AssistantMessage is the generated message.
// reasoning_content is no OpenAI field, but understood by many OpenAI-compatible clients.
type AssistantMessage struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	Refusal          string     `json:"refusal,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall is a function call requested by the model.
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

// ChatCompletionChunk is a single server-sent event of a streamed chat completion.
type ChatCompletionChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`
	Usage   *Usage        `json:"usage,omitempty"`
}

// ChunkChoice carries an incremental update of a choice.
type ChunkChoice struct {
	Index        int     `json:"index"`
	Delta        Delta   `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

// Delta is the change a chunk applies to the message.
type Delta struct {
	Role             string          `json:"role,omitempty"`
	Content          string          `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	Refusal          string          `json:"refusal,omitempty"`
	ToolCalls        []ToolCallDelta `json:"tool_calls,omitempty"`
}

// ToolCallDelta is a fragment of a tool call.
// Only the first fragment of a call carries its ID, type and name.
type ToolCallDelta struct {
	Index    int           `json:"index"`
	ID       string        `json:"id,omitempty"`
	Type     string        `json:"type,omitempty"`
	Function FunctionDelta `json:"function"`
}

// FunctionDelta is a fragment of a function call.
type FunctionDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// Usage reports token counts.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ModelList is the response of the models endpoint.
type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// Model describes a model watsonx offers.
// description, max_completion_tokens and token_limits are no OpenAI fields.
type Model struct {
	ID                  string       `json:"id"`
	Object              string       `json:"object"`
	Created             int64        `json:"created"`
	OwnedBy             string       `json:"owned_by"`
	Description         string       `json:"description,omitempty"`
	MaxCompletionTokens int          `json:"max_completion_tokens,omitempty"`
	TokenLimits         *TokenLimits `json:"token_limits,omitempty"`
}

// TokenLimits are the token limits of a model.
type TokenLimits struct {
	MaxSequenceLength int `json:"max_sequence_length"`
	MaxOutputTokens   int `json:"max_output_tokens"`
}

// ErrorResponse is the OpenAI error envelope.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail describes a failed request.
type ErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}
