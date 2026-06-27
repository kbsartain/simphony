package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/kbsartain/simphony/internal/agent"
	"github.com/kbsartain/simphony/internal/config"
	"github.com/kbsartain/simphony/internal/orchestrator"
	"github.com/kbsartain/simphony/internal/project"
	"github.com/kbsartain/simphony/internal/server"
	"github.com/kbsartain/simphony/internal/tracker"
	"github.com/kbsartain/simphony/internal/workspace"
	"github.com/kbsartain/simphony/pkg/api"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "validate":
			if err := runValidateCommand(os.Args[2:]); err != nil {
				log.Fatalf("simphony failed: %v", err)
			}
			return
		case "projects":
			if err := runProjectsCommand(os.Args[2:]); err != nil {
				log.Fatalf("simphony failed: %v", err)
			}
			return
		}
	}

	var workflowPath string
	var registryPath string
	var projectID string
	var validateRegistryOnly bool
	flag.StringVar(&workflowPath, "workflow", "", "Path to WORKFLOW.md (default: WORKFLOW.md in current directory)")
	flag.StringVar(&registryPath, "config", "", "Path to simphony.yaml multi-project registry")
	flag.StringVar(&projectID, "project", "", "Start only this project ID from -config")
	flag.BoolVar(&validateRegistryOnly, "validate-config", false, "Validate -config and exit without starting project runtimes")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if registryPath != "" {
		if workflowPath != "" {
			log.Fatalf("simphony failed: use either -workflow or -config, not both")
		}
		if validateRegistryOnly {
			if err := validateProjectRegistry(registryPath); err != nil {
				log.Fatalf("simphony failed: %v", err)
			}
			return
		}
		if err := runProjectRegistry(ctx, registryPath, projectID); err != nil {
			log.Fatalf("simphony failed: %v", err)
		}
		return
	}

	if workflowPath == "" {
		workflowPath = config.DefaultWorkflowPath()
	}

	if err := run(ctx, workflowPath); err != nil {
		log.Fatalf("simphony failed: %v", err)
	}
}

func runValidateCommand(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	var registryPath string
	fs.StringVar(&registryPath, "config", "", "Path to simphony.yaml multi-project registry")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(registryPath) == "" {
		return fmt.Errorf("validate requires -config")
	}
	return validateProjectRegistry(registryPath)
}

func runProjectsCommand(args []string) error {
	fs := flag.NewFlagSet("projects", flag.ExitOnError)
	var registryPath string
	fs.StringVar(&registryPath, "config", "", "Path to simphony.yaml multi-project registry")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(registryPath) == "" {
		return fmt.Errorf("projects requires -config")
	}
	return listProjectRegistry(registryPath)
}

func validateProjectRegistry(registryPath string) error {
	registry, err := config.LoadProjectRegistry(registryPath)
	if err != nil {
		return fmt.Errorf("load project registry: %w", err)
	}

	enabledProjects := registry.EnabledProjects()
	report, err := config.ValidateProjectIsolation(registry)
	if err != nil {
		return err
	}

	fmt.Printf("Loaded project registry from %s\n", registry.SourcePath)
	fmt.Printf("Projects: %d configured, %d enabled\n", len(registry.Projects), len(enabledProjects))
	for _, project := range registry.Projects {
		status := "enabled"
		if !project.Enabled {
			status = "disabled"
		}
		fmt.Printf("- %s (%s): %s [%s]\n", project.Name, project.ID, project.WorkflowPath, status)
	}
	printRegistryWarnings(report.Warnings)
	fmt.Println("Registry validation complete; run with -config to start enabled project runtimes.")
	return nil
}

func listProjectRegistry(registryPath string) error {
	registry, err := config.LoadProjectRegistry(registryPath)
	if err != nil {
		return fmt.Errorf("load project registry: %w", err)
	}
	report, err := config.ValidateProjectIsolation(registry)
	if err != nil {
		return err
	}

	fmt.Printf("Loaded project registry from %s\n", registry.SourcePath)
	if registry.Server != nil {
		fmt.Printf("Server: http://%s:%d%s\n", registry.Server.BindAddress, registry.Server.Port, registry.Server.APIPrefix)
	} else {
		fmt.Println("Server: disabled")
	}
	if registry.Concurrency.MaxConcurrentAgents > 0 {
		fmt.Printf("Global concurrency: %d agent(s)\n", registry.Concurrency.MaxConcurrentAgents)
	} else {
		fmt.Println("Global concurrency: unlimited")
	}
	printRegistryWarnings(report.Warnings)

	for _, project := range registry.Projects {
		status := "enabled"
		if !project.Enabled {
			status = "disabled"
		}
		fmt.Printf("- %s (%s): %s [%s]\n", project.Name, project.ID, project.WorkflowPath, status)
		_, cfg, err := config.ResolveProjectWorkflow(registry, project)
		if err != nil {
			fmt.Printf("  health: config_error=%v\n", err)
			continue
		}
		fmt.Printf("  workspace.root: %s\n", cfg.Workspace.Root)
		fmt.Printf("  tracker: %s project_slug=%s\n", cfg.Tracker.Kind, cfg.Tracker.ProjectSlug)
		fmt.Printf("  agent_runtime: provider=%s model=%s\n", cfg.AgentRuntime.Provider, cfg.AgentRuntime.Model)
		fmt.Printf("  max_concurrent_agents: %d\n", cfg.Agent.MaxConcurrentAgents)
	}
	return nil
}

