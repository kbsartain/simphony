package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestWorkflow(t *testing.T, path string, extraFrontMatter string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	content := "---\ntracker:\n  kind: linear\n  api_key: $SIM_TEST_TRACKER_KEY\n  project_slug: test-project\n"
	if strings.TrimSpace(extraFrontMatter) != "" {
		content += extraFrontMatter + "\n"
	}
	content += "---\n\nWork on {{ issue.identifier }}.\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
}

func writeTestRegistry(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir registry dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
}

func yamlPath(path string) string {
	return filepath.ToSlash(path)
}

func TestLoadProjectRegistryResolvesProjectsAndDefaults(t *testing.T) {
	t.Setenv("TEST_OPENAI_BASE_URL", "https://openai-compatible.example/v1")
	t.Setenv("TEST_OPENAI_API_KEY", "resolved-openai-key")

	dir := t.TempDir()
	writeTestWorkflow(t, filepath.Join(dir, "workflows", "alpha", "WORKFLOW.md"), "")
	writeTestWorkflow(t, filepath.Join(dir, "workflows", "beta", "WORKFLOW.md"), "")
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeTestRegistry(t, registryPath, `
server:
  bind_address: 127.0.0.1
  port: 9090
  dashboard_enabled: false
  api_prefix: /simphony/api
agent_runtime:
  provider: codex
  model: kimi-k2
  endpoint_url: $TEST_OPENAI_BASE_URL
  api_key: $TEST_OPENAI_API_KEY
concurrency:
  max_concurrent_agents: 10
  default_project_max_concurrent_agents: 3
projects:
  - id: alpha
    name: Alpha Project
    workflow_path: workflows/alpha/WORKFLOW.md
    start_paused: true
  - id: beta
    workflow_path: workflows/beta/WORKFLOW.md
    enabled: false
    max_concurrent_agents: 2
`)

	registry, err := LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}
	if registry.SourcePath != registryPath {
		t.Fatalf("SourcePath = %q, want %q", registry.SourcePath, registryPath)
	}
	if registry.Server == nil {
		t.Fatal("Server = nil, want configured server")
	}
	if registry.Server.Port != 9090 || registry.Server.BindAddress != "127.0.0.1" || registry.Server.DashboardEnabled {
		t.Fatalf("unexpected server config: %+v", registry.Server)
	}
	if registry.Server.APIPrefix != "/simphony/api" {
		t.Fatalf("APIPrefix = %q, want /simphony/api", registry.Server.APIPrefix)
	}
	if registry.Concurrency.MaxConcurrentAgents != 10 {
		t.Fatalf("MaxConcurrentAgents = %d, want 10", registry.Concurrency.MaxConcurrentAgents)
	}
	if registry.Concurrency.DefaultProjectMaxConcurrentAgents != 3 {
		t.Fatalf("DefaultProjectMaxConcurrentAgents = %d, want 3", registry.Concurrency.DefaultProjectMaxConcurrentAgents)
	}
	if registry.AgentRuntime == nil {
		t.Fatal("AgentRuntime = nil, want configured defaults")
	}
	if registry.AgentRuntime.Model != "kimi-k2" {
		t.Fatalf("AgentRuntime.Model = %q, want kimi-k2", registry.AgentRuntime.Model)
	}
	if registry.AgentRuntime.EndpointURL != "https://openai-compatible.example/v1" {
		t.Fatalf("EndpointURL = %q, want env-resolved endpoint", registry.AgentRuntime.EndpointURL)
	}
	if registry.AgentRuntime.APIKey != "resolved-openai-key" || !registry.AgentRuntime.APIKeyConfigured {
		t.Fatalf("api key was not resolved/configured")
	}
	if len(registry.Projects) != 2 {
		t.Fatalf("projects len = %d, want 2", len(registry.Projects))
	}
	alpha := registry.Projects[0]
	if alpha.ID != "alpha" || alpha.Name != "Alpha Project" || !alpha.Enabled || !alpha.StartPaused {
		t.Fatalf("unexpected alpha project: %+v", alpha)
	}
	if !filepath.IsAbs(alpha.WorkflowPath) {
		t.Fatalf("alpha workflow path = %q, want absolute", alpha.WorkflowPath)
	}
	if registry.Projects[1].MaxConcurrentAgents != 2 {
		t.Fatalf("beta MaxConcurrentAgents = %d, want 2", registry.Projects[1].MaxConcurrentAgents)
	}
	enabled := registry.EnabledProjects()
	if len(enabled) != 1 || enabled[0].ID != "alpha" {
		t.Fatalf("enabled projects = %+v, want only alpha", enabled)
	}
}

