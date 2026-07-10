package server

import (
	"context"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/kbsartain/simphony/internal/codexcmd"
	"github.com/kbsartain/simphony/pkg/api"
)

func liveModelCatalog(ctx context.Context, runtime api.AgentRuntimeConfig, workingDir string) (api.ModelCatalogResponse, error) {
	env := os.Environ()
	if runtime.APIKey != "" {
		env = replaceCatalogEnv(env, "OPENAI_API_KEY", runtime.APIKey)
	}
	if runtime.AuthToken != "" {
		env = replaceCatalogEnv(env, "OPENAI_AUTH_TOKEN", runtime.AuthToken)
	}
	if runtime.EndpointURL != "" && !isOfficialOpenAIEndpoint(runtime.EndpointURL) {
		env = replaceCatalogEnv(env, "OPENAI_BASE_URL", runtime.EndpointURL)
	}

	models, err := codexcmd.ListModels(ctx, runtime.Command, workingDir, env)
	if err != nil {
		return api.ModelCatalogResponse{}, err
	}
	result := api.ModelCatalogResponse{
		Provider:    "openai",
		Source:      "codex_app_server",
		RefreshedAt: time.Now(),
		Models:      make([]api.ModelCatalogEntry, 0, len(models)),
	}
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		if model.Hidden {
			continue
		}
		id := strings.TrimSpace(model.Model)
		if id == "" {
			id = strings.TrimSpace(model.ID)
		}
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		label := strings.TrimSpace(model.DisplayName)
		if label == "" {
			label = id
		}
		reasoning := make([]string, 0, len(model.SupportedReasoning))
		for _, option := range model.SupportedReasoning {
			if effort := strings.TrimSpace(option.ReasoningEffort); effort != "" {
				reasoning = append(reasoning, effort)
			}
		}
		result.Models = append(result.Models, api.ModelCatalogEntry{
			ID:               id,
			Label:            label,
			Description:      model.Description,
			DefaultReasoning: model.DefaultReasoningEffort,
			Reasoning:        reasoning,
		})
	}
	sort.SliceStable(result.Models, func(i, j int) bool {
		return strings.ToLower(result.Models[i].Label) < strings.ToLower(result.Models[j].Label)
	})
	return result, nil
}

func replaceCatalogEnv(env []string, key, value string) []string {
	prefix := strings.ToUpper(key) + "="
	result := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(strings.ToUpper(item), prefix) {
			result = append(result, item)
		}
	}
	return append(result, key+"="+value)
}

func isOfficialOpenAIEndpoint(endpoint string) bool {
	endpoint = strings.TrimRight(strings.ToLower(strings.TrimSpace(endpoint)), "/")
	return endpoint == "" || endpoint == "https://api.openai.com" || endpoint == "https://api.openai.com/v1"
}
