package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	control         api.ControlState
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

func (f *fakeProjectRuntime) SetProjectPaused(paused bool) (api.ControlState, bool) {
	f.control.Paused = paused
	return f.control, true
}

func (f *fakeProjectRuntime) SetStagePaused(stage string, paused bool) (api.ControlState, bool, error) {
	valid := map[string]bool{"coding": true, "review": true, "review_resolution": true, "merge": true}
	if !valid[stage] {
		return f.control, true, errors.New("unknown pipeline stage")
	}
	stages := make(map[string]bool)
	for _, existing := range f.control.PausedStages {
		stages[existing] = true
	}
	if paused {
		stages[stage] = true
	} else {
		delete(stages, stage)
	}
	f.control.PausedStages = nil
	for _, candidate := range []string{"coding", "merge", "review", "review_resolution"} {
		if stages[candidate] {
			f.control.PausedStages = append(f.control.PausedStages, candidate)
		}
	}
	return f.control, true, nil
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
  api_key: $SIM_TEST_OPENAI_KEY
  auth_token: $SIM_TEST_AUTH_TOKEN
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

func TestProjectServerServesDashboardAndProjectAPI(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "dashboard", "dist"), 0o755); err != nil {
		t.Fatalf("mkdir dashboard dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "dashboard", "dist", "index.html"), []byte("<!doctype html><title>Simphony</title>"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir temp dashboard root: %v", err)
	}
	defer func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	registry := &config.ProjectRegistry{SourcePath: filepath.Join(tmp, "simphony.yaml")}
	manager := &fakeProjectManager{
		registry: registry,
		summaries: []project.RuntimeSummary{{
			ID:           "alpha",
			Name:         "Alpha",
			WorkflowPath: filepath.Join(tmp, "alpha", "WORKFLOW.md"),
			Enabled:      true,
			Running:      true,
		}},
	}
	s := newTestProjectServer(manager)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Simphony") {
		t.Fatalf("dashboard body = %q, want built index", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("projects status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body api.ProjectsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode projects response: %v", err)
	}
	if len(body.Projects) != 1 || body.Projects[0].ID != "alpha" {
		t.Fatalf("projects response = %+v, want alpha project", body.Projects)
	}
}

