package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/kbsartain/simphony/pkg/api"
	"gopkg.in/yaml.v3"
)

var projectIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ProjectRegistry is the global multi-project configuration.
type ProjectRegistry struct {
	SourcePath           string
	Server               *RegistryServerConfig
	AgentRuntime         *api.AgentRuntimeConfig
	AgentRuntimeDefaults map[string]interface{}
	Catalog              api.ProviderCatalog
	Concurrency          RegistryConcurrencyConfig
	Security             RegistrySecurityConfig
	Projects             []RegistryProject
}

// RegistryServerConfig configures the future supervisor HTTP server.
type RegistryServerConfig struct {
	BindAddress      string
	Port             int
	DashboardEnabled bool
	APIPrefix        string
}

// RegistryConcurrencyConfig configures future cross-project agent limits.
type RegistryConcurrencyConfig struct {
	MaxConcurrentAgents               int
	DefaultProjectMaxConcurrentAgents int
}

// RegistrySecurityConfig controls multi-project isolation validation.
type RegistrySecurityConfig struct {
	AllowWorkspaceOverlap          bool
	AllowWorkspaceUnderRegistryDir bool
	AllowRemoteDashboard           bool
}

// RegistryProject identifies one project runtime in a ProjectRegistry.
type RegistryProject struct {
	ID                  string
	Name                string
	WorkflowPath        string
	Enabled             bool
	MaxConcurrentAgents int
}

// RegistryWarning is a non-fatal registry validation warning.
type RegistryWarning struct {
	Code       string
	Message    string
	ProjectIDs []string
}

// RegistryValidationReport summarizes resolved workflow validation.
type RegistryValidationReport struct {
	Warnings []RegistryWarning
}

type registryResolvedProject struct {
	project RegistryProject
	cfg     *api.WorkflowConfig
}

// LoadProjectRegistry reads and validates a global multi-project registry file.
func LoadProjectRegistry(path string) (*ProjectRegistry, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("%s: resolve registry path: %w", api.ErrProjectRegistryParseError, err)
	}
	path = absPath

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s: %w", api.ErrMissingProjectRegistry, err)
		}
		return nil, fmt.Errorf("%s: %w", api.ErrProjectRegistryParseError, err)
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", api.ErrProjectRegistryParseError, err)
	}
	if raw == nil {
		raw = make(map[string]interface{})
	}

	registry := &ProjectRegistry{SourcePath: path}
	if err := resolveRegistryServer(getSubMap(raw, "server"), registry); err != nil {
		return nil, err
	}
	if err := resolveRegistryConcurrency(getSubMap(raw, "concurrency"), registry); err != nil {
		return nil, err
	}
	if err := resolveRegistrySecurity(getSubMap(raw, "security"), registry); err != nil {
		return nil, err
	}
	if err := resolveRegistryAgentRuntime(getSubMap(raw, "agent_runtime"), registry); err != nil {
		return nil, err
	}
	if err := resolveRegistryCatalog(getSubMap(raw, "providers"), registry); err != nil {
		return nil, err
	}
	if err := resolveRegistryProjects(raw, registry); err != nil {
		return nil, err
	}

	return registry, nil
}

// ProjectByID returns the project definition for id.
func (r *ProjectRegistry) ProjectByID(id string) (*RegistryProject, bool) {
	if r == nil {
		return nil, false
	}
	for i := range r.Projects {
		if strings.EqualFold(r.Projects[i].ID, id) {
			return &r.Projects[i], true
		}
	}
	return nil, false
}

// EnabledProjects returns the projects that should be started by default.
func (r *ProjectRegistry) EnabledProjects() []RegistryProject {
	if r == nil {
		return nil
	}
	projects := make([]RegistryProject, 0, len(r.Projects))
	for _, project := range r.Projects {
		if project.Enabled {
			projects = append(projects, project)
		}
	}
	return projects
}

