package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kbsartain/simphony/internal/config"
	"github.com/kbsartain/simphony/internal/project"
	"github.com/kbsartain/simphony/pkg/api"
	"gopkg.in/yaml.v3"
)

// ProjectRuntimeManager is the manager surface used by the aggregate project API.
type ProjectRuntimeManager interface {
	Summaries() []project.RuntimeSummary
	Summary(id string) (project.RuntimeSummary, bool)
	Runtime(id string) (project.ObservableRuntime, bool)
	Concurrency() api.SupervisorConcurrency
	Registry() *config.ProjectRegistry
}

// ProjectServer serves project-scoped API routes for multi-project mode.
type ProjectServer struct {
	manager    ProjectRuntimeManager
	bind       string
	port       int
	apiPrefix  string
	mux        *http.ServeMux
	httpServer *http.Server
	registryMu sync.Mutex
}

// NewProjectServer creates an aggregate API server for multi-project mode.
func NewProjectServer(manager ProjectRuntimeManager, bind string, port int, apiPrefix string) *ProjectServer {
	if strings.TrimSpace(bind) == "" {
		bind = "127.0.0.1"
	}
	if strings.TrimSpace(apiPrefix) == "" {
		apiPrefix = "/api/v1"
	}
	apiPrefix = "/" + strings.Trim(strings.TrimSpace(apiPrefix), "/")
	if apiPrefix == "/" {
		apiPrefix = "/api/v1"
	}

	s := &ProjectServer{
		manager:   manager,
		bind:      bind,
		port:      port,
		apiPrefix: apiPrefix,
		mux:       http.NewServeMux(),
	}
	s.registerRoutes()
	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", bind, port),
		Handler: s.mux,
	}
	return s
}

func (s *ProjectServer) registerRoutes() {
	projectsPath := s.apiPrefix + "/projects"
	s.mux.HandleFunc(s.apiPrefix+"/runtime-mode", s.withCORS(s.handleRuntimeMode))
	s.mux.HandleFunc(s.apiPrefix+"/registry", s.withCORS(s.handleRegistry))
	s.mux.HandleFunc(s.apiPrefix+"/registry/projects", s.withCORS(s.handleRegistryProjects))
	s.mux.HandleFunc(s.apiPrefix+"/registry/projects/", s.withCORS(s.handleRegistryProjectRoute))
	s.mux.HandleFunc(s.apiPrefix+"/state", s.withCORS(s.handleDefaultProjectState))
	s.mux.HandleFunc(s.apiPrefix+"/refresh", s.withCORS(s.handleDefaultProjectRefresh))
	s.mux.HandleFunc(s.apiPrefix+"/settings/validate-tracker", s.withCORS(s.handleDefaultProjectValidateTrackerSettings))
	s.mux.HandleFunc(s.apiPrefix+"/settings", s.withCORS(s.handleDefaultProjectSettings))
	s.mux.HandleFunc(projectsPath, s.withCORS(s.handleProjects))
	s.mux.HandleFunc(projectsPath+"/", s.withCORS(s.handleProjectRoute))
	s.mux.HandleFunc(s.apiPrefix+"/", s.withCORS(s.handleProjectAPINotFound))
	s.mux.HandleFunc("/", s.withCORS(func(w http.ResponseWriter, r *http.Request) {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
			Error: api.APIError{Code: "not_found", Message: "Multi-project dashboard is not built yet"},
		})
	}))
}

// Start begins listening until ctx is cancelled.
func (s *ProjectServer) Start(ctx context.Context) error {
	log.Printf("project_server listening on http://%s:%d", s.bind, s.port)

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

func (s *ProjectServer) withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

func (s *ProjectServer) setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func (s *ProjectServer) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("project_server json encode error: %v", err)
	}
}

func (s *ProjectServer) handleRegistry(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.apiPrefix+"/registry" {
		s.handleProjectAPINotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only GET is allowed"},
		})
		return
	}

	registry := s.manager.Registry()
	if registry == nil {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
			Error: api.APIError{Code: "registry_not_available", Message: "No project registry is active"},
		})
		return
	}
	response, err := registryResponse(registry)
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, api.APIErrorResponse{
			Error: api.APIError{Code: "registry_validation_error", Message: err.Error()},
		})
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

func (s *ProjectServer) handleRuntimeMode(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.apiPrefix+"/runtime-mode" {
		s.handleProjectAPINotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only GET is allowed"},
		})
		return
	}

	response := api.RuntimeModeResponse{
		Mode:                  api.RuntimeModeProjectRegistry,
		ChangeRequiresRestart: true,
	}
	if registry := s.manager.Registry(); registry != nil {
		response.RegistryPath = registry.SourcePath
	}
	s.writeJSON(w, http.StatusOK, response)
}

