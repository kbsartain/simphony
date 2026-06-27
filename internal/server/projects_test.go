package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	registry    *config.ProjectRegistry
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

func (f *fakeProjectManager) Registry() *config.ProjectRegistry {
	return f.registry
}

func newTestProjectServer(manager ProjectRuntimeManager) *ProjectServer {
	return NewProjectServer(manager, "127.0.0.1", 8080, "/api/v1")
}

func TestProjectServerReturnsRegistryWithoutSecrets(t *testing.T) {
	dir := t.TempDir()
	alphaWorkflow := filepath.Join(dir, "alpha", "WORKFLOW.md")
	betaWorkflow := filepath.Join(dir, "beta", "WORKFLOW.md")
	writeServerTestWorkflow(t, alphaWorkflow, filepath.Join(dir, "..", "alpha-workspaces"))
	writeServerTestWorkflow(t, betaWorkflow, filepath.Join(dir, "..", "beta-workspaces"))
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeServerTestFile(t, registryPath, `
server:
  bind_address: 127.0.0.1
  port: 8080
agent_runtime:
  provider: codex
  model: kimi-k2
  endpoint_url: https://openai-compatible.example/v1
  api_key: secret-openai-key
  auth_token: secret-auth-token
  env:
    SAFE_PUBLIC_FLAG: enabled
concurrency:
  max_concurrent_agents: 10
security:
  allow_workspace_overlap: false
projects:
  - id: alpha
    name: Alpha
    workflow_path: alpha/WORKFLOW.md
  - id: beta
    name: Beta
    workflow_path: beta/WORKFLOW.md
`)
	registry, err := config.LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}
	manager := &fakeProjectManager{registry: registry}
	s := newTestProjectServer(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/registry", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body api.RegistryResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.SourcePath != registryPath || len(body.Projects) != 2 {
		t.Fatalf("registry response = %+v, want source and two projects", body)
	}
	if !body.AgentRuntime.APIKeyConfigured || !body.AgentRuntime.AuthTokenConfigured {
		t.Fatalf("agent runtime secret flags = %+v, want configured", body.AgentRuntime)
	}
	if strings.Contains(rec.Body.String(), "secret-openai-key") || strings.Contains(rec.Body.String(), "secret-auth-token") {
		t.Fatalf("registry response leaked secret: %s", rec.Body.String())
	}
	if len(body.AgentRuntime.EnvKeys) != 1 || body.AgentRuntime.EnvKeys[0] != "SAFE_PUBLIC_FLAG" {
		t.Fatalf("env keys = %+v, want SAFE_PUBLIC_FLAG", body.AgentRuntime.EnvKeys)
	}
	if len(body.Warnings) != 1 || body.Warnings[0].Code != "duplicate_tracker_project" {
		t.Fatalf("warnings = %+v, want duplicate tracker project warning", body.Warnings)
	}
}

func TestProjectServerReturnsRuntimeMode(t *testing.T) {
	registry := &config.ProjectRegistry{SourcePath: filepath.Join(t.TempDir(), "simphony.yaml")}
	manager := &fakeProjectManager{registry: registry}
	s := newTestProjectServer(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtime-mode", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body api.RuntimeModeResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode runtime mode: %v", err)
	}
	if body.Mode != api.RuntimeModeProjectRegistry || body.RegistryPath != registry.SourcePath {
		t.Fatalf("runtime mode = %+v, want project registry with path %q", body, registry.SourcePath)
	}
	if !body.ChangeRequiresRestart {
		t.Fatalf("change_requires_restart = false, want true")
	}
}

