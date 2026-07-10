package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/kbsartain/simphony/internal/agentruntime"
	"github.com/kbsartain/simphony/internal/config"
	"github.com/kbsartain/simphony/pkg/api"
)

func fetchModelCatalog(ctx context.Context, runtime api.AgentRuntimeConfig) (api.ModelCatalogResponse, error) {
	provider := strings.ToLower(strings.TrimSpace(runtime.ModelProvider))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(runtime.Provider))
	}
	endpoint := strings.TrimRight(strings.TrimSpace(runtime.EndpointURL), "/")
	if endpoint == "" {
		switch provider {
		case "", "codex", "openai":
			provider = "openai"
			endpoint = "https://api.openai.com/v1"
		case "anthropic", "claude":
			provider = "anthropic"
			endpoint = "https://api.anthropic.com"
		default:
			return api.ModelCatalogResponse{}, fmt.Errorf("endpoint_url is required to refresh the %s model catalog", provider)
		}
	}

	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return api.ModelCatalogResponse{}, fmt.Errorf("endpoint_url must be an HTTP(S) URL without embedded credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return api.ModelCatalogResponse{}, fmt.Errorf("endpoint_url must not contain a query string or fragment")
	}

	modelsURL := endpoint + "/models"
	if provider == "anthropic" && !strings.HasSuffix(strings.ToLower(endpoint), "/v1") {
		modelsURL = endpoint + "/v1/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return api.ModelCatalogResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	if runtime.APIKey != "" {
		if provider == "anthropic" {
			req.Header.Set("x-api-key", runtime.APIKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else {
			req.Header.Set("Authorization", "Bearer "+runtime.APIKey)
		}
	}

	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return api.ModelCatalogResponse{}, fmt.Errorf("request model catalog: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return api.ModelCatalogResponse{}, fmt.Errorf("model catalog returned HTTP %d", response.StatusCode)
	}
	var body struct {
		Data []struct {
			ID      string `json:"id"`
			Display string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return api.ModelCatalogResponse{}, fmt.Errorf("decode model catalog: %w", err)
	}
	models := make([]api.ModelCatalogEntry, 0, len(body.Data))
	seen := make(map[string]struct{}, len(body.Data))
	for _, item := range body.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		label := strings.TrimSpace(item.Display)
		if label == "" {
			label = id
		}
		models = append(models, api.ModelCatalogEntry{ID: id, Label: label})
	}
	sort.Slice(models, func(i, j int) bool { return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID) })
	return api.ModelCatalogResponse{Provider: provider, EndpointURL: endpoint, RefreshedAt: time.Now(), Models: models}, nil
}

func modelCatalogRuntime(cfg *api.WorkflowConfig, stage string) (api.AgentRuntimeConfig, string, error) {
	if cfg == nil {
		return api.AgentRuntimeConfig{}, "", fmt.Errorf("project settings are not resolved")
	}
	stage = strings.ToLower(strings.TrimSpace(stage))
	stage = strings.ReplaceAll(stage, "-", "_")
	stage = strings.ReplaceAll(stage, " ", "_")
	if stage == "" {
		return cfg.AgentRuntime, "", nil
	}
	switch stage {
	case "coding", "review", "review_resolution", "merge":
	default:
		return api.AgentRuntimeConfig{}, "", fmt.Errorf("unknown pipeline stage %q", stage)
	}
	return agentruntime.EffectiveConfig(&cfg.AgentRuntime, api.PipelineStage{Kind: stage}), stage, nil
}

func (s *Server) handleModelCatalog(w http.ResponseWriter, r *http.Request) {
	if s.workflowPath == "" {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{Error: api.APIError{Code: "not_found", Message: "Settings API is not configured"}})
		return
	}
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{Error: api.APIError{Code: "method_not_allowed", Message: "Only POST is allowed"}})
		return
	}
	s.settingsMu.Lock()
	def, err := config.LoadWorkflow(s.workflowPath)
	s.settingsMu.Unlock()
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, api.APIErrorResponse{Error: api.APIError{Code: "settings_load_error", Message: err.Error()}})
		return
	}
	cfg, err := config.ResolveConfig(def, s.workflowDir)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{Error: api.APIError{Code: "settings_validation_error", Message: err.Error()}})
		return
	}
	runtime, stage, err := modelCatalogRuntime(cfg, r.URL.Query().Get("stage"))
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{Error: api.APIError{Code: "invalid_stage", Message: err.Error()}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	result, err := fetchModelCatalog(ctx, runtime)
	if err != nil {
		s.writeJSON(w, http.StatusBadGateway, api.APIErrorResponse{Error: api.APIError{Code: "model_catalog_error", Message: err.Error()}})
		return
	}
	result.ExecutionProvider = runtime.Provider
	result.Stage = stage
	s.writeJSON(w, http.StatusOK, result)
}