func (s *ProjectServer) handleRegistryProjects(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.apiPrefix+"/registry/projects" {
		s.handleProjectAPINotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only POST is allowed"},
		})
		return
	}

	var req api.RegistryProjectCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{
			Error: api.APIError{Code: "bad_request", Message: "Request body must be valid JSON"},
		})
		return
	}

	response, status, errCode, err := s.createRegistryProject(req)
	if err != nil {
		s.writeJSON(w, status, api.APIErrorResponse{
			Error: api.APIError{Code: errCode, Message: err.Error()},
		})
		return
	}
	s.writeJSON(w, http.StatusCreated, response)
}

func (s *ProjectServer) handleRegistryProjectRoute(w http.ResponseWriter, r *http.Request) {
	prefix := s.apiPrefix + "/registry/projects/"
	trimmed := strings.TrimPrefix(r.URL.Path, prefix)
	projectID, err := url.PathUnescape(strings.Trim(trimmed, "/"))
	if err != nil || strings.TrimSpace(projectID) == "" || strings.Contains(projectID, "/") {
		s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{
			Error: api.APIError{Code: "bad_project_id", Message: "Project ID is invalid"},
		})
		return
	}
	if r.Method == http.MethodDelete {
		response, status, errCode, err := s.deleteRegistryProject(projectID)
		if err != nil {
			s.writeJSON(w, status, api.APIErrorResponse{
				Error: api.APIError{Code: errCode, Message: err.Error()},
			})
			return
		}
		s.writeJSON(w, http.StatusOK, response)
		return
	}
	if r.Method != http.MethodPut {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only PUT or DELETE is allowed"},
		})
		return
	}

	var req api.RegistryProjectUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{
			Error: api.APIError{Code: "bad_request", Message: "Request body must be valid JSON"},
		})
		return
	}

	response, status, errCode, err := s.updateRegistryProject(projectID, req)
	if err != nil {
		s.writeJSON(w, status, api.APIErrorResponse{
			Error: api.APIError{Code: errCode, Message: err.Error()},
		})
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

func (s *ProjectServer) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.apiPrefix+"/projects" {
		s.handleProjectAPINotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only GET is allowed"},
		})
		return
	}

	summaries := s.manager.Summaries()
	projects := make([]api.ProjectSummary, 0, len(summaries))
	for _, summary := range summaries {
		projectSummary := api.ProjectSummary{
			ID:                  summary.ID,
			Name:                summary.Name,
			WorkflowPath:        summary.WorkflowPath,
			Enabled:             summary.Enabled,
			Running:             summary.Running,
			LastError:           summary.LastError,
			MaxConcurrentAgents: summary.MaxConcurrentAgents,
		}
		if runtime, ok := s.manager.Runtime(summary.ID); ok {
			if snapshot, ok := runtime.Snapshot(); ok {
				projectSummary.Counts = snapshot.Counts
				if snapshot.LastDispatchDeferredReason == "no_supervisor_slots" {
					projectSummary.WaitingOnSupervisor = true
					projectSummary.LastSupervisorDeferredAt = snapshot.LastDispatchDeferredAt
				}
			}
		}
		projects = append(projects, projectSummary)
	}

	s.writeJSON(w, http.StatusOK, api.ProjectsResponse{
		GeneratedAt: time.Now(),
		Projects:    projects,
		Concurrency: s.manager.Concurrency(),
	})
}

func (s *ProjectServer) handleProjectRoute(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, s.apiPrefix+"/projects/")
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		s.handleProjectAPINotFound(w, r)
		return
	}

	parts := strings.Split(trimmed, "/")
	projectID, err := url.PathUnescape(parts[0])
	if err != nil || strings.TrimSpace(projectID) == "" {
		s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{
			Error: api.APIError{Code: "bad_project_id", Message: "Project ID is invalid"},
		})
		return
	}
	runtime, ok := s.manager.Runtime(projectID)
	if !ok {
		s.writeProjectUnavailable(w, projectID)
		return
	}

	if len(parts) == 2 && parts[1] == "state" {
		s.handleProjectState(w, r, runtime)
		return
	}
	if len(parts) == 2 && parts[1] == "refresh" {
		s.handleProjectRefresh(w, r, runtime)
		return
	}
	if len(parts) == 2 && parts[1] == "settings" {
		s.handleProjectSettings(w, r, runtime)
		return
	}
	if len(parts) == 3 && parts[1] == "settings" && parts[2] == "validate-tracker" {
		s.handleProjectValidateTrackerSettings(w, r, runtime)
		return
	}
	if len(parts) == 3 && parts[1] == "issues" {
		identifier, err := url.PathUnescape(parts[2])
		if err != nil || strings.TrimSpace(identifier) == "" {
			s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{
				Error: api.APIError{Code: "bad_issue_identifier", Message: "Issue identifier is invalid"},
			})
			return
		}
		s.handleProjectIssue(w, r, runtime, identifier)
		return
	}

	s.handleProjectAPINotFound(w, r)
}