func TestValidateProjectIsolationAllowsDistinctWorkspaceRootsAndWarnsDuplicateTrackerProject(t *testing.T) {
	dir := t.TempDir()
	alphaRoot := t.TempDir()
	betaRoot := t.TempDir()
	writeTestWorkflow(t, filepath.Join(dir, "alpha", "WORKFLOW.md"), "workspace:\n  root: "+yamlPath(alphaRoot)+"\n")
	writeTestWorkflow(t, filepath.Join(dir, "beta", "WORKFLOW.md"), "workspace:\n  root: "+yamlPath(betaRoot)+"\n")
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeTestRegistry(t, registryPath, `
projects:
  - id: alpha
    workflow_path: alpha/WORKFLOW.md
  - id: beta
    workflow_path: beta/WORKFLOW.md
`)

	registry, err := LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}
	report, err := ValidateProjectIsolation(registry)
	if err != nil {
		t.Fatalf("ValidateProjectIsolation returned error: %v", err)
	}
	if len(report.Warnings) != 1 || report.Warnings[0].Code != "duplicate_tracker_project" {
		t.Fatalf("Warnings = %+v, want duplicate tracker project warning", report.Warnings)
	}
}

func TestValidateProjectIsolationRejectsOverlappingWorkspaceRoots(t *testing.T) {
	dir := t.TempDir()
	parentRoot := t.TempDir()
	childRoot := filepath.Join(parentRoot, "nested")
	writeTestWorkflow(t, filepath.Join(dir, "alpha", "WORKFLOW.md"), "workspace:\n  root: "+yamlPath(parentRoot)+"\n")
	writeTestWorkflow(t, filepath.Join(dir, "beta", "WORKFLOW.md"), "workspace:\n  root: "+yamlPath(childRoot)+"\n")
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeTestRegistry(t, registryPath, `
projects:
  - id: alpha
    workflow_path: alpha/WORKFLOW.md
  - id: beta
    workflow_path: beta/WORKFLOW.md
`)

	registry, err := LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}
	_, err = ValidateProjectIsolation(registry)
	if err == nil {
		t.Fatal("ValidateProjectIsolation succeeded, want overlapping workspace root error")
	}
	if !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("error = %q, want overlap message", err.Error())
	}
}

func TestValidateProjectIsolationAllowsOverlappingWorkspaceRootsWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	parentRoot := t.TempDir()
	childRoot := filepath.Join(parentRoot, "nested")
	writeTestWorkflow(t, filepath.Join(dir, "alpha", "WORKFLOW.md"), "workspace:\n  root: "+yamlPath(parentRoot)+"\n")
	writeTestWorkflow(t, filepath.Join(dir, "beta", "WORKFLOW.md"), "workspace:\n  root: "+yamlPath(childRoot)+"\n")
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeTestRegistry(t, registryPath, `
security:
  allow_workspace_overlap: true
projects:
  - id: alpha
    workflow_path: alpha/WORKFLOW.md
  - id: beta
    workflow_path: beta/WORKFLOW.md
`)

	registry, err := LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}
	if _, err := ValidateProjectIsolation(registry); err != nil {
		t.Fatalf("ValidateProjectIsolation returned error: %v", err)
	}
}

