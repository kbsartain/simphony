package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/kbsartain/simphony/internal/agent"
	"github.com/kbsartain/simphony/internal/config"
	"github.com/kbsartain/simphony/internal/orchestrator"
	"github.com/kbsartain/simphony/internal/server"
	"github.com/kbsartain/simphony/internal/tracker"
	"github.com/kbsartain/simphony/internal/workspace"
	"github.com/kbsartain/simphony/pkg/api"
)

func main() {
	var workflowPath string
	flag.StringVar(&workflowPath, "workflow", "", "Path to WORKFLOW.md (default: WORKFLOW.md in current directory)")
	flag.Parse()

	if workflowPath == "" {
		workflowPath = config.DefaultWorkflowPath()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, workflowPath); err != nil {
		log.Fatalf("simphony failed: %v", err)
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
