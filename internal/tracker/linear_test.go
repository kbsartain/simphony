package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kbsartain/simphony/pkg/api"
)

func TestNewLinearClient_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     api.TrackerConfig
		wantErr string
	}{
		{
			name:    "wrong kind",
			cfg:     api.TrackerConfig{Kind: "jira", APIKey: "key", ProjectSlug: "proj"},
			wantErr: api.ErrUnsupportedTrackerKind,
		},
		{
			name:    "missing api key",
			cfg:     api.TrackerConfig{Kind: "linear", ProjectSlug: "proj"},
			wantErr: api.ErrMissingTrackerAPIKey,
		},
		{
			name:    "missing project slug",
			cfg:     api.TrackerConfig{Kind: "linear", APIKey: "key"},
			wantErr: api.ErrMissingTrackerProjectSlug,
		},
		{
			name: "valid with default endpoint",
			cfg:  api.TrackerConfig{Kind: "linear", APIKey: "key", ProjectSlug: "proj"},
		},
		{
			name: "valid with custom endpoint",
			cfg:  api.TrackerConfig{Kind: "linear", APIKey: "key", ProjectSlug: "proj", Endpoint: "https://custom.linear.app/graphql"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewLinearClient(tt.cfg)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if client == nil {
				t.Fatal("expected non-nil client")
			}
			if tt.cfg.Endpoint != "" && client.endpoint != tt.cfg.Endpoint {
				t.Errorf("endpoint = %q, want %q", client.endpoint, tt.cfg.Endpoint)
			}
			if tt.cfg.Endpoint == "" && client.endpoint != defaultLinearEndpoint {
				t.Errorf("endpoint = %q, want default %q", client.endpoint, defaultLinearEndpoint)
			}
			if client.httpClient.Timeout != linearTimeout {
				t.Errorf("timeout = %v, want %v", client.httpClient.Timeout, linearTimeout)
			}
		})
	}
}

func TestFetchCandidateIssues_SinglePage(t *testing.T) {
	server := newLinearMockServer(t, &mockServerConfig{
		issues: []map[string]interface{}{
			{
				"id":         "issue-1",
				"identifier": "TEST-1",
				"title":      "First issue",
				"state":      map[string]string{"name": "Todo"},
				"createdAt":  "2024-01-01T00:00:00Z",
				"updatedAt":  "2024-01-02T00:00:00Z",
			},
			{
				"id":         "issue-2",
				"identifier": "TEST-2",
				"title":      "Second issue",
				"state":      map[string]string{"name": "In Progress"},
				"createdAt":  "2024-01-03T00:00:00Z",
				"updatedAt":  "2024-01-04T00:00:00Z",
			},
		},
	})
	defer server.Close()

	client := mustNewClient(t, server.URL, "proj", []string{"Todo", "In Progress"})

	issues, err := client.FetchCandidateIssues(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}
	if issues[0].Identifier != "TEST-1" {
		t.Errorf("issue[0].identifier = %q, want TEST-1", issues[0].Identifier)
	}
	if issues[1].Identifier != "TEST-2" {
		t.Errorf("issue[1].identifier = %q, want TEST-2", issues[1].Identifier)
	}
}

func TestFetchCandidateIssues_Pagination(t *testing.T) {
	server := newLinearMockServer(t, &mockServerConfig{
		issues: []map[string]interface{}{
			{"id": "issue-1", "identifier": "TEST-1", "title": "First", "state": map[string]string{"name": "Todo"}},
			{"id": "issue-2", "identifier": "TEST-2", "title": "Second", "state": map[string]string{"name": "Todo"}},
		},
		pageSize: 1,
	})
	defer server.Close()

	client := mustNewClient(t, server.URL, "proj", []string{"Todo"})

	issues, err := client.FetchCandidateIssues(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}
	if issues[0].ID != "issue-1" {
		t.Errorf("issue[0].id = %q, want issue-1", issues[0].ID)
	}
	if issues[1].ID != "issue-2" {
		t.Errorf("issue[1].id = %q, want issue-2", issues[1].ID)
	}
}

