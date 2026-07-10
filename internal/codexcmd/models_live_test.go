package codexcmd

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestListModelsLive(t *testing.T) {
	if os.Getenv("SIMPHONY_LIVE_CODEX_MODELS") == "" {
		t.Skip("set SIMPHONY_LIVE_CODEX_MODELS=1 to query the installed Codex app-server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	models, err := ListModels(ctx, "codex app-server", t.TempDir(), os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) == 0 {
		t.Fatal("Codex returned no models")
	}
	t.Logf("Codex returned %d models; first=%s", len(models), models[0].Model)
}