func (s *ProjectServer) writeProjectUnavailable(w http.ResponseWriter, projectID string) {
	summary, ok := s.manager.Summary(projectID)
	if !ok {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
			Error: api.APIError{Code: "project_not_found", Message: "Project is not configured"},
		})
		return
	}
	if !summary.Enabled {
		s.writeJSON(w, http.StatusConflict, api.APIErrorResponse{
			Error: api.APIError{Code: "project_disabled", Message: "Project is configured but disabled"},
		})
		return
	}
	message := "Project is configured but not running"
	if summary.LastError != "" {
		message = "Project failed to start: " + summary.LastError
	}
	s.writeJSON(w, http.StatusServiceUnavailable, api.APIErrorResponse{
		Error: api.APIError{Code: "project_not_running", Message: message},
	})
}

func (s *ProjectServer) handleProjectState(w http.ResponseWriter, r *http.Request, runtime project.ObservableRuntime) {
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only GET is allowed"},
		})
		return
	}
	snapshot, ok := runtime.Snapshot()
	if !ok {
		s.writeJSON(w, http.StatusServiceUnavailable, api.APIErrorResponse{
			Error: api.APIError{Code: "project_not_running", Message: "Project runtime is not running"},
		})
		return
	}
	s.writeJSON(w, http.StatusOK, snapshot)
}

func (s *ProjectServer) handleProjectRefresh(w http.ResponseWriter, r *http.Request, runtime project.ObservableRuntime) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only POST is allowed"},
		})
		return
	}
	response, ok := runtime.Refresh()
	if !ok {
		s.writeJSON(w, http.StatusServiceUnavailable, api.APIErrorResponse{
			Error: api.APIError{Code: "project_not_running", Message: "Project runtime is not running"},
		})
		return
	}
	s.writeJSON(w, http.StatusAccepted, response)
}

func (s *ProjectServer) handleProjectIssue(w http.ResponseWriter, r *http.Request, runtime project.ObservableRuntime, identifier string) {
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only GET is allowed"},
		})
		return
	}
	detail, ok := runtime.IssueDetail(identifier)
	if !ok {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
			Error: api.APIError{Code: "issue_not_found", Message: "Issue not found in runtime state"},
		})
		return
	}
	s.writeJSON(w, http.StatusOK, detail)
}

func (s *ProjectServer) handleDefaultProjectState(w http.ResponseWriter, r *http.Request) {
	runtime, ok := s.singleRunningRuntime()
	if !ok {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
			Error: api.APIError{Code: "project_required", Message: "Use /api/v1/projects/{project_id}/state when zero or multiple projects are running"},
		})
		return
	}
	s.handleProjectState(w, r, runtime)
}

func (s *ProjectServer) handleDefaultProjectRefresh(w http.ResponseWriter, r *http.Request) {
	runtime, ok := s.singleRunningRuntime()
	if !ok {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
			Error: api.APIError{Code: "project_required", Message: "Use /api/v1/projects/{project_id}/refresh when zero or multiple projects are running"},
		})
		return
	}
	s.handleProjectRefresh(w, r, runtime)
}

func (s *ProjectServer) handleDefaultProjectSettings(w http.ResponseWriter, r *http.Request) {
	runtime, ok := s.singleRunningRuntime()
	if !ok {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
			Error: api.APIError{Code: "project_required", Message: "Use /api/v1/projects/{project_id}/settings when zero or multiple projects are running"},
		})
		return
	}
	s.handleProjectSettings(w, r, runtime)
}

func (s *ProjectServer) handleDefaultProjectValidateTrackerSettings(w http.ResponseWriter, r *http.Request) {
	runtime, ok := s.singleRunningRuntime()
	if !ok {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
			Error: api.APIError{Code: "project_required", Message: "Use /api/v1/projects/{project_id}/settings/validate-tracker when zero or multiple projects are running"},
		})
		return
	}
	s.handleProjectValidateTrackerSettings(w, r, runtime)
}

func (s *ProjectServer) handleProjectSettings(w http.ResponseWriter, r *http.Request, runtime project.ObservableRuntime) {
	switch r.Method {
	case http.MethodGet:
		settings, err := runtime.WorkflowSettings()
		if err != nil {
			status, code := projectSettingsErrorStatus(err)
			s.writeJSON(w, status, api.APIErrorResponse{
				Error: api.APIError{Code: code, Message: err.Error()},
			})
			return
		}
		s.writeJSON(w, http.StatusOK, settingsResponseFromProjectSettings(settings))
	case http.MethodPut:
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
		current, err := runtime.WorkflowSettings()
		if err != nil {
			status, code := projectSettingsErrorStatus(err)
			s.writeJSON(w, status, api.APIErrorResponse{
				Error: api.APIError{Code: code, Message: err.Error()},
			})
			return
		}
		req.Config = mergeMaskedSecrets(req.Config, current.Definition.Config)
		settings, err := runtime.UpdateWorkflowSettings(req)
		if err != nil {
			status, code := projectSettingsErrorStatus(err)
			s.writeJSON(w, status, api.APIErrorResponse{
				Error: api.APIError{Code: code, Message: err.Error()},
			})
			return
		}
		s.writeJSON(w, http.StatusOK, settingsResponseFromProjectSettings(settings))
	default:
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only GET or PUT is allowed"},
		})
	}
}

