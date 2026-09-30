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
	"net/http"
	"strings"

	"github.com/kubermatic-labs/watsonx-openai-shim/internal/watsonx"
)

const objectModel = "model"

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	models, ok := h.listModels(w, r)
	if !ok {
		return
	}

	writeJSON(w, r, http.StatusOK, ModelList{Object: "list", Data: models})
}

func (h *Handler) handleModel(w http.ResponseWriter, r *http.Request) {
	models, ok := h.listModels(w, r)
	if !ok {
		return
	}

	id := r.PathValue("id")
	for _, m := range models {
		if m.ID == id {
			writeJSON(w, r, http.StatusOK, m)
			return
		}
	}

	writeError(w, r, http.StatusNotFound, errorTypeInvalidRequest, fmt.Sprintf("model %q not found", id))
}

// listModels fetches the models from watsonx and writes an error response on failure.
func (h *Handler) listModels(w http.ResponseWriter, r *http.Request) ([]Model, bool) {
	specs, err := h.client.ListModels(r.Context())
	if err != nil {
		loggerFrom(r.Context()).Error("List models failed", "error", err)
		writeError(w, r, http.StatusBadGateway, errorTypeUpstream, err.Error())
		return nil, false
	}

	created := h.now().Unix()
	models := make([]Model, 0, len(specs))
	for _, spec := range specs {
		models = append(models, toModel(spec, created))
	}

	return models, true
}

// toModel maps a watsonx model spec to an OpenAI model.
// watsonx reports no creation time, so the current time is used.
func toModel(spec watsonx.FoundationModel, created int64) Model {
	model := Model{
		ID:          spec.ModelID,
		Object:      objectModel,
		Created:     created,
		OwnedBy:     spec.Provider + " / " + spec.Source,
		Description: spec.ShortDescription,
	}

	if len(spec.TaskIDs) > 0 {
		model.Description = strings.TrimSpace(fmt.Sprintf("%s Supports tasks like %s.",
			spec.ShortDescription, strings.Join(spec.TaskIDs, ", ")))
	}

	if spec.ModelLimits != nil {
		model.MaxCompletionTokens = spec.ModelLimits.MaxOutputTokens
		model.TokenLimits = &TokenLimits{
			MaxSequenceLength: spec.ModelLimits.MaxSequenceLength,
			MaxOutputTokens:   spec.ModelLimits.MaxOutputTokens,
		}
	}

	return model
}