func TestValidateProjectIsolationRejectsWorkspaceUnderRegistryDir(t *testing.T) {
	dir := t.TempDir()
	writeTestWorkflow(t, filepath.Join(dir, "alpha", "WORKFLOW.md"), "workspace:\n  root: ./workspaces\n")
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeTestRegistry(t, registryPath, `
projects:
  - id: alpha
    workflow_path: alpha/WORKFLOW.md
`)

	registry, err := LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}
	_, err = ValidateProjectIsolation(registry)
	if err == nil {
		t.Fatal("ValidateProjectIsolation succeeded, want registry-dir workspace root error")
	}
	if !strings.Contains(err.Error(), "outside registry directory") {
		t.Fatalf("error = %q, want registry directory message", err.Error())
	}
}

func TestValidateProjectIsolationAllowsWorkspaceUnderRegistryDirWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	writeTestWorkflow(t, filepath.Join(dir, "alpha", "WORKFLOW.md"), "workspace:\n  root: ./workspaces\n")
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeTestRegistry(t, registryPath, `
security:
  allow_workspace_under_registry_dir: true
projects:
  - id: alpha
    workflow_path: alpha/WORKFLOW.md
`)

	registry, err := LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}
	if _, err := ValidateProjectIsolation(registry); err != nil {
		t.Fatalf("ValidateProjectIsolation returned error: %v", err)
	}
}

func TestValidateProjectIsolationRejectsRemoteServerBindByDefault(t *testing.T) {
	dir := t.TempDir()
	workspaceRoot := t.TempDir()
	writeTestWorkflow(t, filepath.Join(dir, "alpha", "WORKFLOW.md"), "workspace:\n  root: "+yamlPath(workspaceRoot)+"\n")
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeTestRegistry(t, registryPath, `
server:
  bind_address: 0.0.0.0
  port: 8080
projects:
  - id: alpha
    workflow_path: alpha/WORKFLOW.md
`)

	registry, err := LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}
	_, err = ValidateProjectIsolation(registry)
	if err == nil {
		t.Fatal("ValidateProjectIsolation succeeded, want remote bind error")
	}
	if !strings.Contains(err.Error(), "allow_remote_dashboard") {
		t.Fatalf("error = %q, want allow_remote_dashboard message", err.Error())
	}
}

func TestValidateProjectIsolationAllowsRemoteServerBindWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	workspaceRoot := t.TempDir()
	writeTestWorkflow(t, filepath.Join(dir, "alpha", "WORKFLOW.md"), "workspace:\n  root: "+yamlPath(workspaceRoot)+"\n")
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeTestRegistry(t, registryPath, `
server:
  bind_address: 0.0.0.0
  port: 8080
security:
  allow_remote_dashboard: true
projects:
  - id: alpha
    workflow_path: alpha/WORKFLOW.md
`)

	registry, err := LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}
	if _, err := ValidateProjectIsolation(registry); err != nil {
		t.Fatalf("ValidateProjectIsolation returned error: %v", err)
	}
}