func TestProjectServerCreatesRegistryProject(t *testing.T) {
	dir := t.TempDir()
	alphaWorkflow := filepath.Join(dir, "alpha", "WORKFLOW.md")
	betaWorkflow := filepath.Join(dir, "beta", "WORKFLOW.md")
	writeServerTestWorkflow(t, alphaWorkflow, filepath.Join(dir, "..", "alpha-workspaces"))
	writeServerTestWorkflow(t, betaWorkflow, filepath.Join(dir, "..", "beta-workspaces"))
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeServerTestFile(t, registryPath, `
server:
  bind_address: 127.0.0.1
  port: 8080
projects:
  - id: alpha
    name: Alpha
    workflow_path: alpha/WORKFLOW.md
`)
	registry, err := config.LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}
	manager := &fakeProjectManager{registry: registry}
	s := newTestProjectServer(manager)

	payload := `{"id":"beta","name":"Beta","workflow_path":"beta/WORKFLOW.md","enabled":false,"max_concurrent_agents":2}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/registry/projects", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var body api.RegistryProjectCreateResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Project.ID != "beta" || body.Project.Enabled || body.Project.MaxConcurrentAgents != 2 {
		t.Fatalf("created project = %+v, want disabled beta with cap", body.Project)
	}
	if !body.ChangeRequiresRestart || !strings.Contains(body.Command, "-config") {
		t.Fatalf("restart metadata = command %q restart %v, want restart command", body.Command, body.ChangeRequiresRestart)
	}
	if len(body.Registry.Projects) != 2 || len(manager.registry.Projects) != 2 {
		t.Fatalf("registry projects = response %d manager %d, want 2", len(body.Registry.Projects), len(manager.registry.Projects))
	}
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "id: beta") || !strings.Contains(text, "enabled: false") || !strings.Contains(text, "max_concurrent_agents: 2") {
		t.Fatalf("registry file = %q, want appended beta project", text)
	}
}

func TestProjectServerRejectsDuplicateRegistryProject(t *testing.T) {
	dir := t.TempDir()
	alphaWorkflow := filepath.Join(dir, "alpha", "WORKFLOW.md")
	writeServerTestWorkflow(t, alphaWorkflow, filepath.Join(dir, "..", "alpha-workspaces"))
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeServerTestFile(t, registryPath, `
projects:
  - id: alpha
    name: Alpha
    workflow_path: alpha/WORKFLOW.md
`)
	registry, err := config.LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}
	before, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	s := newTestProjectServer(&fakeProjectManager{registry: registry})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/registry/projects", strings.NewReader(`{"id":"ALPHA","workflow_path":"alpha/WORKFLOW.md"}`))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	var apiErr api.APIErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if apiErr.Error.Code != "project_id_exists" {
		t.Fatalf("error code = %q, want project_id_exists", apiErr.Error.Code)
	}
	after, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read registry after: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("registry file changed on duplicate")
	}
}

func TestProjectServerUpdatesRegistryProject(t *testing.T) {
	dir := t.TempDir()
	alphaWorkflow := filepath.Join(dir, "alpha", "WORKFLOW.md")
	betaWorkflow := filepath.Join(dir, "beta", "WORKFLOW.md")
	writeServerTestWorkflow(t, alphaWorkflow, filepath.Join(dir, "..", "alpha-workspaces"))
	writeServerTestWorkflow(t, betaWorkflow, filepath.Join(dir, "..", "beta-workspaces"))
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeServerTestFile(t, registryPath, `
projects:
  - id: alpha
    name: Alpha
    workflow_path: alpha/WORKFLOW.md
    max_concurrent_agents: 3
`)
	registry, err := config.LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}
	s := newTestProjectServer(&fakeProjectManager{registry: registry})

	payload := `{"name":"Alpha Disabled","workflow_path":"beta/WORKFLOW.md","enabled":false,"max_concurrent_agents":0}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/registry/projects/alpha", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body api.RegistryProjectUpdateResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Project.Name != "Alpha Disabled" || body.Project.Enabled || body.Project.MaxConcurrentAgents != 0 {
		t.Fatalf("updated project = %+v, want disabled project with cleared cap", body.Project)
	}
	if !body.ChangeRequiresRestart {
		t.Fatalf("change_requires_restart = false, want true")
	}
	if registry.Projects[0].WorkflowPath != betaWorkflow {
		t.Fatalf("manager registry workflow = %q, want %q", registry.Projects[0].WorkflowPath, betaWorkflow)
	}
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "name: Alpha Disabled") || !strings.Contains(text, "workflow_path: beta/WORKFLOW.md") || !strings.Contains(text, "enabled: false") {
		t.Fatalf("registry file = %q, want updated project", text)
	}
	if strings.Contains(text, "max_concurrent_agents") {
		t.Fatalf("registry file = %q, want cleared max_concurrent_agents", text)
	}
}

