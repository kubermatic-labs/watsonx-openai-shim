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
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strconv"
)

const (
	modelSpecsPath = "/ml/v1/foundation_model_specs"

	// modelSpecsVersion is the version query parameter of the model specs endpoint.
	modelSpecsVersion = "2024-05-31"

	// modelSpecsPageSize is the largest page size watsonx allows.
	modelSpecsPageSize = 200

	// availableModelsFilter excludes models whose lifecycle state is withdrawn,
	// i.e. models that can no longer be used.
	// Deprecated and constricted models are kept, since they still serve inference requests.
	availableModelsFilter = "!lifecycle_withdrawn"

	customModelsPath = "/ml/v4/custom_foundation_models"

	// customModelsVersion is the version query parameter of the custom models endpoint.
	customModelsVersion = "2024-05-01"

	// maxModelPages guards against a next link that never ends.
	maxModelPages = 20
)

// FoundationModel describes a model watsonx offers.
// Custom foundation models are described the same way.
type FoundationModel struct {
	ModelID          string       `json:"model_id"`
	Provider         string       `json:"provider"`
	Source           string       `json:"source"`
	ShortDescription string       `json:"short_description"`
	TaskIDs          []string     `json:"task_ids"`
	ModelLimits      *ModelLimits `json:"model_limits"`
}

// ModelLimits are the token limits of a model.
type ModelLimits struct {
	MaxSequenceLength int `json:"max_sequence_length"`
	MaxOutputTokens   int `json:"max_output_tokens"`
}

type foundationModelsPage struct {
	Resources []FoundationModel `json:"resources"`
	Next      *struct {
		Href string `json:"href"`
	} `json:"next"`
}

// ListModels returns the foundation models watsonx offers, except withdrawn ones.
// On CPD, the custom foundation models deployed on the instance follow them.
func (c *Client) ListModels(ctx context.Context) ([]FoundationModel, error) {
	models, err := c.listModelPages(ctx, modelSpecsPath, url.Values{
		versionParam: {modelSpecsVersion},
		"limit":      {strconv.Itoa(modelSpecsPageSize)},
		"filters":    {availableModelsFilter},
	})
	if err != nil {
		return nil, fmt.Errorf("list foundation models: %w", err)
	}

	if !c.isCPD {
		return models, nil
	}

	customModels, err := c.listModelPages(ctx, customModelsPath, url.Values{versionParam: {customModelsVersion}})
	if err != nil {
		return nil, fmt.Errorf("list custom foundation models: %w", err)
	}

	return append(models, customModels...), nil
}

// listModelPages returns the models of an endpoint, following all result pages.
func (c *Client) listModelPages(ctx context.Context, path string, query url.Values) ([]FoundationModel, error) {
	var models []FoundationModel
	start := ""

	for range maxModelPages {
		page, err := c.listModelsPage(ctx, path, query, start)
		if err != nil {
			return nil, err
		}
		models = append(models, page.Resources...)

		if page.Next == nil || page.Next.Href == "" {
			return models, nil
		}

		next, err := url.Parse(page.Next.Href)
		if err != nil {
			return nil, fmt.Errorf("parse watsonx next page link: %w", err)
		}

		start = next.Query().Get("start")
		if start == "" {
			return models, nil
		}
	}

	return nil, fmt.Errorf("watsonx model list exceeds %d pages", maxModelPages)
}

func (c *Client) listModelsPage(ctx context.Context, path string, query url.Values, start string) (*foundationModelsPage, error) {
	query = maps.Clone(query)
	if start != "" {
		query.Set("start", start)
	}

	endpoint := c.baseURL.JoinPath(path)
	endpoint.RawQuery = query.Encode()

	resp, err := c.do(ctx, http.MethodGet, endpoint, nil, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var page foundationModelsPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("decode watsonx model list: %w", err)
	}

	return &page, nil
}
