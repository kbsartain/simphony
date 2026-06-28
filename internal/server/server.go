package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kbsartain/simphony/internal/config"
	"github.com/kbsartain/simphony/internal/tracker"
	"github.com/kbsartain/simphony/pkg/api"
	"gopkg.in/yaml.v3"
)

const settingsSecretMask = "********"

// Orchestrator is the subset of orchestrator.Orchestrator methods used by the HTTP server.
type Orchestrator interface {
	Snapshot() api.StateSnapshot
	IssueDetail(identifier string) (api.IssueDetailResponse, bool)
	Refresh() api.RefreshResponse
}

// SettingsApplier applies validated workflow settings to running services.
type SettingsApplier func(def *api.WorkflowDefinition, cfg *api.WorkflowConfig) error

// Server wraps the HTTP server and orchestrator reference.
type Server struct {
	orch            Orchestrator
	port            int
	mux             *http.ServeMux
	httpServer      *http.Server
	workflowPath    string
	workflowDir     string
	settingsApplier SettingsApplier
	settingsMu      sync.Mutex
}

// New creates a new Server. Call Start to begin listening.
func New(orch Orchestrator, port int) *Server {
	return NewWithSettings(orch, port, "", nil)
}

// NewWithSettings creates a Server with optional workflow settings endpoints.
func NewWithSettings(orch Orchestrator, port int, workflowPath string, applier SettingsApplier) *Server {
	s := &Server{
		orch:            orch,
		port:            port,
		mux:             http.NewServeMux(),
		workflowPath:    workflowPath,
		settingsApplier: applier,
	}
	if workflowPath != "" {
		s.workflowDir = filepath.Dir(workflowPath)
	}

	s.registerRoutes()

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: s.mux,
	}

	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/api/v1/runtime-mode", s.withCORS(s.handleRuntimeMode))
	s.mux.HandleFunc("/api/v1/registry/bootstrap", s.withCORS(s.handleRegistryBootstrap))
	s.mux.HandleFunc("/api/v1/state", s.withCORS(s.handleState))
	s.mux.HandleFunc("/api/v1/refresh", s.withCORS(s.handleRefresh))
	s.mux.HandleFunc("/api/v1/settings/validate-tracker", s.withCORS(s.handleValidateTrackerSettings))
	s.mux.HandleFunc("/api/v1/settings", s.withCORS(s.handleSettings))
	s.mux.HandleFunc("/api/v1/", s.withCORS(s.handleAPIv1)) // catch-all for /api/v1/{issue_identifier}
	s.mux.HandleFunc("/api/", s.withCORS(s.handleAPINotFound))

	// Static files from dashboard/dist.
	distDir := filepath.Join("dashboard", "dist")
	if _, err := os.Stat(distDir); err == nil {
		fs := http.FileServer(http.Dir(distDir))
		s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			s.setCORS(w)
			// API paths should not reach here, but guard anyway.
			if strings.HasPrefix(r.URL.Path, "/api/") {
				s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
					Error: api.APIError{Code: "not_found", Message: "API endpoint not found"},
				})
				return
			}

			cleanPath := pathpkg.Clean("/" + r.URL.Path)
			assetPath := filepath.Join(distDir, filepath.FromSlash(strings.TrimPrefix(cleanPath, "/")))
			if info, err := os.Stat(assetPath); err == nil && !info.IsDir() {
				fs.ServeHTTP(w, r)
				return
			}

			// For SPA routing, missing extensionless paths serve index.html.
			if pathpkg.Ext(cleanPath) != "" {
				http.NotFound(w, r)
				return
			}
			r.URL.Path = "/"
			fs.ServeHTTP(w, r)
		})
	} else {
		log.Printf("server warning: dashboard dist not found at %s", distDir)
		s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			s.setCORS(w)
			s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
				Error: api.APIError{Code: "not_found", Message: "Dashboard not built"},
			})
		})
	}
}