func runProjectRegistry(ctx context.Context, registryPath string, projectID string) error {
	registry, err := config.LoadProjectRegistry(registryPath)
	if err != nil {
		return fmt.Errorf("load project registry: %w", err)
	}
	validationReport, err := config.ValidateProjectIsolation(registry)
	if err != nil {
		return err
	}
	printRegistryWarnings(validationReport.Warnings)

	manager := project.NewManager(registry)
	projectID = strings.TrimSpace(projectID)
	report := project.StartReport{}
	if projectID != "" {
		report = manager.StartProject(ctx, projectID)
	} else {
		report = manager.Start(ctx)
	}
	if len(report.Started) == 0 {
		for id, err := range report.Failed {
			if id != "" && err != nil {
				return fmt.Errorf("start project runtimes: %s: %w", id, err)
			}
		}
		return fmt.Errorf("start project runtimes: no enabled projects started; failures=%d", len(report.Failed))
	}
	for id, err := range report.Failed {
		log.Printf("project_id=%s action=project_start warning=%v", id, err)
	}

	fmt.Printf("Loaded project registry from %s\n", registry.SourcePath)
	if projectID != "" {
		fmt.Printf("Started project %q", projectID)
	} else {
		fmt.Printf("Started %d project runtime(s)", len(report.Started))
	}
	if len(report.Failed) > 0 {
		fmt.Printf(" with %d startup failure(s)", len(report.Failed))
	}
	fmt.Println()
	for _, summary := range manager.Summaries() {
		status := "disabled"
		if summary.Enabled {
			status = "stopped"
		}
		if summary.Running {
			status = "running"
		}
		if summary.LastError != "" {
			status = "failed"
		}
		fmt.Printf("- %s (%s): %s [%s]\n", summary.Name, summary.ID, summary.WorkflowPath, status)
	}
	if registry.Server != nil {
		projectServer := server.NewProjectServer(manager, registry.Server.BindAddress, registry.Server.Port, registry.Server.APIPrefix)
		go func() {
			if err := projectServer.Start(ctx); err != nil {
				log.Printf("project_server error: %v", err)
			}
		}()
		fmt.Printf("Project API listening on http://%s:%d%s/projects\n", registry.Server.BindAddress, registry.Server.Port, registry.Server.APIPrefix)
		fmt.Printf("Dashboard/API listening on http://%s:%d\n", registry.Server.BindAddress, registry.Server.Port)
	} else {
		fmt.Println("No registry server configured; project runtimes are running headless.")
	}

	<-ctx.Done()
	manager.Stop()
	return nil
}

func printRegistryWarnings(warnings []config.RegistryWarning) {
	for _, warning := range warnings {
		fmt.Printf("Warning [%s]: %s\n", warning.Code, warning.Message)
	}
}

func run(ctx context.Context, workflowPath string) error {
	absWorkflowPath, err := filepath.Abs(workflowPath)
	if err != nil {
		return fmt.Errorf("resolve workflow path: %w", err)
	}
	workflowPath = absWorkflowPath

	def, err := config.LoadWorkflow(workflowPath)
	if err != nil {
		return fmt.Errorf("load workflow: %w", err)
	}

	workflowDir := filepath.Dir(workflowPath)
	cfg, err := config.ResolveConfig(def, workflowDir)
	if err != nil {
		return fmt.Errorf("resolve config: %w", err)
	}

	trackerClient, err := tracker.NewLinearClient(cfg.Tracker)
	if err != nil {
		return fmt.Errorf("initialize tracker: %w", err)
	}

	wsMgr, err := workspace.NewManagerWithConfig(cfg.Workspace)
	if err != nil {
		return fmt.Errorf("initialize workspace manager: %w", err)
	}

	runner := agent.NewRunner(def.PromptTemplate)
	orch := orchestrator.New(cfg, trackerClient, wsMgr, runner)
	orch.Start()

	applyWorkflow := func(newDef *api.WorkflowDefinition, newCfg *api.WorkflowConfig) error {
		newTrackerClient, err := tracker.NewLinearClient(newCfg.Tracker)
		if err != nil {
			return fmt.Errorf("initialize tracker: %w", err)
		}
		newWsMgr, err := workspace.NewManagerWithConfig(newCfg.Workspace)
		if err != nil {
			return fmt.Errorf("initialize workspace manager: %w", err)
		}
		orch.UpdateRuntime(newCfg, newTrackerClient, newWsMgr)
		runner.SetPromptTemplate(newDef.PromptTemplate)
		return nil
	}

	// Optional HTTP server.
	if cfg.Server != nil {
		srv := server.NewWithSettings(orch, cfg.Server.Port, workflowPath, applyWorkflow)
		go func() {
			if err := srv.Start(ctx); err != nil {
				log.Printf("server error: %v", err)
			}
		}()
	}

	// Hot reload: watch WORKFLOW.md and re-apply config without restart.
	watcher, err := config.WatchWorkflow(workflowPath, func() {
		log.Printf("workflow change detected: %s", workflowPath)
		newDef, err := config.LoadWorkflow(workflowPath)
		if err != nil {
			log.Printf("workflow reload error: %v", err)
			return
		}
		newCfg, err := config.ResolveConfig(newDef, workflowDir)
		if err != nil {
			log.Printf("workflow reload error: %v", err)
			return
		}
		if err := applyWorkflow(newDef, newCfg); err != nil {
			log.Printf("workflow reload error: %v", err)
			return
		}
		log.Printf("workflow reloaded successfully")
	})
	if err != nil {
		log.Printf("workflow watch error: %v", err)
	} else {
		defer watcher.Close()
	}

	fmt.Printf("Loaded workflow from %s\n", workflowPath)
	fmt.Printf("Tracker kind: %s\n", cfg.Tracker.Kind)

	<-ctx.Done()

	orch.Stop()
	return nil
}
