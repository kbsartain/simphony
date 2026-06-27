package project

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/kbsartain/simphony/internal/agent"
	"github.com/kbsartain/simphony/internal/config"
	"github.com/kbsartain/simphony/internal/orchestrator"
	"github.com/kbsartain/simphony/internal/tracker"
	"github.com/kbsartain/simphony/internal/workspace"
	"github.com/kbsartain/simphony/pkg/api"
)

type workflowWatcher interface {
	Close() error
}

// Runtime owns one isolated project orchestrator and its workflow watcher.
type Runtime struct {
	registry *config.ProjectRegistry
	project  config.RegistryProject

	mu      sync.Mutex
	def     *api.WorkflowDefinition
	cfg     *api.WorkflowConfig
	runner  *agent.Runner
	orch    *orchestrator.Orchestrator
	limiter orchestrator.DispatchLimiter
	watcher workflowWatcher
	started bool
}

// NewRuntime creates a project runtime.
func NewRuntime(registry *config.ProjectRegistry, project config.RegistryProject, limiter orchestrator.DispatchLimiter) ManagedRuntime {
	return &Runtime{
		registry: registry,
		project:  project,
		limiter:  limiter,
	}
}

// ID returns the stable project ID.
func (r *Runtime) ID() string {
	return r.project.ID
}

// Project returns the project registry entry.
func (r *Runtime) Project() config.RegistryProject {
	return r.project
}

// Start initializes dependencies, starts the orchestrator, and watches the workflow file.
func (r *Runtime) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return nil
	}
	def, cfg, runner, orch, err := r.build()
	if err != nil {
		return err
	}

	r.def = def
	r.cfg = cfg
	r.runner = runner
	r.orch = orch
	r.orch.Start()
	r.started = true

	watcher, err := config.WatchWorkflow(r.project.WorkflowPath, func() {
		log.Printf("project_id=%s project_name=%q action=workflow_change_detected workflow=%q", r.project.ID, r.project.Name, r.project.WorkflowPath)
		if err := r.Reload(); err != nil {
			log.Printf("project_id=%s project_name=%q action=workflow_reload status=failed error=%v", r.project.ID, r.project.Name, err)
			return
		}
		log.Printf("project_id=%s project_name=%q action=workflow_reload status=success", r.project.ID, r.project.Name)
	})
	if err != nil {
		log.Printf("project_id=%s project_name=%q action=workflow_watch status=failed error=%v", r.project.ID, r.project.Name, err)
	} else {
		r.watcher = watcher
	}

	go func() {
		<-ctx.Done()
		r.Stop()
	}()

	return nil
}

// Stop closes the workflow watcher and stops the orchestrator.
func (r *Runtime) Stop() {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return
	}
	watcher := r.watcher
	orch := r.orch
	r.watcher = nil
	r.started = false
	r.mu.Unlock()

	if watcher != nil {
		_ = watcher.Close()
	}
	if orch != nil {
		orch.Stop()
	}
}

// Reload applies a changed project workflow to future orchestration work.
func (r *Runtime) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started || r.orch == nil || r.runner == nil {
		return fmt.Errorf("project runtime is not started")
	}

	def, cfg, err := config.ResolveProjectWorkflow(r.registry, r.project)
	if err != nil {
		return err
	}
	trackerClient, err := tracker.NewLinearClient(cfg.Tracker)
	if err != nil {
		return fmt.Errorf("initialize tracker: %w", err)
	}
	wsMgr, err := workspace.NewManagerWithConfig(cfg.Workspace)
	if err != nil {
		return fmt.Errorf("initialize workspace manager: %w", err)
	}

	r.orch.UpdateRuntime(cfg, trackerClient, wsMgr)
	r.runner.SetPromptTemplate(def.PromptTemplate)
	r.def = def
	r.cfg = cfg
	return nil
}

// Snapshot returns this project's orchestrator state when the runtime is running.
func (r *Runtime) Snapshot() (api.StateSnapshot, bool) {
	r.mu.Lock()
	orch := r.orch
	started := r.started
	r.mu.Unlock()
	if !started || orch == nil {
		return api.StateSnapshot{}, false
	}
	return orch.Snapshot(), true
}

// IssueDetail returns per-issue runtime details for this project.
func (r *Runtime) IssueDetail(identifier string) (api.IssueDetailResponse, bool) {
	r.mu.Lock()
	orch := r.orch
	started := r.started
	r.mu.Unlock()
	if !started || orch == nil {
		return api.IssueDetailResponse{}, false
	}
	return orch.IssueDetail(identifier)
}

// Refresh triggers an immediate poll tick for this project.
func (r *Runtime) Refresh() (api.RefreshResponse, bool) {
	r.mu.Lock()
	orch := r.orch
	started := r.started
	r.mu.Unlock()
	if !started || orch == nil {
		return api.RefreshResponse{}, false
	}
	return orch.Refresh(), true
}

// WorkflowSettings returns editable and resolved settings for this project.
func (r *Runtime) WorkflowSettings() (WorkflowSettings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	def, err := config.LoadWorkflow(r.project.WorkflowPath)
	if err != nil {
		return WorkflowSettings{}, fmt.Errorf("%w: %v", ErrSettingsLoad, err)
	}
	cfg, err := config.ResolveProjectDefinition(r.registry, r.project, def)
	settings := WorkflowSettings{
		WorkflowPath:    r.project.WorkflowPath,
		Definition:      def,
		ResolvedConfig:  cfg,
		ValidationError: err,
	}
	return settings, nil
}

