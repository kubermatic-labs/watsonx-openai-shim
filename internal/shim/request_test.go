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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeRequest(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		expectedErr string
	}{
		{name: "valid", body: `{"model":"m","messages":[{"role":"user","content":"x"}]}`},
		{name: "trailing whitespace", body: "{\"model\":\"m\",\"messages\":[{\"role\":\"user\",\"content\":\"x\"}]}\n \t\n"},
		{name: "malformed JSON", body: `{`, expectedErr: "decode body"},
		{
			name:        "second JSON value",
			body:        `{"model":"m","messages":[{"role":"user","content":"x"}]}{"model":"other"}`,
			expectedErr: "unexpected data after the request object",
		},
		{
			name:        "trailing garbage",
			body:        `{"model":"m","messages":[{"role":"user","content":"x"}]} x`,
			expectedErr: "unexpected data after the request object",
		},
		{name: "missing model", body: `{"messages":[{"role":"user","content":"x"}]}`, expectedErr: "model is required"},
		{name: "no messages", body: `{"model":"m","messages":[]}`, expectedErr: "messages must not be empty"},
		{name: "unknown role", body: `{"model":"m","messages":[{"role":"bot","content":"x"}]}`, expectedErr: `messages[0]: unsupported role "bot"`},
		{name: "invalid stop", body: `{"model":"m","messages":[{"role":"user","content":"x"}],"stop":1}`, expectedErr: "stop must be a string or a list of strings"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := decodeRequest(strings.NewReader(tt.body))
			if tt.expectedErr != "" {
				require.ErrorIs(t, err, errInvalidRequest)
				assert.Contains(t, err.Error(), tt.expectedErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, "m", req.Model)
		})
	}
}

func TestToWatsonxRequest(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		expected    string
		expectedErr string
	}{
		{
			name: "defaults",
			body: `{"model":"m","messages":[{"role":"user","content":"Hi"}]}`,
			expected: `{"model_id":"m","project_id":"","max_tokens":100,
				"messages":[{"role":"user","content":"Hi"}]}`,
		},
		{
			name: "messages are adapted to watsonx",
			body: `{"model":"m","messages":[
				{"role":"developer","content":[{"type":"text","text":"Be "},{"type":"text","text":"brief."}]},
				{"role":"user","content":[{"type":"text","text":"Look"},{"type":"image_url","image_url":{"url":"data:x"}}]},
				{"role":"assistant","content":null,"reasoning_content":"hmm",
				 "tool_calls":[{"id":"call-1","type":"function","function":{"name":"weather","arguments":"{}"}}]},
				{"role":"tool","content":"sunny","tool_call_id":"call-1"}]}`,
			expected: `{"model_id":"m","project_id":"","max_tokens":100,"messages":[
				{"role":"system","content":"Be brief."},
				{"role":"user","content":[{"type":"text","text":"Look"},{"type":"image_url","image_url":{"url":"data:x"}}]},
				{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"weather","arguments":"{}"}}]},
				{"role":"tool","content":"sunny","tool_call_id":"call-1"}]}`,
		},
		{
			name: "parameters and tools are passed on",
			body: `{"model":"m","messages":[{"role":"user","content":"Hi"}],
				"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}}],
				"tool_choice":"required","max_tokens":50,"max_completion_tokens":60,"temperature":0.5,"top_p":0.9,
				"frequency_penalty":0.1,"presence_penalty":0.2,"stop":"END","n":2,"seed":42,
				"response_format":{"type":"json_object"},"logprobs":true,"top_logprobs":3,"logit_bias":{"1":-100},
				"user":"ignored","parallel_tool_calls":true}`,
			expected: `{"model_id":"m","project_id":"","messages":[{"role":"user","content":"Hi"}],
				"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}}],
				"tool_choice_option":"required","max_tokens":60,"temperature":0.5,"top_p":0.9,
				"frequency_penalty":0.1,"presence_penalty":0.2,"stop":["END"],"n":2,"seed":42,
				"response_format":{"type":"json_object"},"logprobs":true,"top_logprobs":3,"logit_bias":{"1":-100}}`,
		},
		{
			name: "named tool choice",
			body: `{"model":"m","messages":[{"role":"user","content":"Hi"}],"max_tokens":5,
				"tool_choice":{"type":"function","function":{"name":"weather"}}}`,
			expected: `{"model_id":"m","project_id":"","messages":[{"role":"user","content":"Hi"}],"max_tokens":5,
				"tool_choice":{"type":"function","function":{"name":"weather"}}}`,
		},
		{
			name:        "image in system message",
			body:        `{"model":"m","messages":[{"role":"system","content":[{"type":"image_url"}]}]}`,
			expectedErr: `messages[0]: content part type "image_url" is only supported in user messages`,
		},
		{
			name:        "invalid tool choice",
			body:        `{"model":"m","messages":[{"role":"user","content":"Hi"}],"tool_choice":1}`,
			expectedErr: "tool_choice must be a string or an object",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := decodeRequest(strings.NewReader(tt.body))
			require.NoError(t, err)

			wxReq, err := toWatsonxRequest(req, 100)
			if tt.expectedErr != "" {
				require.ErrorIs(t, err, errInvalidRequest)
				assert.Contains(t, err.Error(), tt.expectedErr)
				return
			}

			require.NoError(t, err)
			got, err := json.Marshal(wxReq)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(got))
		})
	}
}
