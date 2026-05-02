package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"simphony/internal/orchestrator"
	"simphony/pkg/api"
)

// Orchestrator is the subset of orchestrator.Orchestrator methods used by the HTTP server.
type Orchestrator interface {
	Snapshot() api.StateSnapshot
	IssueDetail(identifier string) (api.IssueDetailResponse, bool)
	Refresh() api.RefreshResponse
}

// Server wraps the HTTP server and orchestrator reference.
type Server struct {
	orch       Orchestrator
	port       int
	mux        *http.ServeMux
	httpServer *http.Server
}

// New creates a new Server. Call Start to begin listening.
func New(orch *orchestrator.Orchestrator, port int) *Server {
	s := &Server{
		orch: orch,
		port: port,
		mux:  http.NewServeMux(),
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
	s.mux.HandleFunc("/api/v1/", s.withCORS(s.handleAPIv1)) // catch-all for /api/v1/{issue_identifier}

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
			// For SPA routing: if the file doesn't exist, serve index.html.
			path := filepath.Join(distDir, filepath.Clean(r.URL.Path))
			if _, err := os.Stat(path); os.IsNotExist(err) {
				r.URL.Path = "/"
			}
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
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
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
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, api.APIErrorResponse{
			Error: api.APIError{Code: "method_not_allowed", Message: "Only POST or GET is allowed"},
		})
		return
	}
	resp := s.orch.Refresh()
	s.writeJSON(w, http.StatusOK, resp)
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

	detail, ok := s.orch.IssueDetail(identifier)
	if !ok {
		s.writeJSON(w, http.StatusNotFound, api.APIErrorResponse{
			Error: api.APIError{Code: "not_found", Message: fmt.Sprintf("Issue %s not found", identifier)},
		})
		return
	}

	s.writeJSON(w, http.StatusOK, detail)
}