// UpdateWorkflowSettings validates, applies, and saves project workflow settings.
func (r *Runtime) UpdateWorkflowSettings(req api.SettingsUpdateRequest) (WorkflowSettings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	current, err := config.LoadWorkflow(r.project.WorkflowPath)
	if err != nil {
		return WorkflowSettings{}, fmt.Errorf("%w: %v", ErrSettingsLoad, err)
	}

	promptTemplate := current.PromptTemplate
	if req.PromptTemplate != nil {
		promptTemplate = *req.PromptTemplate
	}
	def := &api.WorkflowDefinition{
		Config:         cloneConfigMap(req.Config),
		PromptTemplate: promptTemplate,
	}
	cfg, err := config.ResolveProjectDefinition(r.registry, r.project, def)
	if err != nil {
		return WorkflowSettings{}, fmt.Errorf("%w: %v", ErrSettingsValidation, err)
	}

	currentCfg, currentCfgErr := config.ResolveProjectDefinition(r.registry, r.project, current)
	if err := r.applyResolvedWorkflowLocked(def, cfg); err != nil {
		return WorkflowSettings{}, fmt.Errorf("%w: %v", ErrSettingsApply, err)
	}
	if err := config.SaveWorkflow(r.project.WorkflowPath, def); err != nil {
		if currentCfgErr == nil {
			if rollbackErr := r.applyResolvedWorkflowLocked(current, currentCfg); rollbackErr != nil {
				err = fmt.Errorf("%w; runtime rollback failed: %v", err, rollbackErr)
			}
		}
		return WorkflowSettings{}, fmt.Errorf("%w: %v", ErrSettingsSave, err)
	}

	return WorkflowSettings{
		WorkflowPath:   r.project.WorkflowPath,
		Definition:     def,
		ResolvedConfig: cfg,
	}, nil
}

// ValidateTrackerSettings validates project tracker settings against Linear.
func (r *Runtime) ValidateTrackerSettings(req api.SettingsUpdateRequest) (api.SettingsValidationResponse, error) {
	r.mu.Lock()
	current, err := config.LoadWorkflow(r.project.WorkflowPath)
	r.mu.Unlock()
	if err != nil {
		return api.SettingsValidationResponse{}, fmt.Errorf("%w: %v", ErrSettingsLoad, err)
	}

	promptTemplate := current.PromptTemplate
	if req.PromptTemplate != nil {
		promptTemplate = *req.PromptTemplate
	}
	def := &api.WorkflowDefinition{
		Config:         cloneConfigMap(req.Config),
		PromptTemplate: promptTemplate,
	}
	cfg, err := config.ResolveProjectDefinition(r.registry, r.project, def)
	if err != nil {
		return api.SettingsValidationResponse{}, fmt.Errorf("%w: %v", ErrSettingsValidation, err)
	}
	client, err := tracker.NewLinearClient(cfg.Tracker)
	if err != nil {
		return api.SettingsValidationResponse{}, fmt.Errorf("%w: %v", ErrSettingsValidation, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	issues, err := client.FetchCandidateIssues(ctx)
	if err != nil {
		return api.SettingsValidationResponse{}, fmt.Errorf("%w: %v", ErrTrackerValidation, err)
	}

	return api.SettingsValidationResponse{
		OK:             true,
		ProjectSlug:    cfg.Tracker.ProjectSlug,
		ActiveStates:   cfg.Tracker.ActiveStates,
		CandidateCount: len(issues),
		Message:        "Linear settings validated",
	}, nil
}

func (r *Runtime) build() (*api.WorkflowDefinition, *api.WorkflowConfig, *agent.Runner, *orchestrator.Orchestrator, error) {
	def, cfg, err := config.ResolveProjectWorkflow(r.registry, r.project)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	trackerClient, err := tracker.NewLinearClient(cfg.Tracker)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("initialize tracker: %w", err)
	}

	wsMgr, err := workspace.NewManagerWithConfig(cfg.Workspace)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("initialize workspace manager: %w", err)
	}

	runner := agent.NewRunner(def.PromptTemplate)
	orch := orchestrator.New(cfg, trackerClient, wsMgr, runner)
	orch.SetLogContext(r.project.ID, r.project.Name)
	orch.SetDispatchLimiter(r.limiter)
	return def, cfg, runner, orch, nil
}

func (r *Runtime) applyResolvedWorkflowLocked(def *api.WorkflowDefinition, cfg *api.WorkflowConfig) error {
	trackerClient, err := tracker.NewLinearClient(cfg.Tracker)
	if err != nil {
		return fmt.Errorf("initialize tracker: %w", err)
	}
	wsMgr, err := workspace.NewManagerWithConfig(cfg.Workspace)
	if err != nil {
		return fmt.Errorf("initialize workspace manager: %w", err)
	}
	if r.orch != nil {
		r.orch.UpdateRuntime(cfg, trackerClient, wsMgr)
	}
	if r.runner != nil {
		r.runner.SetPromptTemplate(def.PromptTemplate)
	}
	r.def = def
	r.cfg = cfg
	return nil
}

// WorkflowDir returns the directory containing this project's workflow file.
func (r *Runtime) WorkflowDir() string {
	return filepath.Dir(r.project.WorkflowPath)
}

func cloneConfigMap(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return map[string]interface{}{}
	}
	dst := make(map[string]interface{}, len(src))
	for key, value := range src {
		dst[key] = cloneConfigValue(value)
	}
	return dst
}

func cloneConfigValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return cloneConfigMap(v)
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = cloneConfigValue(item)
		}
		return out
	default:
		return v
	}
}
