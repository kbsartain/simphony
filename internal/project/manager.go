package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/kbsartain/simphony/internal/config"
	"github.com/kbsartain/simphony/internal/orchestrator"
	"github.com/kbsartain/simphony/pkg/api"
)

var (
	// ErrSettingsLoad indicates a project workflow could not be loaded.
	ErrSettingsLoad = errors.New("settings load error")
	// ErrSettingsValidation indicates proposed project settings are invalid.
	ErrSettingsValidation = errors.New("settings validation error")
	// ErrSettingsApply indicates valid settings could not be applied to runtime dependencies.
	ErrSettingsApply = errors.New("settings apply error")
	// ErrSettingsSave indicates settings could not be written to WORKFLOW.md.
	ErrSettingsSave = errors.New("settings save error")
	// ErrTrackerValidation indicates Linear validation failed after settings were resolved.
	ErrTrackerValidation = errors.New("tracker validation error")
)

// WorkflowSettings contains editable and resolved workflow settings for one project.
type WorkflowSettings struct {
	WorkflowPath    string
	Definition      *api.WorkflowDefinition
	ResolvedConfig  *api.WorkflowConfig
	ValidationError error
}

// ManagedRuntime is a project runtime controlled by Manager.
type ManagedRuntime interface {
	ID() string
	Project() config.RegistryProject
	Start(ctx context.Context) error
	Stop()
}

// ObservableRuntime exposes runtime state for the aggregate API.
type ObservableRuntime interface {
	ManagedRuntime
	Snapshot() (api.StateSnapshot, bool)
	IssueDetail(identifier string) (api.IssueDetailResponse, bool)
	Refresh() (api.RefreshResponse, bool)
	SetProjectPaused(paused bool) (api.ControlState, bool)
	SetStagePaused(stage string, paused bool) (api.ControlState, bool, error)
	WorkflowSettings() (WorkflowSettings, error)
	UpdateWorkflowSettings(req api.SettingsUpdateRequest) (WorkflowSettings, error)
	ValidateTrackerSettings(req api.SettingsUpdateRequest) (api.SettingsValidationResponse, error)
}

// WatchableRuntime exposes workflow watcher health for project summaries.
type WatchableRuntime interface {
	WatcherStatus() (running bool, lastError string)
}

// RuntimeFactory builds a runtime for a registry project.
type RuntimeFactory func(registry *config.ProjectRegistry, project config.RegistryProject, limiter orchestrator.DispatchLimiter) ManagedRuntime

// StartReport summarizes a multi-project startup attempt.
type StartReport struct {
	Started []RuntimeSummary
	Failed  map[string]error
}

// RuntimeSummary is a lightweight project runtime view.
type RuntimeSummary struct {
	ID                     string
	Name                   string
	WorkflowPath           string
	Enabled                bool
	StartPaused            bool
	Running                bool
	LastError              string
	Health                 api.ProjectHealth
	MaxConcurrentAgents    int
	WorkflowWatcherRunning bool
	WorkflowWatcherError   string
}

// Manager owns a set of isolated project runtimes.
type Manager struct {
	registry *config.ProjectRegistry
	factory  RuntimeFactory
	limiter  *SlotLimiter

	mu       sync.Mutex
	runtimes map[string]ManagedRuntime
	failures map[string]error
}

// NewManager creates a project manager that builds real project runtimes.
func NewManager(registry *config.ProjectRegistry) *Manager {
	return NewManagerWithFactory(registry, NewRuntime)
}

// NewManagerWithFactory creates a project manager with injectable runtimes for tests.
func NewManagerWithFactory(registry *config.ProjectRegistry, factory RuntimeFactory) *Manager {
	var limiter *SlotLimiter
	if registry != nil && registry.Concurrency.MaxConcurrentAgents > 0 {
		slotLimiter, err := NewSlotLimiter(registry.Concurrency.MaxConcurrentAgents)
		if err == nil {
			limiter = slotLimiter
		}
	}
	return &Manager{
		registry: registry,
		factory:  factory,
		limiter:  limiter,
		runtimes: make(map[string]ManagedRuntime),
		failures: make(map[string]error),
	}
}

// Start starts every enabled project runtime. One project failure does not stop siblings.
func (m *Manager) Start(ctx context.Context) StartReport {
	report := StartReport{Failed: make(map[string]error)}
	if m == nil || m.registry == nil {
		report.Failed[""] = fmt.Errorf("project registry is nil")
		return report
	}

	for _, project := range m.registry.EnabledProjects() {
		m.startProject(ctx, project, &report)
	}

	return report
}

// StartProject starts one enabled project runtime by ID.
func (m *Manager) StartProject(ctx context.Context, id string) StartReport {
	report := StartReport{Failed: make(map[string]error)}
	if m == nil || m.registry == nil {
		report.Failed[""] = fmt.Errorf("project registry is nil")
		return report
	}

	project, ok := m.registry.ProjectByID(id)
	if !ok {
		report.Failed[id] = fmt.Errorf("project %q is not configured", id)
		return report
	}
	if !project.Enabled {
		report.Failed[project.ID] = fmt.Errorf("project %q is disabled", project.ID)
		return report
	}
	m.startProject(ctx, *project, &report)
	return report
}