// Start begins listening. Blocks until the context is cancelled or an error occurs.
func (s *Server) Start(ctx context.Context) error {
	log.Printf("server listening on http://localhost:%d", s.port)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func (s *Server) withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

func (s *Server) setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("server json encode error: %v", err)
	}
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only GET is allowed"},
		})
		return
	}
	snapshot := s.orch.Snapshot()
	s.writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only POST is allowed"},
		})
		return
	}
	resp := s.orch.Refresh()
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRuntimeMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only GET is allowed"},
		})
		return
	}
	s.writeJSON(w, http.StatusOK, api.RuntimeModeResponse{
		Mode:                  api.RuntimeModeSingleWorkflow,
		WorkflowPath:          s.workflowPath,
		ChangeRequiresRestart: true,
	})
}

func (s *Server) handleRegistryBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only POST is allowed"},
		})
		return
	}
	if s.workflowPath == "" {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
			Error: api.APIError{Code: "workflow_not_configured", Message: "Registry bootstrap requires a configured workflow path"},
		})
		return
	}

	response, content, err := s.buildRegistryBootstrap()
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, api.APIErrorResponse{
			Error: api.APIError{Code: "registry_bootstrap_error", Message: err.Error()},
		})
		return
	}

	info, err := os.Stat(response.RegistryPath)
	if err == nil {
		if info.IsDir() {
			s.writeJSON(w, http.StatusConflict, api.APIErrorResponse{
				Error: api.APIError{Code: "registry_path_is_directory", Message: fmt.Sprintf("%s is a directory", response.RegistryPath)},
			})
			return
		}
		response.Created = false
		s.writeJSON(w, http.StatusOK, response)
		return
	}
	if !os.IsNotExist(err) {
		s.writeJSON(w, http.StatusInternalServerError, api.APIErrorResponse{
			Error: api.APIError{Code: "registry_stat_error", Message: err.Error()},
		})
		return
	}
	if err := os.WriteFile(response.RegistryPath, content, 0o644); err != nil {
		s.writeJSON(w, http.StatusInternalServerError, api.APIErrorResponse{
			Error: api.APIError{Code: "registry_write_error", Message: err.Error()},
		})
		return
	}
	response.Created = true
	s.writeJSON(w, http.StatusCreated, response)
}

type bootstrapRegistryYAML struct {
	Server   bootstrapRegistryServer    `yaml:"server"`
	Security bootstrapRegistrySecurity  `yaml:"security,omitempty"`
	Projects []bootstrapRegistryProject `yaml:"projects"`
}

type bootstrapRegistryServer struct {
	BindAddress      string `yaml:"bind_address"`
	Port             int    `yaml:"port"`
	DashboardEnabled bool   `yaml:"dashboard_enabled"`
	APIPrefix        string `yaml:"api_prefix"`
}

type bootstrapRegistrySecurity struct {
	AllowWorkspaceUnderRegistryDir bool `yaml:"allow_workspace_under_registry_dir,omitempty"`
}

type bootstrapRegistryProject struct {
	ID           string `yaml:"id"`
	Name         string `yaml:"name"`
	WorkflowPath string `yaml:"workflow_path"`
}

