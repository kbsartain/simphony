package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kbsartain/simphony/internal/config"
	"github.com/kbsartain/simphony/internal/project"
	"github.com/kbsartain/simphony/pkg/api"
)

type fakeProjectRuntime struct {
	project         config.RegistryProject
	snapshot        api.StateSnapshot
	refresh         api.RefreshResponse
	details         map[string]api.IssueDetailResponse
	settings        project.WorkflowSettings
	updatedSettings project.WorkflowSettings
	updateReq       *api.SettingsUpdateRequest
	validation      api.SettingsValidationResponse
}

func (f *fakeProjectRuntime) ID() string {
	return f.project.ID
}

func (f *fakeProjectRuntime) Project() config.RegistryProject {
	return f.project
}

func (f *fakeProjectRuntime) Start(ctx context.Context) error {
	return nil
}

func (f *fakeProjectRuntime) Stop() {}

func (f *fakeProjectRuntime) Snapshot() (api.StateSnapshot, bool) {
	return f.snapshot, true
}

func (f *fakeProjectRuntime) IssueDetail(identifier string) (api.IssueDetailResponse, bool) {
	detail, ok := f.details[identifier]
	return detail, ok
}

func (f *fakeProjectRuntime) Refresh() (api.RefreshResponse, bool) {
	return f.refresh, true
}

func (f *fakeProjectRuntime) WorkflowSettings() (project.WorkflowSettings, error) {
	return f.settings, nil
}

func (f *fakeProjectRuntime) UpdateWorkflowSettings(req api.SettingsUpdateRequest) (project.WorkflowSettings, error) {
	f.updateReq = &req
	if f.updatedSettings.Definition != nil {
		return f.updatedSettings, nil
	}
	return f.settings, nil
}

func (f *fakeProjectRuntime) ValidateTrackerSettings(req api.SettingsUpdateRequest) (api.SettingsValidationResponse, error) {
	f.updateReq = &req
	return f.validation, nil
}

type fakeProjectManager struct {
	summaries   []project.RuntimeSummary
	runtimes    map[string]project.ObservableRuntime
	concurrency api.SupervisorConcurrency
}

func (f *fakeProjectManager) Summaries() []project.RuntimeSummary {
	return f.summaries
}

func (f *fakeProjectManager) Summary(id string) (project.RuntimeSummary, bool) {
	for _, summary := range f.summaries {
		if summary.ID == id {
			return summary, true
		}
	}
	return project.RuntimeSummary{}, false
}

func (f *fakeProjectManager) Runtime(id string) (project.ObservableRuntime, bool) {
	runtime, ok := f.runtimes[id]
	return runtime, ok
}

func (f *fakeProjectManager) Concurrency() api.SupervisorConcurrency {
	return f.concurrency
}

func newTestProjectServer(manager ProjectRuntimeManager) *ProjectServer {
	return NewProjectServer(manager, "127.0.0.1", 8080, "/api/v1")
}

func TestProjectServerListsProjects(t *testing.T) {
	deferredAt := time.Date(2026, 6, 26, 12, 10, 0, 0, time.UTC)
	manager := &fakeProjectManager{
		summaries: []project.RuntimeSummary{
			{ID: "alpha", Name: "Alpha", WorkflowPath: "/tmp/alpha/WORKFLOW.md", Enabled: true, Running: true, MaxConcurrentAgents: 2},
			{ID: "beta", Name: "Beta", WorkflowPath: "/tmp/beta/WORKFLOW.md", Enabled: false, Running: false},
		},
		runtimes: map[string]project.ObservableRuntime{
			"alpha": &fakeProjectRuntime{
				project: config.RegistryProject{ID: "alpha", Name: "Alpha"},
				snapshot: api.StateSnapshot{Counts: api.StateCounts{
					Running: 1,
					Claimed: 2,
				}, LastDispatchDeferredReason: "no_supervisor_slots", LastDispatchDeferredAt: &deferredAt},
			},
		},
		concurrency: api.SupervisorConcurrency{MaxConcurrentAgents: 10, UsedAgents: 10, AvailableAgents: 0},
	}
	s := newTestProjectServer(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body api.ProjectsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Projects) != 2 {
		t.Fatalf("projects len = %d, want 2", len(body.Projects))
	}
	if body.Projects[0].ID != "alpha" || body.Projects[0].Counts.Running != 1 || body.Projects[0].Counts.Claimed != 2 {
		t.Fatalf("alpha summary = %+v, want counts from runtime", body.Projects[0])
	}
	if !body.Projects[0].WaitingOnSupervisor || body.Projects[0].LastSupervisorDeferredAt == nil {
		t.Fatalf("alpha supervisor wait = %+v, want waiting with timestamp", body.Projects[0])
	}
	if body.Projects[0].MaxConcurrentAgents != 2 {
		t.Fatalf("alpha max concurrent = %d, want 2", body.Projects[0].MaxConcurrentAgents)
	}
	if body.Projects[1].ID != "beta" || body.Projects[1].Running {
		t.Fatalf("beta summary = %+v, want stopped beta", body.Projects[1])
	}
	if body.Concurrency.MaxConcurrentAgents != 10 || body.Concurrency.UsedAgents != 10 || body.Concurrency.AvailableAgents != 0 {
		t.Fatalf("concurrency = %+v, want full supervisor pool", body.Concurrency)
	}
}