// ResolveProjectWorkflow loads a project's WORKFLOW.md and applies global defaults.
func ResolveProjectWorkflow(registry *ProjectRegistry, project RegistryProject) (*api.WorkflowDefinition, *api.WorkflowConfig, error) {
	if registry == nil {
		return nil, nil, fmt.Errorf("%s: project registry is nil", api.ErrProjectRegistryParseError)
	}
	def, err := LoadWorkflow(project.WorkflowPath)
	if err != nil {
		return nil, nil, fmt.Errorf("project %q: %w", project.ID, err)
	}
	cfg, err := ResolveProjectDefinition(registry, project, def)
	if err != nil {
		return nil, nil, fmt.Errorf("project %q: %w", project.ID, err)
	}
	return def, cfg, nil
}

// ResolveProjectDefinition resolves a parsed project workflow with registry defaults.
func ResolveProjectDefinition(registry *ProjectRegistry, project RegistryProject, def *api.WorkflowDefinition) (*api.WorkflowConfig, error) {
	if registry == nil {
		return nil, fmt.Errorf("%s: project registry is nil", api.ErrProjectRegistryParseError)
	}
	def = applyRegistryDefaults(registry, project, def)
	cfg, err := ResolveConfig(def, filepath.Dir(project.WorkflowPath))
	if err != nil {
		return nil, err
	}
	ApplyProviderCatalog(cfg, registry.Catalog)
	return cfg, nil
}

// ValidateProjectIsolation resolves enabled project workflows and checks cross-project safety.
func ValidateProjectIsolation(registry *ProjectRegistry) (RegistryValidationReport, error) {
	report := RegistryValidationReport{}
	if registry == nil {
		return report, fmt.Errorf("%s: project registry is nil", api.ErrProjectRegistryParseError)
	}

	if registry.Server != nil && !registry.Security.AllowRemoteDashboard && !isLocalBindAddress(registry.Server.BindAddress) {
		return report, fmt.Errorf("%s: server.bind_address %q exposes the dashboard/API outside localhost; set security.allow_remote_dashboard=true to allow this intentionally", api.ErrProjectRegistryParseError, registry.Server.BindAddress)
	}

	var resolved []registryResolvedProject
	for _, project := range registry.EnabledProjects() {
		_, cfg, err := ResolveProjectWorkflow(registry, project)
		if err != nil {
			return report, fmt.Errorf("validate project workflow: %w", err)
		}
		resolved = append(resolved, registryResolvedProject{project: project, cfg: cfg})
	}

	if !registry.Security.AllowWorkspaceUnderRegistryDir {
		registryDir := filepath.Dir(registry.SourcePath)
		for _, item := range resolved {
			if isPathInsideOrSame(item.cfg.Workspace.Root, registryDir) {
				return report, fmt.Errorf("%s: project %q workspace.root %q must be outside registry directory %q; set security.allow_workspace_under_registry_dir=true to allow this intentionally", api.ErrProjectRegistryParseError, item.project.ID, item.cfg.Workspace.Root, registryDir)
			}
		}
	}

	if !registry.Security.AllowWorkspaceOverlap {
		for i := 0; i < len(resolved); i++ {
			for j := i + 1; j < len(resolved); j++ {
				left := resolved[i]
				right := resolved[j]
				if pathsOverlap(left.cfg.Workspace.Root, right.cfg.Workspace.Root) {
					return report, fmt.Errorf("%s: project %q workspace.root %q overlaps project %q workspace.root %q; set security.allow_workspace_overlap=true to allow this intentionally", api.ErrProjectRegistryParseError, left.project.ID, left.cfg.Workspace.Root, right.project.ID, right.cfg.Workspace.Root)
				}
			}
		}
	}

	report.Warnings = append(report.Warnings, duplicateTrackerProjectWarnings(resolved)...)
	report.Warnings = append(report.Warnings, catalogWarnings(resolved, registry.Catalog)...)
	return report, nil
}