func (s *ProjectServer) handleProjectValidateTrackerSettings(w http.ResponseWriter, r *http.Request, runtime project.ObservableRuntime) {
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
	current, err := runtime.WorkflowSettings()
	if err != nil {
		status, code := projectSettingsErrorStatus(err)
		s.writeJSON(w, status, api.APIErrorResponse{
			Error: api.APIError{Code: code, Message: err.Error()},
		})
		return
	}
	req.Config = mergeMaskedSecrets(req.Config, current.Definition.Config)
	response, err := runtime.ValidateTrackerSettings(req)
	if err != nil {
		status, code := projectSettingsErrorStatus(err)
		s.writeJSON(w, status, api.APIErrorResponse{
			Error: api.APIError{Code: code, Message: err.Error()},
		})
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

func settingsResponseFromProjectSettings(settings project.WorkflowSettings) api.SettingsResponse {
	var validationError *string
	if settings.ValidationError != nil {
		msg := settings.ValidationError.Error()
		validationError = &msg
	}
	def := settings.Definition
	if def == nil {
		def = &api.WorkflowDefinition{Config: map[string]interface{}{}}
	}
	return api.SettingsResponse{
		WorkflowPath:    settings.WorkflowPath,
		Config:          sanitizeSettingsConfigForResponse(def.Config),
		ResolvedConfig:  settingsConfigForResponse(settings.ResolvedConfig),
		PromptTemplate:  def.PromptTemplate,
		ValidationError: validationError,
	}
}

func projectSettingsErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, project.ErrSettingsValidation):
		return http.StatusBadRequest, "settings_validation_error"
	case errors.Is(err, project.ErrSettingsSave):
		return http.StatusInternalServerError, "settings_save_error"
	case errors.Is(err, project.ErrSettingsApply):
		return http.StatusInternalServerError, "settings_apply_error"
	case errors.Is(err, project.ErrTrackerValidation):
		return http.StatusBadGateway, "linear_validation_error"
	default:
		return http.StatusInternalServerError, "settings_load_error"
	}
}

func (s *ProjectServer) handleProjectAPINotFound(w http.ResponseWriter, r *http.Request) {
	if runtime, ok := s.singleRunningRuntime(); ok {
		prefix := s.apiPrefix + "/"
		identifier := strings.TrimPrefix(r.URL.Path, prefix)
		if identifier != "" && identifier != r.URL.Path && !strings.Contains(identifier, "/") {
			decodedIdentifier, err := url.PathUnescape(identifier)
			if err != nil {
				s.writeJSON(w, http.StatusBadRequest, api.APIErrorResponse{
					Error: api.APIError{Code: "bad_request", Message: "Issue identifier is not valid URL encoding"},
				})
				return
			}
			s.handleProjectIssue(w, r, runtime, decodedIdentifier)
			return
		}
	}
	if strings.HasPrefix(pathpkg.Clean(r.URL.Path), s.apiPrefix) {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
			Error: api.APIError{Code: "not_found", Message: "API endpoint not found"},
		})
		return
	}
	s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
		Error: api.APIError{Code: "not_found", Message: "Not found"},
	})
}

func (s *ProjectServer) singleRunningRuntime() (project.ObservableRuntime, bool) {
	summaries := s.manager.Summaries()
	var runtime project.ObservableRuntime
	count := 0
	for _, summary := range summaries {
		if !summary.Running {
			continue
		}
		candidate, ok := s.manager.Runtime(summary.ID)
		if !ok {
			continue
		}
		runtime = candidate
		count++
		if count > 1 {
			return nil, false
		}
	}
	if count != 1 {
		return nil, false
	}
	return runtime, true
}

func registryResponse(registry *config.ProjectRegistry) (api.RegistryResponse, error) {
	report, err := config.ValidateProjectIsolation(registry)
	if err != nil {
		return api.RegistryResponse{}, err
	}

	response := api.RegistryResponse{
		GeneratedAt: time.Now(),
		SourcePath:  registry.SourcePath,
		Concurrency: api.RegistryConcurrencySummary{
			MaxConcurrentAgents:               registry.Concurrency.MaxConcurrentAgents,
			DefaultProjectMaxConcurrentAgents: registry.Concurrency.DefaultProjectMaxConcurrentAgents,
		},
		Security: api.RegistrySecuritySummary{
			AllowWorkspaceOverlap:          registry.Security.AllowWorkspaceOverlap,
			AllowWorkspaceUnderRegistryDir: registry.Security.AllowWorkspaceUnderRegistryDir,
			AllowRemoteDashboard:           registry.Security.AllowRemoteDashboard,
		},
		AgentRuntime: registryAgentRuntimeSummary(registry.AgentRuntime),
		Projects:     make([]api.RegistryProjectSummary, 0, len(registry.Projects)),
		Warnings:     make([]api.RegistryWarningSummary, 0, len(report.Warnings)),
	}
	if registry.Server != nil {
		response.Server = &api.RegistryServerSummary{
			BindAddress:      registry.Server.BindAddress,
			Port:             registry.Server.Port,
			DashboardEnabled: registry.Server.DashboardEnabled,
			APIPrefix:        registry.Server.APIPrefix,
		}
	}
	for _, item := range registry.Projects {
		response.Projects = append(response.Projects, api.RegistryProjectSummary{
			ID:                  item.ID,
			Name:                item.Name,
			WorkflowPath:        item.WorkflowPath,
			Enabled:             item.Enabled,
			MaxConcurrentAgents: item.MaxConcurrentAgents,
		})
	}
	for _, warning := range report.Warnings {
		response.Warnings = append(response.Warnings, api.RegistryWarningSummary{
			Code:       warning.Code,
			Message:    warning.Message,
			ProjectIDs: append([]string(nil), warning.ProjectIDs...),
		})
	}
	return response, nil
}

var registryProjectIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func (s *ProjectServer) createRegistryProject(req api.RegistryProjectCreateRequest) (api.RegistryProjectCreateResponse, int, string, error) {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()

	registry := s.manager.Registry()
	if registry == nil {
		return api.RegistryProjectCreateResponse{}, http.StatusNotFound, "registry_not_available", fmt.Errorf("no project registry is active")
	}
	if strings.TrimSpace(registry.SourcePath) == "" {
		return api.RegistryProjectCreateResponse{}, http.StatusInternalServerError, "registry_source_missing", fmt.Errorf("active project registry has no source path")
	}

	projectID := strings.TrimSpace(req.ID)
	if projectID == "" {
		return api.RegistryProjectCreateResponse{}, http.StatusBadRequest, "project_id_required", fmt.Errorf("project id is required")
	}
	if !registryProjectIDPattern.MatchString(projectID) {
		return api.RegistryProjectCreateResponse{}, http.StatusBadRequest, "project_id_invalid", fmt.Errorf("project id must start with a letter or number and contain only letters, numbers, dots, underscores, or hyphens")
	}
	for _, existing := range registry.Projects {
		if strings.EqualFold(existing.ID, projectID) {
			return api.RegistryProjectCreateResponse{}, http.StatusConflict, "project_id_exists", fmt.Errorf("project %q already exists", projectID)
		}
	}

	workflowPathForYAML := strings.TrimSpace(req.WorkflowPath)
	if workflowPathForYAML == "" {
		return api.RegistryProjectCreateResponse{}, http.StatusBadRequest, "workflow_path_required", fmt.Errorf("workflow path is required")
	}
	registryDir := filepath.Dir(registry.SourcePath)
	resolvedWorkflowPath := workflowPathForYAML
	if !filepath.IsAbs(resolvedWorkflowPath) {
		resolvedWorkflowPath = filepath.Join(registryDir, resolvedWorkflowPath)
	}
	resolvedWorkflowPath, err := filepath.Abs(resolvedWorkflowPath)
	if err != nil {
		return api.RegistryProjectCreateResponse{}, http.StatusBadRequest, "workflow_path_invalid", fmt.Errorf("resolve workflow path: %w", err)
	}
	info, err := os.Stat(resolvedWorkflowPath)
	if err != nil {
		return api.RegistryProjectCreateResponse{}, http.StatusBadRequest, "workflow_path_not_found", fmt.Errorf("workflow path %q: %w", resolvedWorkflowPath, err)
	}
	if info.IsDir() {
		return api.RegistryProjectCreateResponse{}, http.StatusBadRequest, "workflow_path_is_directory", fmt.Errorf("workflow path %q is a directory", resolvedWorkflowPath)
	}
	if _, err := config.LoadWorkflow(resolvedWorkflowPath); err != nil {
		return api.RegistryProjectCreateResponse{}, http.StatusBadRequest, "workflow_load_error", err
	}

	if req.MaxConcurrentAgents < 0 {
		return api.RegistryProjectCreateResponse{}, http.StatusBadRequest, "max_concurrent_agents_invalid", fmt.Errorf("max_concurrent_agents must be positive")
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = projectID
	}

	nextRegistry := cloneRegistryForAppend(registry)
	nextProject := config.RegistryProject{
		ID:                  projectID,
		Name:                name,
		WorkflowPath:        resolvedWorkflowPath,
		Enabled:             enabled,
		MaxConcurrentAgents: req.MaxConcurrentAgents,
	}
	nextRegistry.Projects = append(nextRegistry.Projects, nextProject)
	if _, err := config.ValidateProjectIsolation(nextRegistry); err != nil {
		return api.RegistryProjectCreateResponse{}, http.StatusBadRequest, "registry_validation_error", err
	}

	if err := appendRegistryProjectToFile(registry.SourcePath, api.RegistryProjectCreateRequest{
		ID:                  projectID,
		Name:                name,
		WorkflowPath:        filepath.ToSlash(workflowPathForYAML),
		Enabled:             &enabled,
		MaxConcurrentAgents: req.MaxConcurrentAgents,
	}); err != nil {
		return api.RegistryProjectCreateResponse{}, http.StatusInternalServerError, "registry_write_error", err
	}

	registry.Projects = append(registry.Projects, nextProject)
	registrySummary, err := registryResponse(registry)
	if err != nil {
		return api.RegistryProjectCreateResponse{}, http.StatusInternalServerError, "registry_validation_error", err
	}
	projectSummary := api.RegistryProjectSummary{
		ID:                  nextProject.ID,
		Name:                nextProject.Name,
		WorkflowPath:        nextProject.WorkflowPath,
		Enabled:             nextProject.Enabled,
		MaxConcurrentAgents: nextProject.MaxConcurrentAgents,
	}
	return api.RegistryProjectCreateResponse{
		Registry:              registrySummary,
		Project:               projectSummary,
		Command:               fmt.Sprintf("simphony -config %s", registry.SourcePath),
		ChangeRequiresRestart: true,
	}, 0, "", nil
}

