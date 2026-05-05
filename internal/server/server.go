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
	"strings"
	"sync"
	"time"

	"github.com/kbsartain/simphony/internal/config"
	"github.com/kbsartain/simphony/pkg/api"
)

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
	s.mux.HandleFunc("/api/v1/state", s.withCORS(s.handleState))
	s.mux.HandleFunc("/api/v1/refresh", s.withCORS(s.handleRefresh))
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
		Config:          def.Config,
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
		Config:         req.Config,
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
		Config:         def.Config,
		ResolvedConfig: settingsConfigForResponse(cfg),
		PromptTemplate: def.PromptTemplate,
	})
}

func settingsConfigForResponse(cfg *api.WorkflowConfig) api.WorkflowConfig {
	if cfg == nil {
		return api.WorkflowConfig{}
	}
	safe := *cfg
	if safe.Tracker.APIKey != "" {
		safe.Tracker.APIKey = "********"
	}
	return safe
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