func resolveRegistryServer(m map[string]interface{}, registry *ProjectRegistry) error {
	if m == nil {
		return nil
	}
	server := &RegistryServerConfig{
		BindAddress:      "127.0.0.1",
		Port:             8080,
		DashboardEnabled: true,
		APIPrefix:        "/api/v1",
	}
	if v, ok := getString(m, "bind_address"); ok && strings.TrimSpace(v) != "" {
		server.BindAddress = strings.TrimSpace(v)
	}
	if v, ok := getInt(m, "port"); ok {
		server.Port = v
	}
	if server.Port <= 0 || server.Port > 65535 {
		return fmt.Errorf("%s: server.port must be between 1 and 65535, got %d", api.ErrProjectRegistryParseError, server.Port)
	}
	if v, ok := getBool(m, "dashboard_enabled"); ok {
		server.DashboardEnabled = v
	}
	if v, ok := getString(m, "api_prefix"); ok && strings.TrimSpace(v) != "" {
		server.APIPrefix = strings.TrimRight(strings.TrimSpace(v), "/")
		if server.APIPrefix == "" {
			server.APIPrefix = "/"
		}
	}
	registry.Server = server
	return nil
}

func resolveRegistryConcurrency(m map[string]interface{}, registry *ProjectRegistry) error {
	if m == nil {
		return nil
	}
	if v, ok := getInt(m, "max_concurrent_agents"); ok {
		if v <= 0 {
			return fmt.Errorf("%s: concurrency.max_concurrent_agents must be positive, got %d", api.ErrProjectRegistryParseError, v)
		}
		registry.Concurrency.MaxConcurrentAgents = v
	}
	if v, ok := getInt(m, "default_project_max_concurrent_agents"); ok {
		if v <= 0 {
			return fmt.Errorf("%s: concurrency.default_project_max_concurrent_agents must be positive, got %d", api.ErrProjectRegistryParseError, v)
		}
		registry.Concurrency.DefaultProjectMaxConcurrentAgents = v
	}
	return nil
}

func resolveRegistrySecurity(m map[string]interface{}, registry *ProjectRegistry) error {
	if m == nil {
		return nil
	}
	if v, ok := getBool(m, "allow_workspace_overlap"); ok {
		registry.Security.AllowWorkspaceOverlap = v
	}
	if v, ok := getBool(m, "allow_workspace_under_registry_dir"); ok {
		registry.Security.AllowWorkspaceUnderRegistryDir = v
	}
	if v, ok := getBool(m, "allow_remote_dashboard"); ok {
		registry.Security.AllowRemoteDashboard = v
	}
	return nil
}

func resolveRegistryAgentRuntime(m map[string]interface{}, registry *ProjectRegistry) error {
	if m == nil {
		return nil
	}
	defaults := cloneMap(m)
	provider := "codex"
	if v, ok := getString(defaults, "provider"); ok && strings.TrimSpace(v) != "" {
		provider = strings.ToLower(strings.TrimSpace(v))
	}
	switch provider {
	case "codex", "claude":
	default:
		return fmt.Errorf("%s: agent_runtime.provider must be codex or claude, got %q", api.ErrProjectRegistryParseError, provider)
	}

	runtime := defaultRuntimeConfig(provider)
	if err := applyRuntimeCommon(defaults, &runtime, "agent_runtime"); err != nil {
		return fmt.Errorf("%s: %w", api.ErrProjectRegistryParseError, err)
	}
	if provider == "claude" {
		if v, ok := getString(defaults, "permission_mode"); ok && strings.TrimSpace(v) != "" {
			runtime.PermissionMode = strings.TrimSpace(v)
		}
		if v, ok := getStringSlice(defaults, "allowed_tools"); ok {
			runtime.AllowedTools = v
		}
		if v, ok := getStringSlice(defaults, "disallowed_tools"); ok {
			runtime.DisallowedTools = v
		}
		if v, ok := getStringSlice(defaults, "setting_sources"); ok {
			runtime.SettingSources = v
		}
	}
	stageOverrides, err := resolveAgentStageOverrides(defaults, "agent_runtime")
	if err != nil {
		return fmt.Errorf("%s: %w", api.ErrProjectRegistryParseError, err)
	}
	if stageOverrides != nil {
		runtime.StageOverrides = stageOverrides
	}

	registry.AgentRuntime = &runtime
	registry.AgentRuntimeDefaults = defaults
	return nil
}

