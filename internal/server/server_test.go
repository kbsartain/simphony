package server

import (
	"bytes"
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
	"github.com/kbsartain/simphony/pkg/api"
)

type fakeOrchestrator struct {
	snapshot     api.StateSnapshot
	details      map[string]api.IssueDetailResponse
	refresh      api.RefreshResponse
	refreshCalls int
}

func (f *fakeOrchestrator) Snapshot() api.StateSnapshot {
	return f.snapshot
}

func (f *fakeOrchestrator) IssueDetail(identifier string) (api.IssueDetailResponse, bool) {
	detail, ok := f.details[identifier]
	return detail, ok
}

func (f *fakeOrchestrator) Refresh() api.RefreshResponse {
	f.refreshCalls++
	return f.refresh
}

func newTestServer(orch *fakeOrchestrator) *Server {
	s := &Server{
		orch: orch,
		mux:  http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func writeWorkflowForSettingsTest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "WORKFLOW.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	return path
}

func TestHandleStateReturnsSnapshot(t *testing.T) {
	generatedAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	s := newTestServer(&fakeOrchestrator{
		snapshot: api.StateSnapshot{
			GeneratedAt:         generatedAt,
			PollIntervalMs:      5000,
			MaxConcurrentAgents: 4,
			Counts:              api.StateCounts{Running: 2, Retrying: 1, Claimed: 3, Completed: 4},
			CodexTotals:         api.CodexTotals{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, SecondsRunning: 12.5},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("cors origin = %q, want *", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q, want no-store", got)
	}

	var snapshot api.StateSnapshot
	if err := json.NewDecoder(rec.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.Counts.Running != 2 || snapshot.Counts.Retrying != 1 {
		t.Fatalf("counts = %+v, want running=2 retrying=1", snapshot.Counts)
	}
	if snapshot.Counts.Claimed != 3 || snapshot.Counts.Completed != 4 {
		t.Fatalf("counts = %+v, want claimed=3 completed=4", snapshot.Counts)
	}
	if snapshot.PollIntervalMs != 5000 || snapshot.MaxConcurrentAgents != 4 {
		t.Fatalf("runtime config = poll %d max %d, want poll 5000 max 4", snapshot.PollIntervalMs, snapshot.MaxConcurrentAgents)
	}
	if snapshot.CodexTotals.TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", snapshot.CodexTotals.TotalTokens)
	}
}

func TestHandleRefreshPostsAndOptions(t *testing.T) {
	requestedAt := time.Date(2026, 5, 1, 12, 30, 0, 0, time.UTC)
	orch := &fakeOrchestrator{
		refresh: api.RefreshResponse{
			Queued:      true,
			RequestedAt: requestedAt,
			Operations:  []string{"tick"},
		},
	}
	s := newTestServer(orch)

	optionsReq := httptest.NewRequest(http.MethodOptions, "/api/v1/refresh", nil)
	optionsRec := httptest.NewRecorder()
	s.mux.ServeHTTP(optionsRec, optionsReq)

	if optionsRec.Code != http.StatusNoContent {
		t.Fatalf("options status = %d, want %d", optionsRec.Code, http.StatusNoContent)
	}
	if orch.refreshCalls != 0 {
		t.Fatalf("options refresh calls = %d, want 0", orch.refreshCalls)
	}

	postReq := httptest.NewRequest(http.MethodPost, "/api/v1/refresh", nil)
	postRec := httptest.NewRecorder()
	s.mux.ServeHTTP(postRec, postReq)

	if postRec.Code != http.StatusOK {
		t.Fatalf("post status = %d, want %d", postRec.Code, http.StatusOK)
	}
	if orch.refreshCalls != 1 {
		t.Fatalf("post refresh calls = %d, want 1", orch.refreshCalls)
	}

	var refresh api.RefreshResponse
	if err := json.NewDecoder(postRec.Body).Decode(&refresh); err != nil {
		t.Fatalf("decode refresh: %v", err)
	}
	if !refresh.Queued || len(refresh.Operations) != 1 || refresh.Operations[0] != "tick" {
		t.Fatalf("refresh response = %+v, want queued tick", refresh)
	}
}

func TestHandleRefreshRejectsGet(t *testing.T) {
	orch := &fakeOrchestrator{}
	s := newTestServer(orch)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/refresh", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if orch.refreshCalls != 0 {
		t.Fatalf("refresh calls = %d, want 0", orch.refreshCalls)
	}

	var body api.APIErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "method_not_allowed" || body.Error.Message != "Only POST is allowed" {
		t.Fatalf("error = %+v, want POST-only method error", body.Error)
	}
}

func TestHandleRuntimeModeReturnsSingleWorkflow(t *testing.T) {
	workflowPath := writeWorkflowForSettingsTest(t, "---\ntracker:\n  kind: linear\n---\n\nPrompt body\n")
	s := NewWithSettings(&fakeOrchestrator{}, 8080, workflowPath, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtime-mode", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body api.RuntimeModeResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode runtime mode: %v", err)
	}
	if body.Mode != api.RuntimeModeSingleWorkflow || body.WorkflowPath != workflowPath {
		t.Fatalf("runtime mode = %+v, want single workflow with path %q", body, workflowPath)
	}
	if !body.ChangeRequiresRestart {
		t.Fatalf("change_requires_restart = false, want true")
	}
}

func TestHandleRegistryBootstrapCreatesStarterRegistry(t *testing.T) {
	workflowPath := writeWorkflowForSettingsTest(t, `---
tracker:
  kind: linear
  api_key: test-linear-key
  project_slug: simphony
workspace:
  root: ./simphony_workspaces
---

Prompt body
`)
	s := NewWithSettings(&fakeOrchestrator{}, 8080, workflowPath, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/registry/bootstrap", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var body api.RegistryBootstrapResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if !body.Created {
		t.Fatalf("created = false, want true")
	}
	if body.WorkflowPath != workflowPath || body.ProjectID == "" || body.ProjectName == "" {
		t.Fatalf("bootstrap response = %+v, want workflow path and project identity", body)
	}
	if !strings.Contains(body.Command, "-config") || !strings.Contains(body.Command, body.RegistryPath) {
		t.Fatalf("command = %q, want -config registry path", body.Command)
	}

	data, err := os.ReadFile(body.RegistryPath)
	if err != nil {
		t.Fatalf("read generated registry: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "workflow_path: WORKFLOW.md") {
		t.Fatalf("generated registry = %q, want relative workflow path", text)
	}
	if !strings.Contains(text, "allow_workspace_under_registry_dir: true") {
		t.Fatalf("generated registry = %q, want explicit workspace-under-registry opt-in", text)
	}
}

func TestHandleRegistryBootstrapDoesNotOverwriteExistingRegistry(t *testing.T) {
	workflowPath := writeWorkflowForSettingsTest(t, "---\ntracker:\n  kind: linear\n---\n\nPrompt body\n")
	registryPath := filepath.Join(filepath.Dir(workflowPath), "simphony.yaml")
	original := "projects: []\n"
	if err := os.WriteFile(registryPath, []byte(original), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	s := NewWithSettings(&fakeOrchestrator{}, 8080, workflowPath, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/registry/bootstrap", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body api.RegistryBootstrapResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if body.Created {
		t.Fatalf("created = true, want false")
	}
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	if string(data) != original {
		t.Fatalf("registry content = %q, want original %q", string(data), original)
	}
}

func TestHandleIssueDetail(t *testing.T) {
	orch := &fakeOrchestrator{
		details: map[string]api.IssueDetailResponse{
			"CON-1": {
				IssueIdentifier: "CON-1",
				IssueID:         "issue-1",
				Status:          "running",
				Workspace:       api.WorkspaceDetail{Path: `C:\tmp\CON-1`},
			},
		},
	}
	s := newTestServer(orch)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/CON-1", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var detail api.IssueDetailResponse
	if err := json.NewDecoder(rec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.IssueIdentifier != "CON-1" || detail.Status != "running" {
		t.Fatalf("detail = %+v, want CON-1 running", detail)
	}
}

func TestHandleIssueDetailDecodesIdentifier(t *testing.T) {
	orch := &fakeOrchestrator{
		details: map[string]api.IssueDetailResponse{
			"CON 1": {
				IssueIdentifier: "CON 1",
				IssueID:         "issue-1",
				Status:          "running",
			},
		},
	}
	s := newTestServer(orch)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/CON%201", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var detail api.IssueDetailResponse
	if err := json.NewDecoder(rec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.IssueIdentifier != "CON 1" {
		t.Fatalf("identifier = %q, want CON 1", detail.IssueIdentifier)
	}
}

func TestHandleIssueDetailRejectsInvalidEscapedIdentifier(t *testing.T) {
	s := newTestServer(&fakeOrchestrator{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/placeholder", nil)
	req.URL.Path = "/api/v1/CON%ZZ"
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var body api.APIErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "bad_request" {
		t.Fatalf("error code = %q, want bad_request", body.Error.Code)
	}
}

func TestHandleIssueDetailNotFound(t *testing.T) {
	s := newTestServer(&fakeOrchestrator{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/MISSING-1", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	var body api.APIErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "not_found" || body.Error.Message != "Issue MISSING-1 not found" {
		t.Fatalf("error = %+v, want not_found message", body.Error)
	}
}

func TestHandleStateRejectsPost(t *testing.T) {
	s := newTestServer(&fakeOrchestrator{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/state", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}

	var body api.APIErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "method_not_allowed" {
		t.Fatalf("error code = %q, want method_not_allowed", body.Error.Code)
	}
}

func TestServeDashboardStaticAsset(t *testing.T) {
	s := newServerWithDashboardDist(t, map[string]string{
		filepath.Join("assets", "app.js"): "console.log('simphony')",
	})

	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); body != "console.log('simphony')" {
		t.Fatalf("asset body = %q, want app js", body)
	}
}

func TestServeDashboardSPAFallback(t *testing.T) {
	s := newServerWithDashboardDist(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/issues/CON-1", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "<div id=\"root\"></div>") {
		t.Fatalf("fallback body = %q, want dashboard index", rec.Body.String())
	}
}

func TestServeDashboardMissingAssetReturnsNotFound(t *testing.T) {
	s := newServerWithDashboardDist(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if strings.Contains(rec.Body.String(), "<div id=\"root\"></div>") {
		t.Fatalf("missing asset served dashboard index")
	}
}

func TestServeDashboardDoesNotCatchAPIPaths(t *testing.T) {
	s := newServerWithDashboardDist(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/unknown", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	var body api.APIErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "not_found" || body.Error.Message != "API endpoint not found" {
		t.Fatalf("error = %+v, want API endpoint not found", body.Error)
	}
}

func TestUnknownAPIPathReturnsAPIErrorWithoutDashboardDist(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	s := newTestServer(&fakeOrchestrator{})

	req := httptest.NewRequest(http.MethodGet, "/api/unknown", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	var body api.APIErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "not_found" || body.Error.Message != "API endpoint not found" {
		t.Fatalf("error = %+v, want API endpoint not found", body.Error)
	}
}

func TestSettingsAPIRequiresWorkflowPath(t *testing.T) {
	srv := NewWithSettings(&fakeOrchestrator{}, 8080, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got api.APIErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Error.Code != "not_found" {
		t.Errorf("error code = %q, want not_found", got.Error.Code)
	}
}

func TestSettingsGetReturnsEditableAndResolvedConfig(t *testing.T) {
	t.Setenv("LINEAR_TEST_KEY", "resolved-secret")
	workflowPath := writeWorkflowForSettingsTest(t, `---
tracker:
  kind: linear
  api_key: $LINEAR_TEST_KEY
  project_slug: proj
server:
  port: 8080
---

Prompt body
`)

	srv := NewWithSettings(&fakeOrchestrator{}, 8080, workflowPath, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got api.SettingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.WorkflowPath != workflowPath {
		t.Errorf("workflow_path = %q, want %q", got.WorkflowPath, workflowPath)
	}
	tracker := got.Config["tracker"].(map[string]interface{})
	if tracker["api_key"] != "$LINEAR_TEST_KEY" {
		t.Errorf("editable api_key = %v, want env reference", tracker["api_key"])
	}
	if got.ResolvedConfig.Tracker.APIKey != "********" {
		t.Errorf("resolved api_key = %q, want mask", got.ResolvedConfig.Tracker.APIKey)
	}
	if got.PromptTemplate != "Prompt body" {
		t.Errorf("prompt_template = %q, want Prompt body", got.PromptTemplate)
	}
	if got.ValidationError != nil {
		t.Errorf("validation_error = %v, want nil", *got.ValidationError)
	}
}

func TestSettingsGetMasksLiteralEditableSecrets(t *testing.T) {
	workflowPath := writeWorkflowForSettingsTest(t, `---
tracker:
  kind: linear
  api_key: literal-linear-secret
  project_slug: proj
agent_runtime:
  provider: codex
  api_key: literal-agent-secret
  auth_token: literal-agent-token
  stage_overrides:
    coding:
      api_key: literal-stage-secret
      auth_token: literal-stage-token
      env:
        ROUTER_TOKEN: literal-stage-env-secret
---

Prompt body
`)

	srv := NewWithSettings(&fakeOrchestrator{}, 8080, workflowPath, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got api.SettingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	trackerConfig := got.Config["tracker"].(map[string]interface{})
	if trackerConfig["api_key"] != settingsSecretMask {
		t.Fatalf("tracker api_key = %v, want mask", trackerConfig["api_key"])
	}
	runtimeConfig := got.Config["agent_runtime"].(map[string]interface{})
	if runtimeConfig["api_key"] != settingsSecretMask || runtimeConfig["auth_token"] != settingsSecretMask {
		t.Fatalf("runtime secrets = %v, want masks", runtimeConfig)
	}
	stageOverrides := runtimeConfig["stage_overrides"].(map[string]interface{})
	coding := stageOverrides["coding"].(map[string]interface{})
	if coding["api_key"] != settingsSecretMask || coding["auth_token"] != settingsSecretMask {
		t.Fatalf("stage secrets = %v, want masks", coding)
	}
	stageEnv := coding["env"].(map[string]interface{})
	if stageEnv["ROUTER_TOKEN"] != settingsSecretMask {
		t.Fatalf("stage env = %v, want mask", stageEnv)
	}
	if strings.Contains(rec.Body.String(), "literal-linear-secret") || strings.Contains(rec.Body.String(), "literal-agent-secret") || strings.Contains(rec.Body.String(), "literal-stage-secret") || strings.Contains(rec.Body.String(), "literal-stage-env-secret") {
		t.Fatal("response leaked literal secret")
	}
}

func TestSettingsPutPreservesMaskedSecrets(t *testing.T) {
	workflowPath := writeWorkflowForSettingsTest(t, `---
tracker:
  kind: linear
  api_key: literal-linear-secret
  project_slug: old-proj
agent_runtime:
  provider: codex
  api_key: literal-agent-secret
  stage_overrides:
    coding:
      api_key: literal-stage-secret
      auth_token: literal-stage-token
---

Prompt body
`)

	srv := NewWithSettings(&fakeOrchestrator{}, 8080, workflowPath, nil)
	reqPayload := api.SettingsUpdateRequest{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":         "linear",
				"api_key":      settingsSecretMask,
				"project_slug": "new-proj",
			},
			"agent_runtime": map[string]interface{}{
				"provider": "codex",
				"api_key":  settingsSecretMask,
				"stage_overrides": map[string]interface{}{
					"coding": map[string]interface{}{
						"api_key":    settingsSecretMask,
						"auth_token": settingsSecretMask,
					},
				},
			},
		},
	}
	body, err := json.Marshal(reqPayload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	saved, err := config.LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("load saved workflow: %v", err)
	}
	trackerConfig := saved.Config["tracker"].(map[string]interface{})
	if trackerConfig["api_key"] != "literal-linear-secret" || trackerConfig["project_slug"] != "new-proj" {
		t.Fatalf("saved tracker = %v, want preserved secret and new project", trackerConfig)
	}
	runtimeConfig := saved.Config["agent_runtime"].(map[string]interface{})
	if runtimeConfig["api_key"] != "literal-agent-secret" {
		t.Fatalf("saved agent_runtime = %v, want preserved secret", runtimeConfig)
	}
	stageOverrides := runtimeConfig["stage_overrides"].(map[string]interface{})
	coding := stageOverrides["coding"].(map[string]interface{})
	if coding["api_key"] != "literal-stage-secret" || coding["auth_token"] != "literal-stage-token" {
		t.Fatalf("saved stage override = %v, want preserved secrets", coding)
	}
	if strings.Contains(rec.Body.String(), "literal-linear-secret") || strings.Contains(rec.Body.String(), "literal-agent-secret") || strings.Contains(rec.Body.String(), "literal-stage-secret") {
		t.Fatal("response leaked literal secret")
	}
}

func TestSettingsPutValidatesBeforeSaving(t *testing.T) {
	workflowPath := writeWorkflowForSettingsTest(t, `---
tracker:
  kind: linear
  api_key: key
  project_slug: proj
---

Original prompt
`)
	original, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}

	srv := NewWithSettings(&fakeOrchestrator{}, 8080, workflowPath, nil)
	reqBody := `{"config":{"tracker":{"kind":"linear","api_key":"key","project_slug":"proj"},"polling":{"interval_ms":0}}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	after, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read workflow after failed save: %v", err)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("workflow changed despite validation failure")
	}
}

func TestSettingsPutSavesAndAppliesWorkflow(t *testing.T) {
	workflowPath := writeWorkflowForSettingsTest(t, `---
tracker:
  kind: linear
  api_key: key
  project_slug: proj
---

Original prompt
`)
	var appliedDef *api.WorkflowDefinition
	var appliedCfg *api.WorkflowConfig
	srv := NewWithSettings(&fakeOrchestrator{}, 8080, workflowPath, func(def *api.WorkflowDefinition, cfg *api.WorkflowConfig) error {
		appliedDef = def
		appliedCfg = cfg
		return nil
	})

	prompt := "Updated prompt {{ issue.identifier }}"
	reqPayload := api.SettingsUpdateRequest{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":         "linear",
				"api_key":      "new-key",
				"project_slug": "proj",
			},
			"polling": map[string]interface{}{
				"interval_ms": float64(45000),
			},
			"future_section": map[string]interface{}{
				"enabled": true,
			},
		},
		PromptTemplate: &prompt,
	}
	body, err := json.Marshal(reqPayload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if appliedDef == nil || appliedCfg == nil {
		t.Fatal("settings applier was not called")
	}
	if appliedCfg.Polling.IntervalMs != 45000 {
		t.Errorf("applied polling interval = %d, want 45000", appliedCfg.Polling.IntervalMs)
	}
	if appliedDef.PromptTemplate != prompt {
		t.Errorf("applied prompt = %q, want %q", appliedDef.PromptTemplate, prompt)
	}

	saved, err := config.LoadWorkflow(workflowPath)
	if err != nil {
		t.Fatalf("load saved workflow: %v", err)
	}
	if saved.PromptTemplate != prompt {
		t.Errorf("saved prompt = %q, want %q", saved.PromptTemplate, prompt)
	}
	if saved.Config["future_section"].(map[string]interface{})["enabled"] != true {
		t.Errorf("future section not preserved: %v", saved.Config)
	}
}

func TestSettingsPutAppliesBeforeSavingAndDoesNotSaveWhenApplyFails(t *testing.T) {
	workflowPath := writeWorkflowForSettingsTest(t, `---
tracker:
  kind: linear
  api_key: key
  project_slug: proj
polling:
  interval_ms: 30000
---

Original prompt
`)
	original, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	srv := NewWithSettings(&fakeOrchestrator{}, 8080, workflowPath, func(*api.WorkflowDefinition, *api.WorkflowConfig) error {
		duringApply, err := os.ReadFile(workflowPath)
		if err != nil {
			t.Fatalf("read workflow during apply: %v", err)
		}
		if !bytes.Equal(duringApply, original) {
			t.Fatal("workflow was saved before runtime apply")
		}
		return errors.New("apply failed")
	})

	prompt := "Updated prompt"
	reqPayload := api.SettingsUpdateRequest{
		Config: map[string]interface{}{
			"tracker": map[string]interface{}{
				"kind":         "linear",
				"api_key":      "key",
				"project_slug": "proj",
			},
			"polling": map[string]interface{}{
				"interval_ms": float64(45000),
			},
		},
		PromptTemplate: &prompt,
	}
	body, err := json.Marshal(reqPayload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	after, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read workflow after failed apply: %v", err)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("workflow changed despite apply failure")
	}
}

func newServerWithDashboardDist(t *testing.T, files map[string]string) *Server {
	t.Helper()

	root := t.TempDir()
	t.Chdir(root)
	distDir := filepath.Join(root, "dashboard", "dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		t.Fatalf("create dist dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "index.html"), []byte(`<!doctype html><div id="root"></div>`), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	for name, content := range files {
		path := filepath.Join(distDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create asset dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write asset: %v", err)
		}
	}

	return newTestServer(&fakeOrchestrator{})
}