func TestFetchCandidateIssues_MissingEndCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"issues": map[string]interface{}{
					"nodes": []map[string]interface{}{
						{"id": "issue-1", "identifier": "TEST-1", "title": "First", "state": map[string]string{"name": "Todo"}},
					},
					"pageInfo": map[string]interface{}{
						"hasNextPage": true,
						"endCursor":   "",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := mustNewClient(t, server.URL, "proj", []string{"Todo"})

	_, err := client.FetchCandidateIssues(context.Background())
	if err == nil {
		t.Fatal("expected error for missing end cursor, got nil")
	}
	if !strings.Contains(err.Error(), api.ErrLinearMissingEndCursor) {
		t.Errorf("expected error containing %q, got %v", api.ErrLinearMissingEndCursor, err)
	}
}

func TestFetchIssuesByStates(t *testing.T) {
	server := newLinearMockServer(t, &mockServerConfig{
		issues: []map[string]interface{}{
			{"id": "issue-1", "identifier": "TEST-1", "title": "First", "state": map[string]string{"name": "Closed"}},
			{"id": "issue-2", "identifier": "TEST-2", "title": "Second", "state": map[string]string{"name": "Cancelled"}},
		},
	})
	defer server.Close()

	client := mustNewClient(t, server.URL, "proj", []string{"Todo"})

	issues, err := client.FetchIssuesByStates(context.Background(), []string{"Closed", "Cancelled"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}
	if issues[0].State != "Closed" {
		t.Errorf("issue[0].state = %q, want Closed", issues[0].State)
	}
	if issues[1].State != "Cancelled" {
		t.Errorf("issue[1].state = %q, want Cancelled", issues[1].State)
	}
}

func TestFetchIssueStatesByIDs(t *testing.T) {
	server := newLinearMockServer(t, &mockServerConfig{
		issues: []map[string]interface{}{
			{"id": "issue-1", "identifier": "TEST-1", "title": "First", "state": map[string]string{"name": "In Progress"}},
			{"id": "issue-2", "identifier": "TEST-2", "title": "Second", "state": map[string]string{"name": "Todo"}},
		},
	})
	defer server.Close()

	client := mustNewClient(t, server.URL, "proj", []string{"Todo"})

	result, err := client.FetchIssueStatesByIDs(context.Background(), []string{"issue-1", "issue-2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(result))
	}
	if result["issue-1"].State != "In Progress" {
		t.Errorf("issue-1 state = %q, want In Progress", result["issue-1"].State)
	}
	if result["issue-2"].State != "Todo" {
		t.Errorf("issue-2 state = %q, want Todo", result["issue-2"].State)
	}
}

func TestFetchIssueStatesByIDs_Empty(t *testing.T) {
	client := mustNewClient(t, "http://example.com", "proj", []string{"Todo"})
	result, err := client.FetchIssueStatesByIDs(context.Background(), []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected 0 issues, got %d", len(result))
	}
}

func TestTransitionIssueState(t *testing.T) {
	server := newLinearMockServer(t, &mockServerConfig{
		issues: []map[string]interface{}{
			{"id": "issue-1", "identifier": "TEST-1", "title": "First", "state": map[string]string{"name": "Todo"}},
		},
		states: []map[string]string{
			{"id": "state-todo", "name": "Todo"},
			{"id": "state-progress", "name": "In Progress"},
		},
	})
	defer server.Close()

	client := mustNewClient(t, server.URL, "proj", []string{"Todo"})
	updated, err := client.TransitionIssueState(context.Background(), api.Issue{
		ID:         "issue-1",
		Identifier: "TEST-1",
		Title:      "First",
		State:      "Todo",
	}, "In Progress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.State != "In Progress" {
		t.Fatalf("updated.State = %q, want In Progress", updated.State)
	}
}

func TestTransitionIssueState_NoOpWhenAlreadyInState(t *testing.T) {
	client := mustNewClient(t, "http://127.0.0.1:1", "proj", []string{"Todo"})
	issue := api.Issue{ID: "issue-1", Identifier: "TEST-1", Title: "First", State: "In Progress"}

	updated, err := client.TransitionIssueState(context.Background(), issue, "In Progress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.State != "In Progress" {
		t.Fatalf("updated.State = %q, want In Progress", updated.State)
	}
}

func TestAddIssueComment(t *testing.T) {
	server := newLinearMockServer(t, &mockServerConfig{})
	defer server.Close()

	client := mustNewClient(t, server.URL, "proj", []string{"Todo"})
	err := client.AddIssueComment(context.Background(), api.Issue{ID: "issue-1", Identifier: "TEST-1"}, "hello from simphony")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"issues": map[string]interface{}{
					"nodes": []map[string]interface{}{
						{
							"id":          "issue-1",
							"identifier":  "TEST-1",
							"title":       "Test Issue",
							"description": "A description",
							"priority":    2.0,
							"state":       map[string]string{"name": "Todo"},
							"branchName":  "feature/test-1",
							"url":         "https://linear.app/issue/TEST-1",
							"labels": map[string]interface{}{
								"nodes": []map[string]string{
									{"name": "Bug"},
									{"name": "HIGH-PRIORITY"},
								},
							},
							"createdAt": "2024-01-01T00:00:00Z",
							"updatedAt": "2024-01-02T12:30:45.123Z",
							"inverseRelations": map[string]interface{}{
								"nodes": []map[string]interface{}{
									{
										"type": "blocks",
										"issue": map[string]interface{}{
											"id":         "blocker-1",
											"identifier": "TEST-99",
											"state":      map[string]string{"name": "In Progress"},
										},
									},
									{
										"type": "relates",
										"issue": map[string]interface{}{
											"id":         "related-1",
											"identifier": "TEST-100",
											"state":      map[string]string{"name": "Todo"},
										},
									},
								},
							},
						},
						{
							"id":         "issue-2",
							"identifier": "TEST-2",
							"title":      "Non-integer priority",
							"priority":   2.5,
							"state":      map[string]string{"name": "Todo"},
							"labels": map[string]interface{}{
								"nodes": []map[string]string{},
							},
						},
						{
							"id":         "issue-3",
							"identifier": "TEST-3",
							"title":      "No optional fields",
							"state":      map[string]string{"name": "Done"},
						},
					},
					"pageInfo": map[string]interface{}{
						"hasNextPage": false,
						"endCursor":   "",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := mustNewClient(t, server.URL, "proj", []string{"Todo"})

	issues, err := client.FetchCandidateIssues(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 3 {
		t.Fatalf("expected 3 issues, got %d", len(issues))
	}

	// Issue 1: fully populated.
	issue1 := issues[0]
	if issue1.ID != "issue-1" {
		t.Errorf("id = %q, want issue-1", issue1.ID)
	}
	if issue1.Identifier != "TEST-1" {
		t.Errorf("identifier = %q, want TEST-1", issue1.Identifier)
	}
	if issue1.Title != "Test Issue" {
		t.Errorf("title = %q, want Test Issue", issue1.Title)
	}
	if issue1.Description == nil || *issue1.Description != "A description" {
		t.Errorf("description = %v, want 'A description'", issue1.Description)
	}
	if issue1.Priority == nil || *issue1.Priority != 2 {
		t.Errorf("priority = %v, want 2", issue1.Priority)
	}
	if issue1.State != "Todo" {
		t.Errorf("state = %q, want Todo", issue1.State)
	}
	if issue1.BranchName == nil || *issue1.BranchName != "feature/test-1" {
		t.Errorf("branch_name = %v, want feature/test-1", issue1.BranchName)
	}
	if issue1.URL == nil || *issue1.URL != "https://linear.app/issue/TEST-1" {
		t.Errorf("url = %v, want https://linear.app/issue/TEST-1", issue1.URL)
	}
	wantLabels := []string{"bug", "high-priority"}
	if len(issue1.Labels) != len(wantLabels) {
		t.Errorf("labels = %v, want %v", issue1.Labels, wantLabels)
	}
	for i, l := range issue1.Labels {
		if l != wantLabels[i] {
			t.Errorf("labels[%d] = %q, want %q", i, l, wantLabels[i])
		}
	}
	if len(issue1.BlockedBy) != 1 {
		t.Fatalf("blocked_by length = %d, want 1", len(issue1.BlockedBy))
	}
	if *issue1.BlockedBy[0].ID != "blocker-1" {
		t.Errorf("blocked_by[0].id = %q, want blocker-1", *issue1.BlockedBy[0].ID)
	}
	if *issue1.BlockedBy[0].Identifier != "TEST-99" {
		t.Errorf("blocked_by[0].identifier = %q, want TEST-99", *issue1.BlockedBy[0].Identifier)
	}
	if *issue1.BlockedBy[0].State != "In Progress" {
		t.Errorf("blocked_by[0].state = %q, want In Progress", *issue1.BlockedBy[0].State)
	}
	if issue1.CreatedAt == nil {
		t.Error("created_at is nil")
	} else if !issue1.CreatedAt.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("created_at = %v, want 2024-01-01T00:00:00Z", issue1.CreatedAt)
	}
	if issue1.UpdatedAt == nil {
		t.Error("updated_at is nil")
	} else if !issue1.UpdatedAt.Equal(time.Date(2024, 1, 2, 12, 30, 45, 123000000, time.UTC)) {
		t.Errorf("updated_at = %v, want 2024-01-02T12:30:45.123Z", issue1.UpdatedAt)
	}

	// Issue 2: non-integer priority should be nil.
	issue2 := issues[1]
	if issue2.Priority != nil {
		t.Errorf("issue2.priority = %v, want nil", issue2.Priority)
	}

	// Issue 3: absent optional fields should be nil/empty.
	issue3 := issues[2]
	if issue3.Description != nil {
		t.Errorf("issue3.description = %v, want nil", issue3.Description)
	}
	if issue3.Priority != nil {
		t.Errorf("issue3.priority = %v, want nil", issue3.Priority)
	}
	if issue3.BranchName != nil {
		t.Errorf("issue3.branch_name = %v, want nil", issue3.BranchName)
	}
	if issue3.URL != nil {
		t.Errorf("issue3.url = %v, want nil", issue3.URL)
	}
	if len(issue3.Labels) != 0 {
		t.Errorf("issue3.labels = %v, want empty", issue3.Labels)
	}
	if len(issue3.BlockedBy) != 0 {
		t.Errorf("issue3.blocked_by = %v, want empty", issue3.BlockedBy)
	}
	if issue3.CreatedAt != nil {
		t.Errorf("issue3.created_at = %v, want nil", issue3.CreatedAt)
	}
	if issue3.UpdatedAt != nil {
		t.Errorf("issue3.updated_at = %v, want nil", issue3.UpdatedAt)
	}
}

func TestErrorHandling_TransportError(t *testing.T) {
	client := mustNewClient(t, "http://127.0.0.1:1", "proj", []string{"Todo"})

	_, err := client.FetchCandidateIssues(context.Background())
	if err == nil {
		t.Fatal("expected error for transport failure, got nil")
	}
	if !strings.Contains(err.Error(), api.ErrLinearAPIRequest) {
		t.Errorf("expected error containing %q, got %v", api.ErrLinearAPIRequest, err)
	}
}

func TestErrorHandling_Non200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad token test-key"}`))
	}))
	defer server.Close()

	client := mustNewClient(t, server.URL, "proj", []string{"Todo"})

	_, err := client.FetchCandidateIssues(context.Background())
	if err == nil {
		t.Fatal("expected error for non-200, got nil")
	}
	if !strings.Contains(err.Error(), api.ErrLinearAPIStatus) {
		t.Errorf("expected error containing %q, got %v", api.ErrLinearAPIStatus, err)
	}
	if !strings.Contains(err.Error(), "bad token") {
		t.Errorf("expected error body in error, got %v", err)
	}
	if strings.Contains(err.Error(), "test-key") {
		t.Errorf("error leaked api key: %v", err)
	}
	if !strings.Contains(err.Error(), "********") {
		t.Errorf("error did not include secret mask: %v", err)
	}
}

func TestErrorHandling_GraphQLErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"errors": []map[string]interface{}{
				{"message": "Invalid filter"},
				{"message": "Project not found for token test-key"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := mustNewClient(t, server.URL, "proj", []string{"Todo"})

	_, err := client.FetchCandidateIssues(context.Background())
	if err == nil {
		t.Fatal("expected error for GraphQL errors, got nil")
	}
	if !strings.Contains(err.Error(), api.ErrLinearGraphQLErrors) {
		t.Errorf("expected error containing %q, got %v", api.ErrLinearGraphQLErrors, err)
	}
	if !strings.Contains(err.Error(), "Invalid filter") {
		t.Errorf("expected error containing 'Invalid filter', got %v", err)
	}
	if !strings.Contains(err.Error(), "Project not found") {
		t.Errorf("expected error containing 'Project not found', got %v", err)
	}
	if strings.Contains(err.Error(), "test-key") {
		t.Errorf("GraphQL error leaked api key: %v", err)
	}
}

func TestErrorHandling_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer server.Close()

	client := mustNewClient(t, server.URL, "proj", []string{"Todo"})

	_, err := client.FetchCandidateIssues(context.Background())
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), api.ErrLinearUnknownPayload) {
		t.Errorf("expected error containing %q, got %v", api.ErrLinearUnknownPayload, err)
	}
}

func TestContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := mustNewClient(t, server.URL, "proj", []string{"Todo"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.FetchCandidateIssues(ctx)
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
	if !strings.Contains(err.Error(), api.ErrLinearAPIRequest) {
		t.Errorf("expected error containing %q, got %v", api.ErrLinearAPIRequest, err)
	}
}

type mockServerConfig struct {
	issues   []map[string]interface{}
	states   []map[string]string
	pageSize int
}

func newLinearMockServer(t *testing.T, cfg *mockServerConfig) *httptest.Server {
	t.Helper()
	if cfg.pageSize == 0 {
		cfg.pageSize = 50
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "test-key" {
			t.Errorf("expected Authorization header test-key, got %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", r.Header.Get("Content-Type"))
		}

		var reqBody struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		// Determine which query type this is.
		if strings.Contains(reqBody.Query, "CreateComment") {
			resp := map[string]interface{}{
				"data": map[string]interface{}{
					"commentCreate": map[string]interface{}{
						"success": true,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(reqBody.Query, "IssueTeamStates") {
			resp := map[string]interface{}{
				"data": map[string]interface{}{
					"issue": map[string]interface{}{
						"team": map[string]interface{}{
							"states": map[string]interface{}{
								"nodes": cfg.states,
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(reqBody.Query, "UpdateIssueState") {
			stateID, _ := reqBody.Variables["stateID"].(string)
			stateName := ""
			for _, state := range cfg.states {
				if state["id"] == stateID {
					stateName = state["name"]
					break
				}
			}
			if stateName == "" {
				stateName = "Unknown"
			}

			var issue map[string]interface{}
			if len(cfg.issues) > 0 {
				issue = cloneIssueMap(cfg.issues[0])
			} else {
				issue = map[string]interface{}{"id": reqBody.Variables["issueID"], "identifier": "TEST-1", "title": "First"}
			}
			issue["state"] = map[string]string{"name": stateName}
			resp := map[string]interface{}{
				"data": map[string]interface{}{
					"issueUpdate": map[string]interface{}{
						"success": true,
						"issue":   issue,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(reqBody.Query, "IssuesByIds") {
			// Return all matching issues by ID.
			var ids []string
			if rawIDs, ok := reqBody.Variables["ids"].([]interface{}); ok {
				for _, id := range rawIDs {
					if s, ok := id.(string); ok {
						ids = append(ids, s)
					}
				}
			}
			var nodes []map[string]interface{}
			for _, issue := range cfg.issues {
				for _, id := range ids {
					if issue["id"] == id {
						nodes = append(nodes, issue)
					}
				}
			}
			resp := map[string]interface{}{
				"data": map[string]interface{}{
					"issues": map[string]interface{}{
						"nodes": nodes,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// Paginated Issues query.
		var after string
		if a, ok := reqBody.Variables["after"].(string); ok {
			after = a
		}

		// Find start index based on after cursor.
		startIdx := 0
		if after != "" {
			fmt.Sscanf(after, "cursor-%d", &startIdx)
		}

		endIdx := startIdx + cfg.pageSize
		hasNext := false
		if endIdx < len(cfg.issues) {
			hasNext = true
		} else {
			endIdx = len(cfg.issues)
		}

		nodes := cfg.issues[startIdx:endIdx]
		var nextCursor string
		if hasNext {
			nextCursor = fmt.Sprintf("cursor-%d", endIdx)
		}

		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"issues": map[string]interface{}{
					"nodes": nodes,
					"pageInfo": map[string]interface{}{
						"hasNextPage": hasNext,
						"endCursor":   nextCursor,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func cloneIssueMap(issue map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(issue))
	for k, v := range issue {
		out[k] = v
	}
	return out
}

func mustNewClient(t *testing.T, endpoint, projectSlug string, activeStates []string) *LinearClient {
	t.Helper()
	client, err := NewLinearClient(api.TrackerConfig{
		Kind:         "linear",
		Endpoint:     endpoint,
		APIKey:       "test-key",
		ProjectSlug:  projectSlug,
		ActiveStates: activeStates,
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	return client
}
