package project

import (
	"context"
	"errors"
	"testing"

	"github.com/kbsartain/simphony/internal/config"
	"github.com/kbsartain/simphony/internal/orchestrator"
)

type fakeRuntime struct {
	project  config.RegistryProject
	startErr error
	started  bool
	stopped  bool
}

func (f *fakeRuntime) ID() string {
	return f.project.ID
}

func (f *fakeRuntime) Project() config.RegistryProject {
	return f.project
}

func (f *fakeRuntime) Start(ctx context.Context) error {
	f.started = true
	return f.startErr
}

func (f *fakeRuntime) Stop() {
	f.stopped = true
}

func TestManagerStartsEnabledProjectsAndSkipsDisabled(t *testing.T) {
	registry := &config.ProjectRegistry{
		Projects: []config.RegistryProject{
			{ID: "alpha", Name: "Alpha", WorkflowPath: "alpha/WORKFLOW.md", Enabled: true},
			{ID: "beta", Name: "Beta", WorkflowPath: "beta/WORKFLOW.md", Enabled: false},
		},
	}
	created := map[string]*fakeRuntime{}
	manager := NewManagerWithFactory(registry, func(_ *config.ProjectRegistry, project config.RegistryProject, _ orchestrator.DispatchLimiter) ManagedRuntime {
		runtime := &fakeRuntime{project: project}
		created[project.ID] = runtime
		return runtime
	})

	report := manager.Start(context.Background())
	if len(report.Failed) != 0 {
		t.Fatalf("Failed = %v, want empty", report.Failed)
	}
	if len(report.Started) != 1 || report.Started[0].ID != "alpha" {
		t.Fatalf("Started = %+v, want only alpha", report.Started)
	}
	if _, ok := created["beta"]; ok {
		t.Fatal("disabled project beta was created")
	}
	if !created["alpha"].started {
		t.Fatal("alpha runtime was not started")
	}
}

func TestManagerStartFailureDoesNotStopSiblings(t *testing.T) {
	startErr := errors.New("boom")
	registry := &config.ProjectRegistry{
		Projects: []config.RegistryProject{
			{ID: "alpha", Name: "Alpha", WorkflowPath: "alpha/WORKFLOW.md", Enabled: true},
			{ID: "beta", Name: "Beta", WorkflowPath: "beta/WORKFLOW.md", Enabled: true},
		},
	}
	manager := NewManagerWithFactory(registry, func(_ *config.ProjectRegistry, project config.RegistryProject, _ orchestrator.DispatchLimiter) ManagedRuntime {
		runtime := &fakeRuntime{project: project}
		if project.ID == "alpha" {
			runtime.startErr = startErr
		}
		return runtime
	})

	report := manager.Start(context.Background())
	if len(report.Started) != 1 || report.Started[0].ID != "beta" {
		t.Fatalf("Started = %+v, want beta", report.Started)
	}
	if report.Failed["alpha"] == nil {
		t.Fatalf("Failed = %v, want alpha failure", report.Failed)
	}

	summaries := manager.Summaries()
	if len(summaries) != 2 {
		t.Fatalf("Summaries len = %d, want 2", len(summaries))
	}
	var alpha RuntimeSummary
	for _, summary := range summaries {
		if summary.ID == "alpha" {
			alpha = summary
		}
	}
	if alpha.Running {
		t.Fatal("alpha summary is running, want failed stopped project")
	}
	if alpha.LastError == "" {
		t.Fatal("alpha summary LastError is empty")
	}
}

func TestManagerStartProjectStartsOnlySelectedProject(t *testing.T) {
	registry := &config.ProjectRegistry{
		Projects: []config.RegistryProject{
			{ID: "alpha", Name: "Alpha", WorkflowPath: "alpha/WORKFLOW.md", Enabled: true},
			{ID: "beta", Name: "Beta", WorkflowPath: "beta/WORKFLOW.md", Enabled: true},
		},
	}
	created := map[string]*fakeRuntime{}
	manager := NewManagerWithFactory(registry, func(_ *config.ProjectRegistry, project config.RegistryProject, _ orchestrator.DispatchLimiter) ManagedRuntime {
		runtime := &fakeRuntime{project: project}
		created[project.ID] = runtime
		return runtime
	})

	report := manager.StartProject(context.Background(), "beta")
	if len(report.Failed) != 0 {
		t.Fatalf("Failed = %v, want empty", report.Failed)
	}
	if len(report.Started) != 1 || report.Started[0].ID != "beta" {
		t.Fatalf("Started = %+v, want only beta", report.Started)
	}
	if _, ok := created["alpha"]; ok {
		t.Fatal("unselected project alpha was created")
	}
	if !created["beta"].started {
		t.Fatal("beta runtime was not started")
	}

	alpha, ok := manager.Summary("alpha")
	if !ok || !alpha.Enabled || alpha.Running {
		t.Fatalf("alpha summary = %+v, want enabled but not running", alpha)
	}
}