func (s *ProjectServer) updateRegistryProject(projectID string, req api.RegistryProjectUpdateRequest) (api.RegistryProjectUpdateResponse, int, string, error) {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()

	registry := s.manager.Registry()
	if registry == nil {
		return api.RegistryProjectUpdateResponse{}, http.StatusNotFound, "registry_not_available", fmt.Errorf("no project registry is active")
	}
	if strings.TrimSpace(registry.SourcePath) == "" {
		return api.RegistryProjectUpdateResponse{}, http.StatusInternalServerError, "registry_source_missing", fmt.Errorf("active project registry has no source path")
	}

	projectID = strings.TrimSpace(projectID)
	projectIndex := -1
	for i, existing := range registry.Projects {
		if strings.EqualFold(existing.ID, projectID) {
			projectIndex = i
			break
		}
	}
	if projectIndex < 0 {
		return api.RegistryProjectUpdateResponse{}, http.StatusNotFound, "project_not_found", fmt.Errorf("project %q was not found", projectID)
	}

	workflowPathForYAML := strings.TrimSpace(req.WorkflowPath)
	if workflowPathForYAML == "" {
		return api.RegistryProjectUpdateResponse{}, http.StatusBadRequest, "workflow_path_required", fmt.Errorf("workflow path is required")
	}
	registryDir := filepath.Dir(registry.SourcePath)
	resolvedWorkflowPath := workflowPathForYAML
	if !filepath.IsAbs(resolvedWorkflowPath) {
		resolvedWorkflowPath = filepath.Join(registryDir, resolvedWorkflowPath)
	}
	resolvedWorkflowPath, err := filepath.Abs(resolvedWorkflowPath)
	if err != nil {
		return api.RegistryProjectUpdateResponse{}, http.StatusBadRequest, "workflow_path_invalid", fmt.Errorf("resolve workflow path: %w", err)
	}
	info, err := os.Stat(resolvedWorkflowPath)
	if err != nil {
		return api.RegistryProjectUpdateResponse{}, http.StatusBadRequest, "workflow_path_not_found", fmt.Errorf("workflow path %q: %w", resolvedWorkflowPath, err)
	}
	if info.IsDir() {
		return api.RegistryProjectUpdateResponse{}, http.StatusBadRequest, "workflow_path_is_directory", fmt.Errorf("workflow path %q is a directory", resolvedWorkflowPath)
	}
	if _, err := config.LoadWorkflow(resolvedWorkflowPath); err != nil {
		return api.RegistryProjectUpdateResponse{}, http.StatusBadRequest, "workflow_load_error", err
	}

	maxConcurrentAgents := registry.Projects[projectIndex].MaxConcurrentAgents
	if req.MaxConcurrentAgents != nil {
		if *req.MaxConcurrentAgents < 0 {
			return api.RegistryProjectUpdateResponse{}, http.StatusBadRequest, "max_concurrent_agents_invalid", fmt.Errorf("max_concurrent_agents must be positive")
		}
		maxConcurrentAgents = *req.MaxConcurrentAgents
	}
	enabled := registry.Projects[projectIndex].Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = registry.Projects[projectIndex].ID
	}

	nextRegistry := cloneRegistryForAppend(registry)
	nextProject := nextRegistry.Projects[projectIndex]
	nextProject.Name = name
	nextProject.WorkflowPath = resolvedWorkflowPath
	nextProject.Enabled = enabled
	nextProject.MaxConcurrentAgents = maxConcurrentAgents
	nextRegistry.Projects[projectIndex] = nextProject
	if _, err := config.ValidateProjectIsolation(nextRegistry); err != nil {
		return api.RegistryProjectUpdateResponse{}, http.StatusBadRequest, "registry_validation_error", err
	}

	if err := updateRegistryProjectInFile(registry.SourcePath, projectID, api.RegistryProjectUpdateRequest{
		Name:                name,
		WorkflowPath:        filepath.ToSlash(workflowPathForYAML),
		Enabled:             &enabled,
		MaxConcurrentAgents: &maxConcurrentAgents,
	}); err != nil {
		return api.RegistryProjectUpdateResponse{}, http.StatusInternalServerError, "registry_write_error", err
	}

	registry.Projects[projectIndex] = nextProject
	registrySummary, err := registryResponse(registry)
	if err != nil {
		return api.RegistryProjectUpdateResponse{}, http.StatusInternalServerError, "registry_validation_error", err
	}
	projectSummary := api.RegistryProjectSummary{
		ID:                  nextProject.ID,
		Name:                nextProject.Name,
		WorkflowPath:        nextProject.WorkflowPath,
		Enabled:             nextProject.Enabled,
		MaxConcurrentAgents: nextProject.MaxConcurrentAgents,
	}
	return api.RegistryProjectUpdateResponse{
		Registry:              registrySummary,
		Project:               projectSummary,
		Command:               fmt.Sprintf("simphony -config %s", registry.SourcePath),
		ChangeRequiresRestart: true,
	}, 0, "", nil
}