func (m *Manager) startProject(ctx context.Context, project config.RegistryProject, report *StartReport) {
	var limiter orchestrator.DispatchLimiter
	if m.limiter != nil {
		limiter = m.limiter
	}
	runtime := m.factory(m.registry, project, limiter)
	if err := runtime.Start(ctx); err != nil {
		log.Printf("project_id=%s project_name=%q action=project_start status=failed error=%v", project.ID, project.Name, err)
		report.Failed[project.ID] = err
		m.mu.Lock()
		m.failures[project.ID] = err
		m.mu.Unlock()
		return
	}

	log.Printf("project_id=%s project_name=%q action=project_start status=started workflow=%q", project.ID, project.Name, project.WorkflowPath)
	m.mu.Lock()
	m.runtimes[project.ID] = runtime
	delete(m.failures, project.ID)
	m.mu.Unlock()
	report.Started = append(report.Started, summaryFromRuntime(runtime, true, ""))
}

// Stop stops all started project runtimes.
func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	runtimes := make([]ManagedRuntime, 0, len(m.runtimes))
	for _, runtime := range m.runtimes {
		runtimes = append(runtimes, runtime)
	}
	m.mu.Unlock()

	for _, runtime := range runtimes {
		project := runtime.Project()
		log.Printf("project_id=%s project_name=%q action=project_stop status=stopping", project.ID, project.Name)
		runtime.Stop()
		log.Printf("project_id=%s project_name=%q action=project_stop status=stopped", project.ID, project.Name)
	}
}

// Summaries returns a project-level runtime view.
func (m *Manager) Summaries() []RuntimeSummary {
	if m == nil || m.registry == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	summaries := make([]RuntimeSummary, 0, len(m.registry.Projects))
	for _, project := range m.registry.Projects {
		if runtime, ok := m.runtimes[project.ID]; ok {
			summaries = append(summaries, summaryFromRuntime(runtime, true, ""))
			continue
		}
		lastError := ""
		if err, ok := m.failures[project.ID]; ok && err != nil {
			lastError = err.Error()
		}
		summaries = append(summaries, RuntimeSummary{
			ID:                     project.ID,
			Name:                   project.Name,
			WorkflowPath:           project.WorkflowPath,
			Enabled:                project.Enabled,
			StartPaused:            project.StartPaused,
			Running:                false,
			LastError:              lastError,
			Health:                 startupHealth(project.Enabled, lastError),
			MaxConcurrentAgents:    project.MaxConcurrentAgents,
			WorkflowWatcherRunning: false,
		})
	}
	return summaries
}

// Runtime returns a running observable runtime by project ID.
func (m *Manager) Runtime(id string) (ObservableRuntime, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime, ok := m.runtimes[id]
	if !ok {
		return nil, false
	}
	observable, ok := runtime.(ObservableRuntime)
	return observable, ok
}

// Summary returns one project summary by project ID.
func (m *Manager) Summary(id string) (RuntimeSummary, bool) {
	if m == nil {
		return RuntimeSummary{}, false
	}
	id = strings.ToLower(strings.TrimSpace(id))
	for _, summary := range m.Summaries() {
		if strings.ToLower(summary.ID) == id {
			return summary, true
		}
	}
	return RuntimeSummary{}, false
}

// Concurrency returns shared supervisor slot usage for multi-project mode.
func (m *Manager) Concurrency() api.SupervisorConcurrency {
	if m == nil || m.limiter == nil {
		return api.SupervisorConcurrency{}
	}
	used := m.limiter.Used()
	capacity := m.limiter.Capacity()
	available := capacity - used
	if available < 0 {
		available = 0
	}
	return api.SupervisorConcurrency{
		MaxConcurrentAgents: capacity,
		UsedAgents:          used,
		AvailableAgents:     available,
	}
}

// Registry returns the active project registry.
func (m *Manager) Registry() *config.ProjectRegistry {
	if m == nil {
		return nil
	}
	return m.registry
}

func summaryFromRuntime(runtime ManagedRuntime, running bool, lastError string) RuntimeSummary {
	project := runtime.Project()
	watcherRunning := false
	watcherError := ""
	if watchable, ok := runtime.(WatchableRuntime); ok {
		watcherRunning, watcherError = watchable.WatcherStatus()
	}
	health := api.ProjectHealth{Status: "unknown", Summary: "Project health has not been checked"}
	if observable, ok := runtime.(ObservableRuntime); ok {
		if snapshot, snapshotOK := observable.Snapshot(); snapshotOK {
			health = snapshot.Health
		}
	}
	return RuntimeSummary{
		ID:                     project.ID,
		Name:                   project.Name,
		WorkflowPath:           project.WorkflowPath,
		Enabled:                project.Enabled,
		StartPaused:            project.StartPaused,
		Running:                running,
		LastError:              lastError,
		Health:                 health,
		MaxConcurrentAgents:    project.MaxConcurrentAgents,
		WorkflowWatcherRunning: watcherRunning,
		WorkflowWatcherError:   watcherError,
	}
}

func startupHealth(enabled bool, lastError string) api.ProjectHealth {
	if !enabled {
		return api.ProjectHealth{Status: "disabled", Summary: "Project is disabled"}
	}
	if strings.TrimSpace(lastError) != "" {
		return api.ProjectHealth{
			Status:  "blocked",
			Summary: "Project runtime failed to start",
			Issues: []api.HealthIssue{{
				Code:     "project_start_failed",
				Severity: "blocker",
				Message:  "Project runtime failed to start",
				Detail:   lastError,
			}},
		}
	}
	return api.ProjectHealth{Status: "unknown", Summary: "Project runtime is not running"}
}