func resolveRegistryProjects(raw map[string]interface{}, registry *ProjectRegistry) error {
	rawProjects, ok := raw["projects"]
	if !ok {
		return fmt.Errorf("%s: projects must contain at least one project", api.ErrProjectRegistryParseError)
	}
	items, ok := rawProjects.([]interface{})
	if !ok {
		return fmt.Errorf("%s: projects must be a list", api.ErrProjectRegistryParseError)
	}
	if len(items) == 0 {
		return fmt.Errorf("%s: projects must contain at least one project", api.ErrProjectRegistryParseError)
	}

	registryDir := filepath.Dir(registry.SourcePath)
	seen := make(map[string]struct{}, len(items))
	projects := make([]RegistryProject, 0, len(items))
	for i, item := range items {
		projectMap, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s: projects[%d] must be a map", api.ErrProjectRegistryParseError, i)
		}

		id, ok := getString(projectMap, "id")
		id = strings.TrimSpace(id)
		if !ok || id == "" {
			return fmt.Errorf("%s: projects[%d].id is required", api.ErrProjectRegistryParseError, i)
		}
		if !projectIDPattern.MatchString(id) {
			return fmt.Errorf("%s: projects[%d].id must start with a letter or number and contain only letters, numbers, dots, underscores, or hyphens", api.ErrProjectRegistryParseError, i)
		}
		idKey := strings.ToLower(id)
		if _, exists := seen[idKey]; exists {
			return fmt.Errorf("%s: duplicate project id %q", api.ErrProjectRegistryParseError, id)
		}
		seen[idKey] = struct{}{}

		name := id
		if v, ok := getString(projectMap, "name"); ok && strings.TrimSpace(v) != "" {
			name = strings.TrimSpace(v)
		}

		workflowPathRaw, ok := getString(projectMap, "workflow_path")
		if !ok || strings.TrimSpace(workflowPathRaw) == "" {
			return fmt.Errorf("%s: projects[%d].workflow_path is required", api.ErrProjectRegistryParseError, i)
		}
		workflowPath, err := resolvePath(strings.TrimSpace(workflowPathRaw), registryDir)
		if err != nil {
			return fmt.Errorf("%s: projects[%d].workflow_path %w", api.ErrProjectRegistryParseError, i, err)
		}
		info, err := os.Stat(workflowPath)
		if err != nil {
			return fmt.Errorf("%s: projects[%d].workflow_path %q: %w", api.ErrProjectRegistryParseError, i, workflowPath, err)
		}
		if info.IsDir() {
			return fmt.Errorf("%s: projects[%d].workflow_path %q is a directory", api.ErrProjectRegistryParseError, i, workflowPath)
		}

		enabled := true
		if v, ok := getBool(projectMap, "enabled"); ok {
			enabled = v
		}

		maxConcurrentAgents := 0
		if v, ok := getInt(projectMap, "max_concurrent_agents"); ok {
			if v <= 0 {
				return fmt.Errorf("%s: projects[%d].max_concurrent_agents must be positive, got %d", api.ErrProjectRegistryParseError, i, v)
			}
			maxConcurrentAgents = v
		}

		projects = append(projects, RegistryProject{
			ID:                  id,
			Name:                name,
			WorkflowPath:        workflowPath,
			Enabled:             enabled,
			MaxConcurrentAgents: maxConcurrentAgents,
		})
	}

	registry.Projects = projects
	return nil
}