func (s *Server) buildRegistryBootstrap() (api.RegistryBootstrapResponse, []byte, error) {
	workflowPath, err := filepath.Abs(s.workflowPath)
	if err != nil {
		return api.RegistryBootstrapResponse{}, nil, fmt.Errorf("resolve workflow path: %w", err)
	}
	workflowDir := filepath.Dir(workflowPath)
	registryPath := filepath.Join(workflowDir, "simphony.yaml")
	workflowRel, err := filepath.Rel(workflowDir, workflowPath)
	if err != nil {
		return api.RegistryBootstrapResponse{}, nil, fmt.Errorf("resolve workflow relative path: %w", err)
	}

	projectName := filepath.Base(workflowDir)
	if strings.TrimSpace(projectName) == "" || projectName == "." || projectName == string(filepath.Separator) {
		projectName = "Project"
	}
	projectID := registryProjectID(projectName)

	serverPort := s.port
	if serverPort == 0 {
		serverPort = 8080
	}

	registryFile := bootstrapRegistryYAML{
		Server: bootstrapRegistryServer{
			BindAddress:      "127.0.0.1",
			Port:             serverPort,
			DashboardEnabled: true,
			APIPrefix:        "/api/v1",
		},
		Projects: []bootstrapRegistryProject{
			{
				ID:           projectID,
				Name:         projectName,
				WorkflowPath: filepath.ToSlash(workflowRel),
			},
		},
	}
	if workspaceUnderRegistryDir(workflowPath, workflowDir) {
		registryFile.Security.AllowWorkspaceUnderRegistryDir = true
	}
	content, err := yaml.Marshal(registryFile)
	if err != nil {
		return api.RegistryBootstrapResponse{}, nil, fmt.Errorf("render registry yaml: %w", err)
	}

	response := api.RegistryBootstrapResponse{
		RegistryPath: registryPath,
		WorkflowPath: workflowPath,
		ProjectID:    projectID,
		ProjectName:  projectName,
		Command:      fmt.Sprintf("simphony -config %s", registryPath),
	}
	return response, content, nil
}

var registryProjectIDInvalid = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func registryProjectID(name string) string {
	id := strings.ToLower(strings.Trim(registryProjectIDInvalid.ReplaceAllString(name, "-"), ".-_"))
	if id == "" {
		return "project"
	}
	first := id[0]
	if (first >= 'a' && first <= 'z') || (first >= '0' && first <= '9') {
		return id
	}
	return "project-" + id
}

func workspaceUnderRegistryDir(workflowPath string, registryDir string) bool {
	def, err := config.LoadWorkflow(workflowPath)
	if err != nil {
		return false
	}
	cfg, err := config.ResolveConfig(def, filepath.Dir(workflowPath))
	if err != nil {
		return false
	}
	workspaceRoot, err := filepath.Abs(cfg.Workspace.Root)
	if err != nil {
		return false
	}
	registryDir, err = filepath.Abs(registryDir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(registryDir, workspaceRoot)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if s.workflowPath == "" {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
			Error: api.APIError{Code: "not_found", Message: "Settings API is not configured"},
		})
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleSettingsGet(w)
	case http.MethodPut:
		s.handleSettingsPut(w, r)
	default:
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only GET or PUT is allowed"},
		})
	}
}

func (s *Server) handleSettingsGet(w http.ResponseWriter) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()

	def, err := config.LoadWorkflow(s.workflowPath)
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, api.APIErrorResponse{
			Error: api.APIError{Code: "settings_load_error", Message: err.Error()},
		})
		return
	}

	cfg, err := config.ResolveConfig(def, s.workflowDir)
	var validationError *string
	if err != nil {
		msg := err.Error()
		validationError = &msg
	}

	s.writeJSON(w, http.StatusOK, api.SettingsResponse{
		WorkflowPath:    s.workflowPath,
		Config:          sanitizeSettingsConfigForResponse(def.Config),
		ResolvedConfig:  settingsConfigForResponse(cfg),
		PromptTemplate:  def.PromptTemplate,
		ValidationError: validationError,
	})
}

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	var req api.SettingsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{
			Error: api.APIError{Code: "bad_request", Message: fmt.Sprintf("Invalid JSON: %v", err)},
		})
		return
	}
	if req.Config == nil {
		s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{
			Error: api.APIError{Code: "bad_request", Message: "config is required"},
		})
		return
	}

	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()

	current, err := config.LoadWorkflow(s.workflowPath)
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, api.APIErrorResponse{
			Error: api.APIError{Code: "settings_load_error", Message: err.Error()},
		})
		return
	}

	promptTemplate := current.PromptTemplate
	if req.PromptTemplate != nil {
		promptTemplate = *req.PromptTemplate
	}
	def := &api.WorkflowDefinition{
		Config:         mergeMaskedSecrets(req.Config, current.Config),
		PromptTemplate: promptTemplate,
	}
	cfg, err := config.ResolveConfig(def, s.workflowDir)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{
			Error: api.APIError{Code: "settings_validation_error", Message: err.Error()},
		})
		return
	}
	currentCfg, currentCfgErr := config.ResolveConfig(current, s.workflowDir)
	if s.settingsApplier != nil {
		if err := s.settingsApplier(def, cfg); err != nil {
			s.writeJSON(w, http.StatusInternalServerError, api.APIErrorResponse{
				Error: api.APIError{Code: "settings_apply_error", Message: err.Error()},
			})
			return
		}
	}
	if err := config.SaveWorkflow(s.workflowPath, def); err != nil {
		if s.settingsApplier != nil && currentCfgErr == nil {
			if rollbackErr := s.settingsApplier(current, currentCfg); rollbackErr != nil {
				err = fmt.Errorf("%w; runtime rollback failed: %v", err, rollbackErr)
			}
		}
		s.writeJSON(w, http.StatusInternalServerError, api.APIErrorResponse{
			Error: api.APIError{Code: "settings_save_error", Message: err.Error()},
		})
		return
	}

	s.writeJSON(w, http.StatusOK, api.SettingsResponse{
		WorkflowPath:   s.workflowPath,
		Config:         sanitizeSettingsConfigForResponse(def.Config),
		ResolvedConfig: settingsConfigForResponse(cfg),
		PromptTemplate: def.PromptTemplate,
	})
}