func (s *ProjectServer) deleteRegistryProject(projectID string) (api.RegistryProjectDeleteResponse, int, string, error) {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()

	registry := s.manager.Registry()
	if registry == nil {
		return api.RegistryProjectDeleteResponse{}, http.StatusNotFound, "registry_not_available", fmt.Errorf("no project registry is active")
	}
	if strings.TrimSpace(registry.SourcePath) == "" {
		return api.RegistryProjectDeleteResponse{}, http.StatusInternalServerError, "registry_source_missing", fmt.Errorf("active project registry has no source path")
	}
	if len(registry.Projects) <= 1 {
		return api.RegistryProjectDeleteResponse{}, http.StatusConflict, "registry_requires_project", fmt.Errorf("project registry must contain at least one project")
	}

	projectID = strings.TrimSpace(projectID)
	projectIndex := -1
	for i, existing := range registry.Projects {
		if strings.EqualFold(existing.ID, projectID) {
			projectIndex = i
			break
		}
	}
	if projectIndex < 0 {
		return api.RegistryProjectDeleteResponse{}, http.StatusNotFound, "project_not_found", fmt.Errorf("project %q was not found", projectID)
	}
	removed := registry.Projects[projectIndex]

	nextRegistry := cloneRegistryForAppend(registry)
	nextRegistry.Projects = append(nextRegistry.Projects[:projectIndex], nextRegistry.Projects[projectIndex+1:]...)
	if _, err := config.ValidateProjectIsolation(nextRegistry); err != nil {
		return api.RegistryProjectDeleteResponse{}, http.StatusBadRequest, "registry_validation_error", err
	}

	if err := deleteRegistryProjectFromFile(registry.SourcePath, projectID); err != nil {
		return api.RegistryProjectDeleteResponse{}, http.StatusInternalServerError, "registry_write_error", err
	}

	registry.Projects = append(registry.Projects[:projectIndex], registry.Projects[projectIndex+1:]...)
	registrySummary, err := registryResponse(registry)
	if err != nil {
		return api.RegistryProjectDeleteResponse{}, http.StatusInternalServerError, "registry_validation_error", err
	}
	return api.RegistryProjectDeleteResponse{
		Registry:              registrySummary,
		ProjectID:             removed.ID,
		ProjectName:           removed.Name,
		Command:               fmt.Sprintf("simphony -config %s", registry.SourcePath),
		ChangeRequiresRestart: true,
	}, 0, "", nil
}

func cloneRegistryForAppend(registry *config.ProjectRegistry) *config.ProjectRegistry {
	next := *registry
	next.Projects = append([]config.RegistryProject(nil), registry.Projects...)
	return &next
}