func applyRegistryDefaults(registry *ProjectRegistry, project RegistryProject, def *api.WorkflowDefinition) *api.WorkflowDefinition {
	if registry == nil || def == nil {
		return def
	}

	projectMaxConcurrentAgents := project.MaxConcurrentAgents
	defaultMaxConcurrentAgents := registry.Concurrency.DefaultProjectMaxConcurrentAgents
	hasAgentRuntimeDefaults := len(registry.AgentRuntimeDefaults) > 0
	if !hasAgentRuntimeDefaults && projectMaxConcurrentAgents <= 0 && defaultMaxConcurrentAgents <= 0 {
		return def
	}

	configMap := cloneMap(def.Config)
	if hasAgentRuntimeDefaults {
		mergedRuntime := cloneMap(registry.AgentRuntimeDefaults)
		if projectRuntime := getSubMap(configMap, "agent_runtime"); projectRuntime != nil {
			for k, v := range projectRuntime {
				mergedRuntime[k] = cloneValue(v)
			}
		}
		configMap["agent_runtime"] = mergedRuntime
	}

	if projectMaxConcurrentAgents > 0 || defaultMaxConcurrentAgents > 0 {
		agentMap := getSubMap(configMap, "agent")
		if agentMap == nil {
			agentMap = make(map[string]interface{})
		}
		if projectMaxConcurrentAgents > 0 {
			agentMap["max_concurrent_agents"] = projectMaxConcurrentAgents
		} else if _, ok := getInt(agentMap, "max_concurrent_agents"); !ok {
			agentMap["max_concurrent_agents"] = defaultMaxConcurrentAgents
		}
		configMap["agent"] = agentMap
	}

	return &api.WorkflowDefinition{
		Config:         configMap,
		PromptTemplate: def.PromptTemplate,
	}
}

func cloneMap(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return make(map[string]interface{})
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v interface{}) interface{} {
	switch typed := v.(type) {
	case map[string]interface{}:
		return cloneMap(typed)
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, item := range typed {
			out[i] = cloneValue(item)
		}
		return out
	default:
		return typed
	}
}

func duplicateTrackerProjectWarnings(projects []registryResolvedProject) []RegistryWarning {
	type seenProject struct {
		id string
	}
	seen := make(map[string][]seenProject)
	for _, item := range projects {
		if item.cfg == nil || !strings.EqualFold(item.cfg.Tracker.Kind, "linear") {
			continue
		}
		slug := strings.ToLower(strings.TrimSpace(item.cfg.Tracker.ProjectSlug))
		if slug == "" {
			continue
		}
		endpoint := strings.ToLower(strings.TrimSpace(item.cfg.Tracker.Endpoint))
		key := endpoint + "|" + slug
		seen[key] = append(seen[key], seenProject{id: item.project.ID})
	}

	var warnings []RegistryWarning
	for _, projectsForSlug := range seen {
		if len(projectsForSlug) < 2 {
			continue
		}
		ids := make([]string, 0, len(projectsForSlug))
		for _, project := range projectsForSlug {
			ids = append(ids, project.id)
		}
		warnings = append(warnings, RegistryWarning{
			Code:       "duplicate_tracker_project",
			Message:    fmt.Sprintf("multiple enabled projects reference the same Linear project slug: %s", strings.Join(ids, ", ")),
			ProjectIDs: ids,
		})
	}
	return warnings
}

func pathsOverlap(left string, right string) bool {
	return isPathInsideOrSame(left, right) || isPathInsideOrSame(right, left)
}

func isPathInsideOrSame(child string, parent string) bool {
	child = cleanComparablePath(child)
	parent = cleanComparablePath(parent)
	if child == "" || parent == "" {
		return false
	}
	if child == parent {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cleanComparablePath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	clean := filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		clean = strings.ToLower(clean)
	}
	return clean
}

func isLocalBindAddress(address string) bool {
	address = strings.TrimSpace(address)
	if address == "" || strings.EqualFold(address, "localhost") {
		return true
	}
	address = strings.TrimPrefix(strings.TrimSuffix(address, "]"), "[")
	ip := net.ParseIP(address)
	return ip != nil && ip.IsLoopback()
}