func (s *Server) handleValidateTrackerSettings(w http.ResponseWriter, r *http.Request) {
	if s.workflowPath == "" {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
			Error: api.APIError{Code: "not_found", Message: "Settings API is not configured"},
		})
		return
	}
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only POST is allowed"},
		})
		return
	}

	var req api.SettingsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{
			Error: api.APIError{Code: "bad_request", Message: fmt.Sprintf("Invalid JSON: %v", err)},
		})
		return
	}
	if req.Config == nil {
		s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{
			Error: api.APIError{Code: "bad_request", Message: "config is required"},
		})
		return
	}

	s.settingsMu.Lock()
	current, err := config.LoadWorkflow(s.workflowPath)
	s.settingsMu.Unlock()
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, api.APIErrorResponse{
			Error: api.APIError{Code: "settings_load_error", Message: err.Error()},
		})
		return
	}

	def := &api.WorkflowDefinition{
		Config:         mergeMaskedSecrets(req.Config, current.Config),
		PromptTemplate: current.PromptTemplate,
	}
	if req.PromptTemplate != nil {
		def.PromptTemplate = *req.PromptTemplate
	}
	cfg, err := config.ResolveConfig(def, s.workflowDir)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{
			Error: api.APIError{Code: "settings_validation_error", Message: err.Error()},
		})
		return
	}

	client, err := tracker.NewLinearClient(cfg.Tracker)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{
			Error: api.APIError{Code: "settings_validation_error", Message: err.Error()},
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	issues, err := client.FetchCandidateIssues(ctx)
	if err != nil {
		s.writeJSON(w, http.StatusBadGateway, api.APIErrorResponse{
			Error: api.APIError{Code: "linear_validation_error", Message: err.Error()},
		})
		return
	}

	s.writeJSON(w, http.StatusOK, api.SettingsValidationResponse{
		OK:             true,
		ProjectSlug:    cfg.Tracker.ProjectSlug,
		ActiveStates:   cfg.Tracker.ActiveStates,
		CandidateCount: len(issues),
		Message:        "Linear settings validated",
	})
}

func settingsConfigForResponse(cfg *api.WorkflowConfig) api.WorkflowConfig {
	if cfg == nil {
		return api.WorkflowConfig{}
	}
	safe := *cfg
	if safe.Tracker.APIKey != "" {
		safe.Tracker.APIKey = settingsSecretMask
	}
	safe.AgentRuntime = runtimeConfigForResponse(safe.AgentRuntime)
	safe.Codex = runtimeConfigForResponse(safe.Codex)
	safe.Claude = runtimeConfigForResponse(safe.Claude)
	return safe
}