func appendRegistryProjectToFile(registryPath string, req api.RegistryProjectCreateRequest) error {
	data, err := os.ReadFile(registryPath)
	if err != nil {
		return fmt.Errorf("read registry: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse registry yaml: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("registry root must be a mapping")
	}
	root := doc.Content[0]
	projectsNode := mappingValue(root, "projects")
	if projectsNode == nil {
		projectsNode = &yaml.Node{Kind: yaml.SequenceNode}
		root.Content = append(root.Content, scalarNode("projects"), projectsNode)
	}
	if projectsNode.Kind != yaml.SequenceNode {
		return fmt.Errorf("projects must be a list")
	}
	projectsNode.Content = append(projectsNode.Content, registryProjectNode(req))

	file, err := os.OpenFile(registryPath, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open registry for write: %w", err)
	}
	defer file.Close()
	encoder := yaml.NewEncoder(file)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return fmt.Errorf("write registry yaml: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("finish registry yaml: %w", err)
	}
	return nil
}

func updateRegistryProjectInFile(registryPath string, projectID string, req api.RegistryProjectUpdateRequest) error {
	data, err := os.ReadFile(registryPath)
	if err != nil {
		return fmt.Errorf("read registry: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse registry yaml: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("registry root must be a mapping")
	}
	projectsNode := mappingValue(doc.Content[0], "projects")
	if projectsNode == nil || projectsNode.Kind != yaml.SequenceNode {
		return fmt.Errorf("projects must be a list")
	}

	var projectNode *yaml.Node
	for _, candidate := range projectsNode.Content {
		if candidate.Kind != yaml.MappingNode {
			continue
		}
		idNode := mappingValue(candidate, "id")
		if idNode != nil && strings.EqualFold(idNode.Value, projectID) {
			projectNode = candidate
			break
		}
	}
	if projectNode == nil {
		return fmt.Errorf("project %q was not found", projectID)
	}

	setMappingValue(projectNode, "name", scalarNode(req.Name))
	setMappingValue(projectNode, "workflow_path", scalarNode(req.WorkflowPath))
	if req.Enabled != nil {
		if *req.Enabled {
			removeMappingValue(projectNode, "enabled")
		} else {
			setMappingValue(projectNode, "enabled", boolNode(false))
		}
	}
	if req.MaxConcurrentAgents != nil {
		if *req.MaxConcurrentAgents > 0 {
			setMappingValue(projectNode, "max_concurrent_agents", intNode(*req.MaxConcurrentAgents))
		} else {
			removeMappingValue(projectNode, "max_concurrent_agents")
		}
	}

	file, err := os.OpenFile(registryPath, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open registry for write: %w", err)
	}
	defer file.Close()
	encoder := yaml.NewEncoder(file)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return fmt.Errorf("write registry yaml: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("finish registry yaml: %w", err)
	}
	return nil
}

func deleteRegistryProjectFromFile(registryPath string, projectID string) error {
	data, err := os.ReadFile(registryPath)
	if err != nil {
		return fmt.Errorf("read registry: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse registry yaml: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("registry root must be a mapping")
	}
	projectsNode := mappingValue(doc.Content[0], "projects")
	if projectsNode == nil || projectsNode.Kind != yaml.SequenceNode {
		return fmt.Errorf("projects must be a list")
	}

	removeIndex := -1
	for i, candidate := range projectsNode.Content {
		if candidate.Kind != yaml.MappingNode {
			continue
		}
		idNode := mappingValue(candidate, "id")
		if idNode != nil && strings.EqualFold(idNode.Value, projectID) {
			removeIndex = i
			break
		}
	}
	if removeIndex < 0 {
		return fmt.Errorf("project %q was not found", projectID)
	}
	if len(projectsNode.Content) <= 1 {
		return fmt.Errorf("project registry must contain at least one project")
	}
	projectsNode.Content = append(projectsNode.Content[:removeIndex], projectsNode.Content[removeIndex+1:]...)

	file, err := os.OpenFile(registryPath, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open registry for write: %w", err)
	}
	defer file.Close()
	encoder := yaml.NewEncoder(file)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return fmt.Errorf("write registry yaml: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("finish registry yaml: %w", err)
	}
	return nil
}

func registryProjectNode(req api.RegistryProjectCreateRequest) *yaml.Node {
	content := []*yaml.Node{
		scalarNode("id"), scalarNode(req.ID),
		scalarNode("name"), scalarNode(req.Name),
		scalarNode("workflow_path"), scalarNode(req.WorkflowPath),
	}
	if req.Enabled != nil && !*req.Enabled {
		content = append(content, scalarNode("enabled"), boolNode(false))
	}
	if req.MaxConcurrentAgents > 0 {
		content = append(content, scalarNode("max_concurrent_agents"), intNode(req.MaxConcurrentAgents))
	}
	return &yaml.Node{Kind: yaml.MappingNode, Content: content}
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func setMappingValue(node *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content[i+1] = value
			return
		}
	}
	node.Content = append(node.Content, scalarNode(key), value)
}

func removeMappingValue(node *yaml.Node, key string) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return
		}
	}
}

func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func boolNode(value bool) *yaml.Node {
	if value {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"}
}

func intNode(value int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", value)}
}

func registryAgentRuntimeSummary(runtime *api.AgentRuntimeConfig) api.RegistryAgentRuntimeSummary {
	if runtime == nil {
		return api.RegistryAgentRuntimeSummary{}
	}
	return api.RegistryAgentRuntimeSummary{
		Configured:          true,
		Provider:            runtime.Provider,
		Command:             runtime.Command,
		Model:               runtime.Model,
		ModelProvider:       runtime.ModelProvider,
		ReasoningEffort:     runtime.ReasoningEffort,
		EndpointURL:         runtime.EndpointURL,
		APIKeyConfigured:    runtime.APIKeyConfigured || runtime.APIKey != "",
		AuthTokenConfigured: runtime.AuthTokenConfigured || runtime.AuthToken != "",
		EnvKeys:             sortedStringKeys(runtime.Env),
		StageOverrideKeys:   sortedStringKeys(runtime.StageOverrides),
		PermissionMode:      runtime.PermissionMode,
		AllowedTools:        append([]string(nil), runtime.AllowedTools...),
		DisallowedTools:     append([]string(nil), runtime.DisallowedTools...),
		SettingSources:      append([]string(nil), runtime.SettingSources...),
	}
}

func sortedStringKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
