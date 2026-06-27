package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	pathpkg "path"
	"strings"
	"time"

	"github.com/kbsartain/simphony/internal/project"
	"github.com/kbsartain/simphony/pkg/api"
)

// ProjectRuntimeManager is the manager surface used by the aggregate project API.
type ProjectRuntimeManager interface {
	Summaries() []project.RuntimeSummary
	Summary(id string) (project.RuntimeSummary, bool)
	Runtime(id string) (project.ObservableRuntime, bool)
	Concurrency() api.SupervisorConcurrency
}

// ProjectServer serves project-scoped API routes for multi-project mode.
type ProjectServer struct {
	manager    ProjectRuntimeManager
	bind       string
	port       int
	apiPrefix  string
	mux        *http.ServeMux
	httpServer *http.Server
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
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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