func TestProjectServerRejectsUnknownRegistryProjectUpdate(t *testing.T) {
	dir := t.TempDir()
	alphaWorkflow := filepath.Join(dir, "alpha", "WORKFLOW.md")
	writeServerTestWorkflow(t, alphaWorkflow, filepath.Join(dir, "..", "alpha-workspaces"))
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeServerTestFile(t, registryPath, `
projects:
  - id: alpha
    name: Alpha
    workflow_path: alpha/WORKFLOW.md
`)
	registry, err := config.LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}
	s := newTestProjectServer(&fakeProjectManager{registry: registry})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/registry/projects/missing", strings.NewReader(`{"workflow_path":"alpha/WORKFLOW.md"}`))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	var body api.APIErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "project_not_found" {
		t.Fatalf("error code = %q, want project_not_found", body.Error.Code)
	}
}

func TestProjectServerDeletesRegistryProject(t *testing.T) {
	dir := t.TempDir()
	alphaWorkflow := filepath.Join(dir, "alpha", "WORKFLOW.md")
	betaWorkflow := filepath.Join(dir, "beta", "WORKFLOW.md")
	writeServerTestWorkflow(t, alphaWorkflow, filepath.Join(dir, "..", "alpha-workspaces"))
	writeServerTestWorkflow(t, betaWorkflow, filepath.Join(dir, "..", "beta-workspaces"))
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeServerTestFile(t, registryPath, `
projects:
  - id: alpha
    name: Alpha
    workflow_path: alpha/WORKFLOW.md
  - id: beta
    name: Beta
    workflow_path: beta/WORKFLOW.md
`)
	registry, err := config.LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}
	s := newTestProjectServer(&fakeProjectManager{registry: registry})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/registry/projects/beta", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body api.RegistryProjectDeleteResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ProjectID != "beta" || body.ProjectName != "Beta" || len(body.Registry.Projects) != 1 {
		t.Fatalf("delete response = %+v, want beta removed and one project", body)
	}
	if len(registry.Projects) != 1 || registry.Projects[0].ID != "alpha" {
		t.Fatalf("manager registry projects = %+v, want alpha only", registry.Projects)
	}
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "id: beta") || !strings.Contains(text, "id: alpha") {
		t.Fatalf("registry file = %q, want beta removed and alpha retained", text)
	}
}

func TestProjectServerRejectsDeletingLastRegistryProject(t *testing.T) {
	dir := t.TempDir()
	alphaWorkflow := filepath.Join(dir, "alpha", "WORKFLOW.md")
	writeServerTestWorkflow(t, alphaWorkflow, filepath.Join(dir, "..", "alpha-workspaces"))
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeServerTestFile(t, registryPath, `
projects:
  - id: alpha
    name: Alpha
    workflow_path: alpha/WORKFLOW.md
`)
	registry, err := config.LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}
	s := newTestProjectServer(&fakeProjectManager{registry: registry})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/registry/projects/alpha", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	var body api.APIErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "registry_requires_project" {
		t.Fatalf("error code = %q, want registry_requires_project", body.Error.Code)
	}
}

func writeServerTestWorkflow(t *testing.T, path string, workspaceRoot string) {
	t.Helper()
	content := "---\ntracker:\n  kind: linear\n  api_key: test-linear-key\n  project_slug: shared-project\nworkspace:\n  root: " + filepath.ToSlash(workspaceRoot) + "\n---\n\nWork on {{ issue.identifier }}.\n"
	writeServerTestFile(t, path, content)
}

func writeServerTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
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
