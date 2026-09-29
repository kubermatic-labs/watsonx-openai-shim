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
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kubermatic-labs/watsonx-openai-shim/internal/watsonx"
)

var testModels = []watsonx.FoundationModel{
	{
		ModelID:          testModel,
		Provider:         "IBM",
		Source:           "IBM",
		ShortDescription: "A granite model.",
		TaskIDs:          []string{"question_answering", "summarization"},
		ModelLimits:      &watsonx.ModelLimits{MaxSequenceLength: 131072, MaxOutputTokens: 8192},
	},
	{
		ModelID:  "meta-llama/llama-3-3-70b-instruct",
		Provider: "Meta",
		Source:   "Hugging Face",
	},
}

const (
	modelsPath = "/v1/models"

	testGraniteModelJSON = `{"id":"` + testModel + `","object":"model","created":1790000000,"owned_by":"IBM / IBM",` +
		`"description":"A granite model. Supports tasks like question_answering, summarization.",` +
		`"max_completion_tokens":8192,"token_limits":{"max_sequence_length":131072,"max_output_tokens":8192}}`
	testLlamaModelJSON = `{"id":"meta-llama/llama-3-3-70b-instruct","object":"model","created":1790000000,` +
		`"owned_by":"Meta / Hugging Face"}`
)

func TestModels(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		client         *fakeClient
		expectedStatus int
		expected       string
	}{
		{
			name:           "list",
			path:           modelsPath,
			client:         &fakeClient{models: testModels},
			expectedStatus: http.StatusOK,
			expected:       `{"object":"list","data":[` + testGraniteModelJSON + `,` + testLlamaModelJSON + `]}`,
		},
		{
			name:           "empty list",
			path:           modelsPath,
			client:         &fakeClient{},
			expectedStatus: http.StatusOK,
			expected:       `{"object":"list","data":[]}`,
		},
		{
			name:           "model with slashes in its ID",
			path:           modelsPath + "/" + testModel,
			client:         &fakeClient{models: testModels},
			expectedStatus: http.StatusOK,
			expected:       testGraniteModelJSON,
		},
		{
			name:           "unknown model",
			path:           modelsPath + "/unknown",
			client:         &fakeClient{models: testModels},
			expectedStatus: http.StatusNotFound,
			expected:       `{"error":{"message":"model \"unknown\" not found","type":"invalid_request_error"}}`,
		},
		{
			name:           "watsonx failure",
			path:           modelsPath,
			client:         &fakeClient{err: errors.New("call watsonx: connection refused")},
			expectedStatus: http.StatusBadGateway,
			expected:       `{"error":{"message":"call watsonx: connection refused","type":"upstream_error"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t, tt.client)

			resp := doRequest(t, http.MethodGet, srv.URL+tt.path, "")

			require.Equal(t, tt.expectedStatus, resp.status)
			assert.JSONEq(t, tt.expected, string(resp.body))
		})
	}
}