func runtimeConfigForResponse(runtime api.AgentRuntimeConfig) api.AgentRuntimeConfig {
	runtime.APIKey = ""
	runtime.AuthToken = ""
	if len(runtime.Env) > 0 {
		runtime.Env = maskEnvMap(runtime.Env)
	}
	if len(runtime.StageOverrides) > 0 {
		masked := make(map[string]api.AgentStageOverride, len(runtime.StageOverrides))
		for stage, override := range runtime.StageOverrides {
			override.APIKey = ""
			override.AuthToken = ""
			if len(override.Env) > 0 {
				override.Env = maskEnvMap(override.Env)
			}
			masked[stage] = override
		}
		runtime.StageOverrides = masked
	}
	return runtime
}

func sanitizeSettingsConfigForResponse(configMap map[string]interface{}) map[string]interface{} {
	cloned := cloneConfigMap(configMap)
	maskNestedString(cloned, []string{"tracker", "api_key"})
	for _, section := range []string{"agent_runtime", "codex", "claude"} {
		maskNestedString(cloned, []string{section, "api_key"})
		maskNestedString(cloned, []string{section, "auth_token"})
		maskNestedEnv(cloned, []string{section, "env"})
		maskStageOverrideSecrets(cloned, section)
	}
	return cloned
}

func mergeMaskedSecrets(next map[string]interface{}, current map[string]interface{}) map[string]interface{} {
	merged := cloneConfigMap(next)
	for _, path := range [][]string{
		{"tracker", "api_key"},
		{"agent_runtime", "api_key"},
		{"agent_runtime", "auth_token"},
		{"codex", "api_key"},
		{"codex", "auth_token"},
		{"claude", "api_key"},
		{"claude", "auth_token"},
	} {
		if nestedString(merged, path) == settingsSecretMask {
			if currentValue := nestedString(current, path); currentValue != "" {
				setNestedString(merged, path, currentValue)
			}
		}
	}
	for _, path := range [][]string{
		{"agent_runtime", "env"},
		{"codex", "env"},
		{"claude", "env"},
	} {
		mergeMaskedEnvValues(merged, current, path)
	}
	for _, section := range []string{"agent_runtime", "codex", "claude"} {
		mergeMaskedStageOverrideSecrets(merged, current, section)
	}
	return merged
}

func maskStageOverrideSecrets(configMap map[string]interface{}, section string) {
	overrides := nestedMap(configMap, []string{section, "stage_overrides"})
	if overrides == nil {
		return
	}
	for _, rawStage := range overrides {
		stageMap, ok := rawStage.(map[string]interface{})
		if !ok {
			continue
		}
		maskNestedString(stageMap, []string{"api_key"})
		maskNestedString(stageMap, []string{"auth_token"})
		maskNestedEnv(stageMap, []string{"env"})
	}
}

func mergeMaskedStageOverrideSecrets(next map[string]interface{}, current map[string]interface{}, section string) {
	nextOverrides := nestedMap(next, []string{section, "stage_overrides"})
	currentOverrides := nestedMap(current, []string{section, "stage_overrides"})
	if nextOverrides == nil || currentOverrides == nil {
		return
	}
	for stageName, rawNext := range nextOverrides {
		nextStage, ok := rawNext.(map[string]interface{})
		if !ok {
			continue
		}
		currentStage, ok := currentOverrides[stageName].(map[string]interface{})
		if !ok {
			continue
		}
		for _, key := range []string{"api_key", "auth_token"} {
			if nestedString(nextStage, []string{key}) == settingsSecretMask {
				if currentValue := nestedString(currentStage, []string{key}); currentValue != "" {
					setNestedString(nextStage, []string{key}, currentValue)
				}
			}
		}
		mergeMaskedEnvValues(nextStage, currentStage, []string{"env"})
	}
}

func mergeMaskedEnvValues(next map[string]interface{}, current map[string]interface{}, path []string) {
	nextEnv := nestedMap(next, path)
	currentEnv := nestedMap(current, path)
	if nextEnv == nil || currentEnv == nil {
		return
	}
	for key, nextValue := range nextEnv {
		if nextValue != settingsSecretMask {
			continue
		}
		if currentValue, ok := currentEnv[key].(string); ok && currentValue != "" {
			nextEnv[key] = currentValue
		}
	}
}