func TestResolveProjectWorkflowAppliesGlobalAgentRuntimeDefaults(t *testing.T) {
	dir := t.TempDir()
	writeTestWorkflow(t, filepath.Join(dir, "alpha", "WORKFLOW.md"), "")
	writeTestWorkflow(t, filepath.Join(dir, "beta", "WORKFLOW.md"), "agent_runtime:\n  model: deepseek-coder\n")
	writeTestWorkflow(t, filepath.Join(dir, "gamma", "WORKFLOW.md"), "agent:\n  max_concurrent_agents: 7\n")
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeTestRegistry(t, registryPath, `
agent_runtime:
  provider: codex
  model: kimi-k2
  endpoint_url: https://openai-compatible.example/v1
concurrency:
  default_project_max_concurrent_agents: 4
projects:
  - id: alpha
    workflow_path: alpha/WORKFLOW.md
  - id: beta
    workflow_path: beta/WORKFLOW.md
    max_concurrent_agents: 2
  - id: gamma
    workflow_path: gamma/WORKFLOW.md
`)

	registry, err := LoadProjectRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadProjectRegistry returned error: %v", err)
	}

	alpha, _ := registry.ProjectByID("alpha")
	_, alphaCfg, err := ResolveProjectWorkflow(registry, *alpha)
	if err != nil {
		t.Fatalf("ResolveProjectWorkflow alpha returned error: %v", err)
	}
	if alphaCfg.AgentRuntime.Model != "kimi-k2" {
		t.Fatalf("alpha model = %q, want global kimi-k2", alphaCfg.AgentRuntime.Model)
	}
	if alphaCfg.AgentRuntime.EndpointURL != "https://openai-compatible.example/v1" {
		t.Fatalf("alpha endpoint = %q, want global endpoint", alphaCfg.AgentRuntime.EndpointURL)
	}
	if alphaCfg.Agent.MaxConcurrentAgents != 4 {
		t.Fatalf("alpha max concurrent = %d, want global default 4", alphaCfg.Agent.MaxConcurrentAgents)
	}

	beta, _ := registry.ProjectByID("beta")
	_, betaCfg, err := ResolveProjectWorkflow(registry, *beta)
	if err != nil {
		t.Fatalf("ResolveProjectWorkflow beta returned error: %v", err)
	}
	if betaCfg.AgentRuntime.Model != "deepseek-coder" {
		t.Fatalf("beta model = %q, want project override deepseek-coder", betaCfg.AgentRuntime.Model)
	}
	if betaCfg.AgentRuntime.EndpointURL != "https://openai-compatible.example/v1" {
		t.Fatalf("beta endpoint = %q, want global endpoint", betaCfg.AgentRuntime.EndpointURL)
	}
	if betaCfg.Agent.MaxConcurrentAgents != 2 {
		t.Fatalf("beta max concurrent = %d, want project cap 2", betaCfg.Agent.MaxConcurrentAgents)
	}

	gamma, _ := registry.ProjectByID("gamma")
	_, gammaCfg, err := ResolveProjectWorkflow(registry, *gamma)
	if err != nil {
		t.Fatalf("ResolveProjectWorkflow gamma returned error: %v", err)
	}
	if gammaCfg.Agent.MaxConcurrentAgents != 7 {
		t.Fatalf("gamma max concurrent = %d, want workflow override 7", gammaCfg.Agent.MaxConcurrentAgents)
	}
}

func TestLoadProjectRegistryRejectsDuplicateProjectIDs(t *testing.T) {
	dir := t.TempDir()
	writeTestWorkflow(t, filepath.Join(dir, "WORKFLOW.md"), "")
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeTestRegistry(t, registryPath, `
projects:
  - id: alpha
    workflow_path: WORKFLOW.md
  - id: Alpha
    workflow_path: WORKFLOW.md
`)

	_, err := LoadProjectRegistry(registryPath)
	if err == nil {
		t.Fatal("LoadProjectRegistry succeeded, want duplicate project id error")
	}
	if !strings.Contains(err.Error(), "duplicate project id") {
		t.Fatalf("error = %q, want duplicate project id", err.Error())
	}
}

func TestLoadProjectRegistryRejectsInvalidProjectConcurrency(t *testing.T) {
	dir := t.TempDir()
	writeTestWorkflow(t, filepath.Join(dir, "WORKFLOW.md"), "")
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeTestRegistry(t, registryPath, `
projects:
  - id: alpha
    workflow_path: WORKFLOW.md
    max_concurrent_agents: 0
`)

	_, err := LoadProjectRegistry(registryPath)
	if err == nil {
		t.Fatal("LoadProjectRegistry succeeded, want invalid project concurrency error")
	}
	if !strings.Contains(err.Error(), "max_concurrent_agents") {
		t.Fatalf("error = %q, want max_concurrent_agents", err.Error())
	}
}

func TestLoadProjectRegistryRejectsMissingWorkflow(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "simphony.yaml")
	writeTestRegistry(t, registryPath, `
projects:
  - id: alpha
    workflow_path: missing/WORKFLOW.md
`)

	_, err := LoadProjectRegistry(registryPath)
	if err == nil {
		t.Fatal("LoadProjectRegistry succeeded, want missing workflow error")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error = %q, want missing workflow path context", err.Error())
	}
}
