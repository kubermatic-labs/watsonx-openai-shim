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
	"fmt"

	"github.com/kubermatic-labs/watsonx-openai-shim/internal/watsonx"
)

// toFinishReason maps a watsonx finish reason to an OpenAI finish reason.
// The cancelled and error reasons are returned as errors, since OpenAI has no equivalent.
func toFinishReason(reason string) (string, error) {
	switch reason {
	case watsonx.FinishReasonTimeLimit:
		return watsonx.FinishReasonLength, nil
	case watsonx.FinishReasonCancelled, watsonx.FinishReasonError:
		return "", fmt.Errorf("watsonx ended the generation with finish reason %q", reason)
	default:
		return reason, nil
	}
}

func toChatCompletion(resp *watsonx.ChatResponse) (*ChatCompletionResponse, error) {
	choices := make([]Choice, 0, len(resp.Choices))
	for _, c := range resp.Choices {
		reason, err := toFinishReason(c.FinishReason)
		if err != nil {
			return nil, err
		}

		toolCalls := make([]ToolCall, 0, len(c.Message.ToolCalls))
		for _, tc := range c.Message.ToolCalls {
			toolCalls = append(toolCalls, ToolCall{
				ID:       tc.ID,
				Type:     tc.Type,
				Function: FunctionCall{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
			})
		}

		choices = append(choices, Choice{
			Index: c.Index,
			Message: AssistantMessage{
				Role:             roleAssistant,
				Content:          c.Message.Content,
				ReasoningContent: c.Message.ReasoningContent,
				Refusal:          c.Message.Refusal,
				ToolCalls:        toolCalls,
			},
			FinishReason: reason,
		})
	}

	return &ChatCompletionResponse{
		ID:      resp.ID,
		Object:  objectChatCompletion,
		Created: resp.Created,
		Model:   resp.ModelID,
		Choices: choices,
		Usage:   toUsage(resp.Usage),
	}, nil
}

func toUsage(usage *watsonx.Usage) *Usage {
	if usage == nil {
		return nil
	}

	return &Usage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
	}
}

// chunkConverter converts the chunks of one watsonx stream into OpenAI chunks.
type chunkConverter struct {
	// toolCalls counts the tool calls started per choice, to derive their index.
	toolCalls map[int]int
	// finished records every choice seen in the stream and whether it has finished.
	finished map[int]bool
	usage    *Usage
	last     ChatCompletionChunk
}

func newChunkConverter() *chunkConverter {
	return &chunkConverter{toolCalls: map[int]int{}, finished: map[int]bool{}}
}

// isComplete reports whether the stream had choices and all of them finished.
func (c *chunkConverter) isComplete() bool {
	for _, isFinished := range c.finished {
		if !isFinished {
			return false
		}
	}

	return len(c.finished) > 0
}

// convert returns the OpenAI chunk, or nil if the chunk only carries usage.
func (c *chunkConverter) convert(chunk *watsonx.ChatChunk) (*ChatCompletionChunk, error) {
	if chunk.Usage != nil {
		c.usage = toUsage(chunk.Usage)
	}

	c.last = ChatCompletionChunk{
		ID:      chunk.ID,
		Object:  objectChatCompletionChunk,
		Created: chunk.Created,
		Model:   chunk.ModelID,
	}

	var choices []ChunkChoice
	for _, choice := range chunk.Choices {
		if _, ok := c.finished[choice.Index]; !ok {
			c.finished[choice.Index] = false
		}

		converted, err := c.convertChoice(choice)
		if err != nil {
			return nil, err
		}

		if converted != nil {
			choices = append(choices, *converted)
		}
	}

	if len(choices) == 0 {
		return nil, nil
	}

	out := c.last
	out.Choices = choices

	return &out, nil
}

// convertChoice returns nil for a choice that carries no change.
func (c *chunkConverter) convertChoice(choice watsonx.ChunkChoice) (*ChunkChoice, error) {
	var finishReason *string
	if choice.FinishReason != "" {
		reason, err := toFinishReason(choice.FinishReason)
		if err != nil {
			return nil, err
		}
		finishReason = &reason
		c.finished[choice.Index] = true
	}

	d := choice.Delta
	delta := Delta{
		Role:             d.Role,
		Content:          d.Content,
		ReasoningContent: d.ReasoningContent,
		Refusal:          d.Refusal,
		ToolCalls:        c.toolCallDeltas(choice.Index, d.ToolCalls),
	}

	isEmpty := delta.Content == "" && delta.ReasoningContent == "" && delta.Refusal == "" && len(delta.ToolCalls) == 0
	if isEmpty && finishReason == nil && delta.Role == "" {
		return nil, nil
	}

	return &ChunkChoice{Index: choice.Index, Delta: delta, FinishReason: finishReason}, nil
}

// toolCallDeltas adds the index OpenAI clients need to assemble streamed tool calls.
// watsonx streams no index: a call with an ID starts a new call,
// and fragments without an ID continue the current one.
func (c *chunkConverter) toolCallDeltas(choice int, calls []watsonx.ToolCall) []ToolCallDelta {
	var deltas []ToolCallDelta
	for _, call := range calls {
		delta := ToolCallDelta{
			Function: FunctionDelta{Name: call.Function.Name, Arguments: call.Function.Arguments},
		}

		if call.ID != "" {
			c.toolCalls[choice]++
			delta.ID = call.ID
			delta.Type = call.Type
		}

		delta.Index = max(c.toolCalls[choice]-1, 0)
		deltas = append(deltas, delta)
	}

	return deltas
}

// usageChunk returns the final usage chunk, or nil if watsonx reported no usage.
func (c *chunkConverter) usageChunk() *ChatCompletionChunk {
	if c.usage == nil {
		return nil
	}

	out := c.last
	out.Choices = []ChunkChoice{}
	out.Usage = c.usage

	return &out
}