func maskNestedEnv(configMap map[string]interface{}, path []string) {
	if len(path) == 0 || configMap == nil {
		return
	}
	current := configMap
	for _, key := range path[:len(path)-1] {
		next, ok := current[key].(map[string]interface{})
		if !ok {
			return
		}
		current = next
	}
	envMap, ok := current[path[len(path)-1]].(map[string]interface{})
	if !ok {
		return
	}
	for key, rawValue := range envMap {
		value, ok := rawValue.(string)
		if !ok {
			continue
		}
		if isSecretEnvName(key) && strings.TrimSpace(value) != "" && !strings.HasPrefix(strings.TrimSpace(value), "$") {
			envMap[key] = settingsSecretMask
		}
	}
}

func maskEnvMap(env map[string]string) map[string]string {
	masked := make(map[string]string, len(env))
	for key, value := range env {
		if isSecretEnvName(key) && strings.TrimSpace(value) != "" {
			masked[key] = settingsSecretMask
			continue
		}
		masked[key] = value
	}
	return masked
}

func isSecretEnvName(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	for _, marker := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func cloneConfigMap(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return map[string]interface{}{}
	}
	dst := make(map[string]interface{}, len(src))
	for key, value := range src {
		dst[key] = cloneConfigValue(value)
	}
	return dst
}

func cloneConfigValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return cloneConfigMap(v)
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = cloneConfigValue(item)
		}
		return out
	default:
		return v
	}
}

func maskNestedString(configMap map[string]interface{}, path []string) {
	value := strings.TrimSpace(nestedString(configMap, path))
	if value != "" && !strings.HasPrefix(value, "$") {
		setNestedString(configMap, path, settingsSecretMask)
	}
}

func nestedString(configMap map[string]interface{}, path []string) string {
	if len(path) == 0 || configMap == nil {
		return ""
	}
	current := configMap
	for _, key := range path[:len(path)-1] {
		next, ok := current[key].(map[string]interface{})
		if !ok {
			return ""
		}
		current = next
	}
	value, _ := current[path[len(path)-1]].(string)
	return value
}

func nestedMap(configMap map[string]interface{}, path []string) map[string]interface{} {
	if len(path) == 0 || configMap == nil {
		return nil
	}
	current := configMap
	for _, key := range path {
		next, ok := current[key].(map[string]interface{})
		if !ok {
			return nil
		}
		current = next
	}
	return current
}

func setNestedString(configMap map[string]interface{}, path []string, value string) {
	if len(path) == 0 {
		return
	}
	current := configMap
	for _, key := range path[:len(path)-1] {
		next, ok := current[key].(map[string]interface{})
		if !ok {
			next = map[string]interface{}{}
			current[key] = next
		}
		current = next
	}
	current[path[len(path)-1]] = value
}

func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
		Error: api.APIError{Code: "not_found", Message: "API endpoint not found"},
	})
}

func (s *Server) handleAPIv1(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only GET is allowed"},
		})
		return
	}

	// Path is /api/v1/{issue_identifier}
	prefix := "/api/v1/"
	identifier := strings.TrimPrefix(r.URL.Path, prefix)
	if identifier == "" || identifier == r.URL.Path {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
			Error: api.APIError{Code: "not_found", Message: "Issue identifier required"},
		})
		return
	}
	decodedIdentifier, err := url.PathUnescape(identifier)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{
			Error: api.APIError{Code: "bad_request", Message: "Issue identifier is not valid URL encoding"},
		})
		return
	}

	detail, ok := s.orch.IssueDetail(decodedIdentifier)
	if !ok {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
			Error: api.APIError{Code: "not_found", Message: fmt.Sprintf("Issue %s not found", decodedIdentifier)},
		})
		return
	}

	s.writeJSON(w, http.StatusOK, detail)
}
