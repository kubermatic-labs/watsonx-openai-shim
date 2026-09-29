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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListModels(t *testing.T) {
	const (
		granite = `{"model_id":"ibm/granite","provider":"IBM","source":"IBM","short_description":"Granite.",` +
			`"task_ids":["summarization"],"model_limits":{"max_sequence_length":8192,"max_output_tokens":4096},"label":"x"}`
		llama  = `{"model_id":"meta-llama/llama","provider":"Meta","source":"Hugging Face","short_description":"Llama."}`
		custom = `{"model_id":"custom/defense","provider":"BWI","source":"custom","short_description":"Custom."}`

		specsQuery  = modelSpecsPath + "?filters=%21lifecycle_withdrawn&limit=200&version=2024-05-31"
		customQuery = customModelsPath + "?version=2024-05-01"
	)

	graniteModel := FoundationModel{
		ModelID: "ibm/granite", Provider: "IBM", Source: "IBM", ShortDescription: "Granite.",
		TaskIDs:     []string{"summarization"},
		ModelLimits: &ModelLimits{MaxSequenceLength: 8192, MaxOutputTokens: 4096},
	}
	llamaModel := FoundationModel{ModelID: "meta-llama/llama", Provider: "Meta", Source: "Hugging Face", ShortDescription: "Llama."}
	customModel := FoundationModel{ModelID: "custom/defense", Provider: "BWI", Source: "custom", ShortDescription: "Custom."}

	tests := []struct {
		name             string
		authMode         AuthMode
		pages            map[string]string
		customPages      map[string]string
		expected         []FoundationModel
		expectedRequests []string
		expectedErr      string
	}{
		{
			name:             "IBM Cloud lists no custom models",
			authMode:         AuthModeIAM,
			pages:            map[string]string{"": `{"total_count":1,"limit":200,"first":{"href":"x"},"resources":[` + granite + `]}`},
			expected:         []FoundationModel{graniteModel},
			expectedRequests: []string{specsQuery},
		},
		{
			name:             "CPD appends custom models",
			authMode:         AuthModeCPD,
			pages:            map[string]string{"": `{"resources":[` + granite + `]}`},
			customPages:      map[string]string{"": `{"total_count":1,"resources":[` + custom + `]}`},
			expected:         []FoundationModel{graniteModel, customModel},
			expectedRequests: []string{specsQuery, customQuery},
		},
		{
			name:     "next pages are followed",
			authMode: AuthModeCPD,
			pages: map[string]string{
				"": `{"resources":[` + granite + `],` +
					`"next":{"href":"https://cpd.example.com/ml/v1/foundation_model_specs?limit=200&start=abc&version=2024-05-31"}}`,
				"abc": `{"resources":[` + llama + `]}`,
			},
			customPages: map[string]string{
				"":    `{"resources":[],"next":{"href":"/ml/v4/custom_foundation_models?start=def&version=2024-05-01"}}`,
				"def": `{"resources":[` + custom + `]}`,
			},
			expected: []FoundationModel{graniteModel, llamaModel, customModel},
			expectedRequests: []string{
				specsQuery, modelSpecsPath + "?filters=%21lifecycle_withdrawn&limit=200&start=abc&version=2024-05-31",
				customQuery, customModelsPath + "?start=def&version=2024-05-01",
			},
		},
		{
			name:             "foundation model failure",
			authMode:         AuthModeCPD,
			pages:            map[string]string{},
			expectedRequests: []string{specsQuery},
			expectedErr:      "list foundation models: watsonx returned status 404",
		},
		{
			name:             "custom model failure",
			authMode:         AuthModeCPD,
			pages:            map[string]string{"": `{"resources":[]}`},
			customPages:      map[string]string{},
			expectedRequests: []string{specsQuery, customQuery},
			expectedErr:      "list custom foundation models: watsonx returned status 404",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, fake := newTestClient(t, tt.authMode)
			fake.modelPages = tt.pages
			fake.customModelPages = tt.customPages

			models, err := client.ListModels(t.Context())

			var requests []string
			for _, req := range fake.requests {
				assert.Equal(t, "application/json", req.accept)
				assert.True(t, strings.HasPrefix(req.authorization, "Bearer "))
				requests = append(requests, req.path+"?"+req.query)
			}
			assert.Equal(t, tt.expectedRequests, requests)

			if tt.expectedErr != "" {
				require.ErrorContains(t, err, tt.expectedErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, models)
		})
	}
}

func TestListModelsRejectsEndlessPages(t *testing.T) {
	client, fake := newTestClient(t, AuthModeCPD)
	loop := `{"resources":[],"next":{"href":"/ml/v1/foundation_model_specs?start=again"}}`
	fake.modelPages = map[string]string{"": loop, "again": loop}

	_, err := client.ListModels(t.Context())

	require.ErrorContains(t, err, "exceeds 20 pages")
	assert.Len(t, fake.requests, maxModelPages)
}