func TestProjectServerReturnsProjectState(t *testing.T) {
	generatedAt := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	manager := &fakeProjectManager{
		runtimes: map[string]project.ObservableRuntime{
			"alpha": &fakeProjectRuntime{
				project: config.RegistryProject{ID: "alpha", Name: "Alpha"},
				snapshot: api.StateSnapshot{GeneratedAt: generatedAt, Counts: api.StateCounts{
					Running: 3,
				}},
			},
		},
	}
	s := newTestProjectServer(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/alpha/state", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var snapshot api.StateSnapshot
	if err := json.NewDecoder(rec.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.Counts.Running != 3 {
		t.Fatalf("running = %d, want 3", snapshot.Counts.Running)
	}
}

func TestProjectServerDefaultStateAliasUsesSingleRunningProject(t *testing.T) {
	manager := &fakeProjectManager{
		summaries: []project.RuntimeSummary{
			{ID: "alpha", Name: "Alpha", Enabled: true, Running: true},
		},
		runtimes: map[string]project.ObservableRuntime{
			"alpha": &fakeProjectRuntime{
				project: config.RegistryProject{ID: "alpha", Name: "Alpha"},
				snapshot: api.StateSnapshot{Counts: api.StateCounts{
					Running: 4,
				}},
			},
		},
	}
	s := newTestProjectServer(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var snapshot api.StateSnapshot
	if err := json.NewDecoder(rec.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.Counts.Running != 4 {
		t.Fatalf("running = %d, want 4", snapshot.Counts.Running)
	}
}

func TestProjectServerDefaultStateAliasRequiresSingleRunningProject(t *testing.T) {
	manager := &fakeProjectManager{
		summaries: []project.RuntimeSummary{
			{ID: "alpha", Name: "Alpha", Enabled: true, Running: true},
			{ID: "beta", Name: "Beta", Enabled: true, Running: true},
		},
		runtimes: map[string]project.ObservableRuntime{
			"alpha": &fakeProjectRuntime{project: config.RegistryProject{ID: "alpha", Name: "Alpha"}},
			"beta":  &fakeProjectRuntime{project: config.RegistryProject{ID: "beta", Name: "Beta"}},
		},
	}
	s := newTestProjectServer(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	var body api.APIErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "project_required" {
		t.Fatalf("error code = %q, want project_required", body.Error.Code)
	}
}

func TestProjectServerRefreshesProject(t *testing.T) {
	requestedAt := time.Date(2026, 6, 26, 12, 5, 0, 0, time.UTC)
	manager := &fakeProjectManager{
		runtimes: map[string]project.ObservableRuntime{
			"alpha": &fakeProjectRuntime{
				project: config.RegistryProject{ID: "alpha", Name: "Alpha"},
				refresh: api.RefreshResponse{
					Queued:      true,
					RequestedAt: requestedAt,
					Operations:  []string{"tick"},
				},
			},
		},
	}
	s := newTestProjectServer(manager)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/alpha/refresh", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	var response api.RefreshResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode refresh: %v", err)
	}
	if !response.Queued || len(response.Operations) != 1 || response.Operations[0] != "tick" {
		t.Fatalf("refresh = %+v, want queued tick", response)
	}
}

func TestProjectServerReturnsIssueDetail(t *testing.T) {
	manager := &fakeProjectManager{
		runtimes: map[string]project.ObservableRuntime{
			"alpha": &fakeProjectRuntime{
				project: config.RegistryProject{ID: "alpha", Name: "Alpha"},
				details: map[string]api.IssueDetailResponse{
					"SIM-1": {
						IssueIdentifier: "SIM-1",
						IssueID:         "issue-1",
						Status:          "running",
					},
				},
			},
		},
	}
	s := newTestProjectServer(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/alpha/issues/SIM-1", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var detail api.IssueDetailResponse
	if err := json.NewDecoder(rec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode issue detail: %v", err)
	}
	if detail.IssueIdentifier != "SIM-1" {
		t.Fatalf("identifier = %q, want SIM-1", detail.IssueIdentifier)
	}
}

func TestProjectServerRejectsUnknownProject(t *testing.T) {
	s := newTestProjectServer(&fakeProjectManager{runtimes: map[string]project.ObservableRuntime{}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/missing/state", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	var body api.APIErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "project_not_found" {
		t.Fatalf("error code = %q, want project_not_found", body.Error.Code)
	}
}

func TestProjectServerRejectsDisabledProject(t *testing.T) {
	manager := &fakeProjectManager{
		summaries: []project.RuntimeSummary{
			{ID: "beta", Name: "Beta", Enabled: false, Running: false},
		},
		runtimes: map[string]project.ObservableRuntime{},
	}
	s := newTestProjectServer(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/beta/state", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	var body api.APIErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "project_disabled" {
		t.Fatalf("error code = %q, want project_disabled", body.Error.Code)
	}
}

func TestProjectServerRejectsStoppedProject(t *testing.T) {
	manager := &fakeProjectManager{
		summaries: []project.RuntimeSummary{
			{ID: "alpha", Name: "Alpha", Enabled: true, Running: false, LastError: "startup failed"},
		},
		runtimes: map[string]project.ObservableRuntime{},
	}
	s := newTestProjectServer(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/alpha/state", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	var body api.APIErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "project_not_running" {
		t.Fatalf("error code = %q, want project_not_running", body.Error.Code)
	}
}

func TestProjectServerSettingsGetMasksSecrets(t *testing.T) {
	runtime := &fakeProjectRuntime{
		project: config.RegistryProject{ID: "alpha", Name: "Alpha"},
		settings: project.WorkflowSettings{
			WorkflowPath: "/tmp/alpha/WORKFLOW.md",
			Definition: &api.WorkflowDefinition{
				Config: map[string]interface{}{
					"tracker": map[string]interface{}{
						"kind":         "linear",
						"api_key":      "literal-linear-secret",
						"project_slug": "alpha",
					},
					"agent_runtime": map[string]interface{}{
						"provider":   "codex",
						"api_key":    "literal-agent-secret",
						"auth_token": "literal-token",
					},
				},
				PromptTemplate: "Prompt body",
			},
			ResolvedConfig: &api.WorkflowConfig{
				Tracker: api.TrackerConfig{APIKey: "resolved-linear-key"},
				AgentRuntime: api.AgentRuntimeConfig{
					Provider: "codex",
					APIKey:   "resolved-agent-key",
				},
			},
		},
	}
	manager := &fakeProjectManager{
		runtimes: map[string]project.ObservableRuntime{"alpha": runtime},
	}
	s := newTestProjectServer(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/alpha/settings", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response api.SettingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	trackerConfig := response.Config["tracker"].(map[string]interface{})
	if trackerConfig["api_key"] != settingsSecretMask {
		t.Fatalf("tracker api_key = %v, want mask", trackerConfig["api_key"])
	}
	runtimeConfig := response.Config["agent_runtime"].(map[string]interface{})
	if runtimeConfig["api_key"] != settingsSecretMask || runtimeConfig["auth_token"] != settingsSecretMask {
		t.Fatalf("runtime secrets = %v, want masks", runtimeConfig)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("literal-linear-secret")) || bytes.Contains(rec.Body.Bytes(), []byte("literal-agent-secret")) {
		t.Fatal("response leaked literal secret")
	}
}

func TestProjectServerSettingsPutPreservesMaskedSecrets(t *testing.T) {
	runtime := &fakeProjectRuntime{
		project: config.RegistryProject{ID: "alpha", Name: "Alpha"},
		settings: project.WorkflowSettings{
			WorkflowPath: "/tmp/alpha/WORKFLOW.md",
			Definition: &api.WorkflowDefinition{
				Config: map[string]interface{}{
					"tracker": map[string]interface{}{
						"kind":         "linear",
						"api_key":      "literal-linear-secret",
						"project_slug": "old-alpha",
					},
					"agent_runtime": map[string]interface{}{
						"provider": "codex",
						"api_key":  "literal-agent-secret",
					},
				},
				PromptTemplate: "Prompt body",
			},
		},
	}
	manager := &fakeProjectManager{
		runtimes: map[string]project.ObservableRuntime{"alpha": runtime},
	}
	s := newTestProjectServer(manager)
	payload := api.SettingsUpdateRequest{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":         "linear",
				"api_key":      settingsSecretMask,
				"project_slug": "new-alpha",
			},
			"agent_runtime": map[string]interface{}{
				"provider": "codex",
				"api_key":  settingsSecretMask,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/projects/alpha/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if runtime.updateReq == nil {
		t.Fatal("UpdateWorkflowSettings was not called")
	}
	trackerConfig := runtime.updateReq.Config["tracker"].(map[string]interface{})
	if trackerConfig["api_key"] != "literal-linear-secret" || trackerConfig["project_slug"] != "new-alpha" {
		t.Fatalf("merged tracker config = %v, want preserved secret and new slug", trackerConfig)
	}
	runtimeConfig := runtime.updateReq.Config["agent_runtime"].(map[string]interface{})
	if runtimeConfig["api_key"] != "literal-agent-secret" {
		t.Fatalf("merged runtime config = %v, want preserved secret", runtimeConfig)
	}
}