func TestManagerStartProjectRejectsDisabledProject(t *testing.T) {
	registry := &config.ProjectRegistry{
		Projects: []config.RegistryProject{
			{ID: "alpha", Name: "Alpha", WorkflowPath: "alpha/WORKFLOW.md", Enabled: false},
		},
	}
	manager := NewManagerWithFactory(registry, func(_ *config.ProjectRegistry, project config.RegistryProject, _ orchestrator.DispatchLimiter) ManagedRuntime {
		t.Fatalf("factory called for disabled project %+v", project)
		return nil
	})

	report := manager.StartProject(context.Background(), "alpha")
	if report.Failed["alpha"] == nil {
		t.Fatalf("Failed = %v, want alpha disabled failure", report.Failed)
	}
	if len(report.Started) != 0 {
		t.Fatalf("Started = %+v, want none", report.Started)
	}
}

func TestManagerStopsStartedRuntimes(t *testing.T) {
	registry := &config.ProjectRegistry{
		Projects: []config.RegistryProject{
			{ID: "alpha", Name: "Alpha", WorkflowPath: "alpha/WORKFLOW.md", Enabled: true},
			{ID: "beta", Name: "Beta", WorkflowPath: "beta/WORKFLOW.md", Enabled: true},
		},
	}
	created := map[string]*fakeRuntime{}
	manager := NewManagerWithFactory(registry, func(_ *config.ProjectRegistry, project config.RegistryProject, _ orchestrator.DispatchLimiter) ManagedRuntime {
		runtime := &fakeRuntime{project: project}
		created[project.ID] = runtime
		return runtime
	})

	manager.Start(context.Background())
	manager.Stop()

	for id, runtime := range created {
		if !runtime.stopped {
			t.Fatalf("%s runtime was not stopped", id)
		}
	}
}

func TestManagerSharesGlobalLimiterAcrossRuntimes(t *testing.T) {
	registry := &config.ProjectRegistry{
		Concurrency: config.RegistryConcurrencyConfig{MaxConcurrentAgents: 3},
		Projects: []config.RegistryProject{
			{ID: "alpha", Name: "Alpha", WorkflowPath: "alpha/WORKFLOW.md", Enabled: true},
			{ID: "beta", Name: "Beta", WorkflowPath: "beta/WORKFLOW.md", Enabled: true},
		},
	}
	received := map[string]orchestrator.DispatchLimiter{}
	manager := NewManagerWithFactory(registry, func(_ *config.ProjectRegistry, project config.RegistryProject, limiter orchestrator.DispatchLimiter) ManagedRuntime {
		received[project.ID] = limiter
		return &fakeRuntime{project: project}
	})

	report := manager.Start(context.Background())
	if len(report.Failed) != 0 {
		t.Fatalf("Failed = %v, want empty", report.Failed)
	}
	if received["alpha"] == nil || received["beta"] == nil {
		t.Fatalf("received limiters = %#v, want both projects to receive one", received)
	}
	if received["alpha"] != received["beta"] {
		t.Fatal("projects received different limiter instances")
	}
	if limiter, ok := received["alpha"].(*SlotLimiter); !ok || limiter.Capacity() != 3 {
		t.Fatalf("shared limiter = %#v, want SlotLimiter capacity 3", received["alpha"])
	}
}

func TestManagerDoesNotCreateLimiterWithoutGlobalCap(t *testing.T) {
	registry := &config.ProjectRegistry{
		Projects: []config.RegistryProject{
			{ID: "alpha", Name: "Alpha", WorkflowPath: "alpha/WORKFLOW.md", Enabled: true},
		},
	}
	received := map[string]orchestrator.DispatchLimiter{}
	manager := NewManagerWithFactory(registry, func(_ *config.ProjectRegistry, project config.RegistryProject, limiter orchestrator.DispatchLimiter) ManagedRuntime {
		received[project.ID] = limiter
		return &fakeRuntime{project: project}
	})

	report := manager.Start(context.Background())
	if len(report.Failed) != 0 {
		t.Fatalf("Failed = %v, want empty", report.Failed)
	}
	if received["alpha"] != nil {
		t.Fatalf("limiter = %#v, want nil when registry has no global cap", received["alpha"])
	}
	concurrency := manager.Concurrency()
	if concurrency.MaxConcurrentAgents != 0 || concurrency.UsedAgents != 0 || concurrency.AvailableAgents != 0 {
		t.Fatalf("Concurrency = %+v, want zero-valued unlimited signal", concurrency)
	}
}

func TestManagerReportsConcurrencyUsage(t *testing.T) {
	registry := &config.ProjectRegistry{
		Concurrency: config.RegistryConcurrencyConfig{MaxConcurrentAgents: 2},
	}
	manager := NewManager(registry)

	if !manager.limiter.TryAcquire() {
		t.Fatal("expected limiter acquisition to succeed")
	}
	concurrency := manager.Concurrency()
	if concurrency.MaxConcurrentAgents != 2 || concurrency.UsedAgents != 1 || concurrency.AvailableAgents != 1 {
		t.Fatalf("Concurrency = %+v, want capacity 2 used 1 available 1", concurrency)
	}
}
