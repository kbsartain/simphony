package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kbsartain/simphony/pkg/api"
)

func TestFetchModelCatalog(t *testing.T) {
	var authorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-new"},{"id":"gpt-old","display_name":"GPT Old"}]}`))
	}))
	defer upstream.Close()

	result, err := fetchModelCatalog(context.Background(), api.AgentRuntimeConfig{
		ModelProvider: "openai",
		EndpointURL:   upstream.URL + "/v1",
		APIKey:        "secret",
	})
	if err != nil {
		t.Fatalf("fetchModelCatalog failed: %v", err)
	}
	if authorization != "Bearer secret" {
		t.Fatalf("authorization = %q", authorization)
	}
	if len(result.Models) != 2 || result.Models[0].ID != "gpt-new" || result.Models[1].Label != "GPT Old" {
		t.Fatalf("models = %#v", result.Models)
	}
}