func TestProjectServerUpdatesRegistrySettings(t *testing.T) {
	dir := t.TempDir()
	alphaWorkflow := filepath.Join(dir, "alpha", "WORKFLOW.md")
	writeServerTestWorkflow(t, alphaWorkflow, filepath.Join(dir, "..", "alpha-workspaces"))
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
	s := newTestProjectServer(&fakeProjectManager{registry: registry})

	payload := `{
  "server": {
    "bind_address": "127.0.0.1",
    "port": 9090,
    "dashboard_enabled": false,
    "api_prefix": "custom-api"
  },
  "concurrency": {
    "max_concurrent_agents": 8,
    "default_project_max_concurrent_agents": 2
  },
  "security": {
    "allow_workspace_overlap": true,
    "allow_workspace_under_registry_dir": true,
    "allow_remote_dashboard": false
  }
}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/registry", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body api.RegistryUpdateResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Registry.Server == nil || body.Registry.Server.Port != 9090 || body.Registry.Server.APIPrefix != "/custom-api" || body.Registry.Server.DashboardEnabled {
		t.Fatalf("registry server = %+v, want updated server settings", body.Registry.Server)
	}
	if body.Registry.Concurrency.MaxConcurrentAgents != 8 || body.Registry.Concurrency.DefaultProjectMaxConcurrentAgents != 2 {
		t.Fatalf("registry concurrency = %+v, want updated caps", body.Registry.Concurrency)
	}
	if !body.Registry.Security.AllowWorkspaceOverlap || !body.Registry.Security.AllowWorkspaceUnderRegistryDir || body.Registry.Security.AllowRemoteDashboard {
		t.Fatalf("registry security = %+v, want updated security flags", body.Registry.Security)
	}
	if !body.ChangeRequiresRestart || !strings.Contains(body.Command, "-config") {
		t.Fatalf("restart metadata = command %q restart %v, want restart command", body.Command, body.ChangeRequiresRestart)
	}
	if registry.Server.Port != 9090 || registry.Server.APIPrefix != "/custom-api" || registry.Concurrency.MaxConcurrentAgents != 8 {
		t.Fatalf("manager registry = %+v, want in-memory settings updated", registry)
	}
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"port: 9090",
		"dashboard_enabled: false",
		"api_prefix: /custom-api",
		"max_concurrent_agents: 8",
		"default_project_max_concurrent_agents: 2",
		"allow_workspace_overlap: true",
		"allow_workspace_under_registry_dir: true",
		"allow_remote_dashboard: false",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("registry file = %q, want %q", text, want)
		}
	}
}

func TestProjectServerRejectsInvalidRegistrySettings(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodPut, "/api/v1/registry", strings.NewReader(`{"server":{"port":70000}}`))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	var body api.APIErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "registry_settings_invalid" {
		t.Fatalf("error code = %q, want registry_settings_invalid", body.Error.Code)
	}
	after, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read registry after: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("registry file changed on invalid settings")
	}
}

func TestProjectServerUpdatesRegistryAgentRuntimeDefaultsAndPreservesSecrets(t *testing.T) {
	dir := t.TempDir()
	alphaWorkflow := filepath.Join(dir, "alpha", "WORKFLOW.md")
	writeServerTestWorkflow(t, alphaWorkflow, filepath.Join(dir, "..", "alpha-workspaces"))
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeServerTestFile(t, registryPath, `
agent_runtime:
  provider: codex
  model: old-model
  api_key: $OLD_RUNTIME_KEY
  auth_token: $OLD_RUNTIME_TOKEN
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

	payload := `{
  "agent_runtime": {
    "provider": "codex",
    "model": "kimi-k2",
    "model_provider": "moonshot",
    "reasoning_effort": "medium",
    "endpoint_url": "https://openai-compatible.example/v1"
  }
}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/registry", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body api.RegistryUpdateResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Registry.AgentRuntime.Provider != "codex" || body.Registry.AgentRuntime.Model != "kimi-k2" || body.Registry.AgentRuntime.ModelProvider != "moonshot" {
		t.Fatalf("agent runtime = %+v, want updated codex moonshot model", body.Registry.AgentRuntime)
	}
	if !body.Registry.AgentRuntime.APIKeyConfigured || !body.Registry.AgentRuntime.AuthTokenConfigured {
		t.Fatalf("secret flags = %+v, want existing secrets preserved", body.Registry.AgentRuntime)
	}
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"model: kimi-k2",
		"model_provider: moonshot",
		"reasoning_effort: medium",
		"endpoint_url: https://openai-compatible.example/v1",
		"api_key: $OLD_RUNTIME_KEY",
		"auth_token: $OLD_RUNTIME_TOKEN",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("registry file = %q, want %q", text, want)
		}
	}
}

func TestProjectServerReplacesRegistryAgentRuntimeSecretWhenProvided(t *testing.T) {
	dir := t.TempDir()
	alphaWorkflow := filepath.Join(dir, "alpha", "WORKFLOW.md")
	writeServerTestWorkflow(t, alphaWorkflow, filepath.Join(dir, "..", "alpha-workspaces"))
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeServerTestFile(t, registryPath, `
agent_runtime:
  provider: codex
  api_key: $OLD_RUNTIME_KEY
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

	req := httptest.NewRequest(http.MethodPut, "/api/v1/registry", strings.NewReader(`{"agent_runtime":{"provider":"claude","api_key":"$ANTHROPIC_API_KEY","permission_mode":"acceptEdits","allowed_tools":["Read","Edit"]}}`))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body api.RegistryUpdateResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Registry.AgentRuntime.Provider != "claude" || body.Registry.AgentRuntime.PermissionMode != "acceptEdits" {
		t.Fatalf("agent runtime = %+v, want claude runtime", body.Registry.AgentRuntime)
	}
	if !body.Registry.AgentRuntime.APIKeyConfigured {
		t.Fatalf("api key flag = false, want configured")
	}
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "old-secret") || !strings.Contains(text, "api_key: $ANTHROPIC_API_KEY") || !strings.Contains(text, "provider: claude") {
		t.Fatalf("registry file = %q, want replaced secret and claude provider", text)
	}
	if !strings.Contains(text, "- Read") || !strings.Contains(text, "- Edit") {
		t.Fatalf("registry file = %q, want allowed tool list", text)
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

	payload := `{"id":"beta","name":"Beta","workflow_path":"beta/WORKFLOW.md","enabled":false,"start_paused":true,"max_concurrent_agents":2}`
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
	if body.Project.ID != "beta" || body.Project.Enabled || !body.Project.StartPaused || body.Project.MaxConcurrentAgents != 2 {
		t.Fatalf("created project = %+v, want disabled and startup-paused beta with cap", body.Project)
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
	if !strings.Contains(text, "id: beta") || !strings.Contains(text, "enabled: false") || !strings.Contains(text, "start_paused: true") || !strings.Contains(text, "max_concurrent_agents: 2") {
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

	payload := `{"name":"Alpha Disabled","workflow_path":"beta/WORKFLOW.md","enabled":false,"start_paused":true,"max_concurrent_agents":0}`
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
	if body.Project.Name != "Alpha Disabled" || body.Project.Enabled || !body.Project.StartPaused || body.Project.MaxConcurrentAgents != 0 {
		t.Fatalf("updated project = %+v, want disabled and startup-paused project with cleared cap", body.Project)
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
	if !strings.Contains(text, "name: Alpha Disabled") || !strings.Contains(text, "workflow_path: beta/WORKFLOW.md") || !strings.Contains(text, "enabled: false") || !strings.Contains(text, "start_paused: true") {
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
	content := "---\ntracker:\n  kind: linear\n  api_key: $SIM_TEST_TRACKER_KEY\n  project_slug: shared-project\nworkspace:\n  root: " + filepath.ToSlash(workspaceRoot) + "\n---\n\nWork on {{ issue.identifier }}.\n"
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
			{ID: "alpha", Name: "Alpha", WorkflowPath: "/tmp/alpha/WORKFLOW.md", Enabled: true, Running: true, MaxConcurrentAgents: 2, WorkflowWatcherRunning: true},
			{ID: "beta", Name: "Beta", WorkflowPath: "/tmp/beta/WORKFLOW.md", Enabled: false, Running: false, WorkflowWatcherError: "watch unavailable"},
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
	if !body.Projects[0].WorkflowWatcherRunning || body.Projects[0].WorkflowWatcherError != "" {
		t.Fatalf("alpha watcher = running:%t error:%q, want running without error", body.Projects[0].WorkflowWatcherRunning, body.Projects[0].WorkflowWatcherError)
	}
	if body.Projects[1].ID != "beta" || body.Projects[1].Running {
		t.Fatalf("beta summary = %+v, want stopped beta", body.Projects[1])
	}
	if body.Projects[1].WorkflowWatcherRunning || body.Projects[1].WorkflowWatcherError != "watch unavailable" {
		t.Fatalf("beta watcher = running:%t error:%q, want stopped with watch unavailable", body.Projects[1].WorkflowWatcherRunning, body.Projects[1].WorkflowWatcherError)
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

func TestProjectServerSoftPauseControls(t *testing.T) {
	runtime := &fakeProjectRuntime{project: config.RegistryProject{ID: "alpha", Name: "Alpha", Enabled: true}}
	manager := &fakeProjectManager{
		summaries: []project.RuntimeSummary{{ID: "alpha", Name: "Alpha", Enabled: true, Running: true}},
		runtimes:  map[string]project.ObservableRuntime{"alpha": runtime},
	}
	s := newTestProjectServer(manager)

	for _, tt := range []struct {
		path       string
		wantPaused bool
		wantStages []string
	}{
		{path: "/api/v1/projects/alpha/pause", wantPaused: true},
		{path: "/api/v1/projects/alpha/stages/review/pause", wantPaused: true, wantStages: []string{"review"}},
		{path: "/api/v1/projects/alpha/resume", wantPaused: false, wantStages: []string{"review"}},
		{path: "/api/v1/projects/alpha/stages/review/resume", wantPaused: false},
	} {
		req := httptest.NewRequest(http.MethodPost, tt.path, nil)
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s status=%d body=%s", tt.path, rec.Code, rec.Body.String())
		}
		var state api.ControlState
		if err := json.NewDecoder(rec.Body).Decode(&state); err != nil {
			t.Fatalf("POST %s decode: %v", tt.path, err)
		}
		if state.Paused != tt.wantPaused || strings.Join(state.PausedStages, ",") != strings.Join(tt.wantStages, ",") {
			t.Fatalf("POST %s state=%+v, want paused=%t stages=%v", tt.path, state, tt.wantPaused, tt.wantStages)
		}
	}

	bad := httptest.NewRequest(http.MethodPost, "/api/v1/projects/alpha/stages/unknown/pause", nil)
	badRec := httptest.NewRecorder()
	s.mux.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusBadRequest || !strings.Contains(badRec.Body.String(), "invalid_stage") {
		t.Fatalf("invalid stage status=%d body=%s", badRec.Code, badRec.Body.String())
	}

	compat := httptest.NewRequest(http.MethodPost, "/api/v1/pause", nil)
	compatRec := httptest.NewRecorder()
	s.mux.ServeHTTP(compatRec, compat)
	if compatRec.Code != http.StatusOK || !strings.Contains(compatRec.Body.String(), `"paused":true`) {
		t.Fatalf("compat pause status=%d body=%s", compatRec.Code, compatRec.Body.String())
	}

	// Repeating a pause is idempotent and returns the same state.
	repeat := httptest.NewRequest(http.MethodPost, "/api/v1/projects/alpha/pause", nil)
	repeatRec := httptest.NewRecorder()
	s.mux.ServeHTTP(repeatRec, repeat)
	if repeatRec.Code != http.StatusOK || !strings.Contains(repeatRec.Body.String(), `"paused":true`) {
		t.Fatalf("repeat pause status=%d body=%s", repeatRec.Code, repeatRec.Body.String())
	}
}

func TestProjectServerPauseUnavailableProjectErrors(t *testing.T) {
	for _, tt := range []struct {
		name     string
		summary  project.RuntimeSummary
		wantCode int
		wantBody string
	}{
		{name: "stopped", summary: project.RuntimeSummary{ID: "alpha", Enabled: true}, wantCode: http.StatusServiceUnavailable, wantBody: "project_not_running"},
		{name: "disabled", summary: project.RuntimeSummary{ID: "alpha", Enabled: false}, wantCode: http.StatusConflict, wantBody: "project_disabled"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestProjectServer(&fakeProjectManager{summaries: []project.RuntimeSummary{tt.summary}, runtimes: map[string]project.ObservableRuntime{}})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/alpha/pause", nil)
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, req)
			if rec.Code != tt.wantCode || !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("status=%d body=%s, want %d containing %s", rec.Code, rec.Body.String(), tt.wantCode, tt.wantBody)
			}
		})
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
