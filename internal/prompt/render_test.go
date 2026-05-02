package prompt

import (
	"strings"
	"testing"
	"time"

	"simphony/pkg/api"
)

func TestRender_Basic(t *testing.T) {
	r := NewRenderer()
	issue := api.Issue{
		ID:         "abc123",
		Identifier: "PROJ-42",
		Title:      "Fix the bug",
		State:      "In Progress",
		Description: strPtr("Something is broken."),
	}
	template := "Issue {{ issue.identifier }}: {{ issue.title }}\n{{ issue.description }}"

	result, err := r.Render(template, issue, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "Issue PROJ-42: Fix the bug\nSomething is broken."
	if result != want {
		t.Fatalf("render result = %q, want %q", result, want)
	}
}

func TestRender_WithAttempt(t *testing.T) {
	r := NewRenderer()
	issue := api.Issue{
		ID:         "abc123",
		Identifier: "PROJ-42",
		Title:      "Fix the bug",
		State:      "In Progress",
	}
	template := "Attempt {{ attempt }} for {{ issue.identifier }}"
	attempt := 3

	result, err := r.Render(template, issue, &attempt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "Attempt 3 for PROJ-42"
	if result != want {
		t.Fatalf("render result = %q, want %q", result, want)
	}
}

func TestRender_UnknownVariable(t *testing.T) {
	r := NewRenderer()
	issue := api.Issue{
		ID:         "abc123",
		Identifier: "PROJ-42",
		Title:      "Fix the bug",
		State:      "In Progress",
	}
	template := "Hello {{ unknown_var }}"

	_, err := r.Render(template, issue, nil)
	if err == nil {
		t.Fatal("expected error for unknown variable, got nil")
	}
	if !strings.Contains(err.Error(), api.ErrTemplateRenderError) {
		t.Fatalf("expected error containing %q, got %v", api.ErrTemplateRenderError, err)
	}
}

func TestRender_UnknownFilter(t *testing.T) {
	r := NewRenderer()
	issue := api.Issue{
		ID:         "abc123",
		Identifier: "PROJ-42",
		Title:      "Fix the bug",
		State:      "In Progress",
	}
	template := "{{ issue.title | nonexistent_filter }}"

	_, err := r.Render(template, issue, nil)
	if err == nil {
		t.Fatal("expected error for unknown filter, got nil")
	}
	if !strings.Contains(err.Error(), api.ErrTemplateRenderError) {
		t.Fatalf("expected error containing %q, got %v", api.ErrTemplateRenderError, err)
	}
}

func TestRender_LabelsIteration(t *testing.T) {
	r := NewRenderer()
	issue := api.Issue{
		ID:         "abc123",
		Identifier: "PROJ-42",
		Title:      "Fix the bug",
		State:      "In Progress",
		Labels:     []string{"bug", "critical"},
	}
	template := "Labels: {% for label in issue.labels %}{{ label }}{% unless forloop.last %}, {% endunless %}{% endfor %}"

	result, err := r.Render(template, issue, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "Labels: bug, critical"
	if result != want {
		t.Fatalf("render result = %q, want %q", result, want)
	}
}

func TestRender_Blockers(t *testing.T) {
	r := NewRenderer()
	state := "Todo"
	issue := api.Issue{
		ID:         "abc123",
		Identifier: "PROJ-42",
		Title:      "Fix the bug",
		State:      "In Progress",
		BlockedBy: []api.Blocker{
			{ID: strPtr("b1"), Identifier: strPtr("PROJ-10"), State: &state},
		},
	}
	template := "Blocked by: {% for b in issue.blocked_by %}{{ b.identifier }} ({{ b.state }}){% endfor %}"

	result, err := r.Render(template, issue, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "Blocked by: PROJ-10 (Todo)"
	if result != want {
		t.Fatalf("render result = %q, want %q", result, want)
	}
}

func TestRender_EmptyTemplateFallback(t *testing.T) {
	r := NewRenderer()
	issue := api.Issue{ID: "1", Identifier: "A-1", Title: "T", State: "Todo"}

	result, err := r.Render("   ", issue, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "You are working on an issue from Linear."
	if result != want {
		t.Fatalf("render result = %q, want %q", result, want)
	}
}

func TestRender_NilOptionalFields(t *testing.T) {
	r := NewRenderer()
	issue := api.Issue{
		ID:         "abc123",
		Identifier: "PROJ-42",
		Title:      "Fix the bug",
		State:      "In Progress",
		// Description, Priority, BranchName, URL are nil
	}
	template := "Title: {{ issue.title }}, Priority: {{ issue.priority }}, Desc: {{ issue.description }}"

	result, err := r.Render(template, issue, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// nil values in Liquid render as empty string
	want := "Title: Fix the bug, Priority: , Desc: "
	if result != want {
		t.Fatalf("render result = %q, want %q", result, want)
	}
}

func TestRender_Timestamps(t *testing.T) {
	r := NewRenderer()
	now := time.Now()
	issue := api.Issue{
		ID:         "abc123",
		Identifier: "PROJ-42",
		Title:      "Fix the bug",
		State:      "In Progress",
		CreatedAt:  &now,
	}
	template := "Created: {{ issue.created_at }}"

	result, err := r.Render(template, issue, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, now.Format("2006")) {
		t.Fatalf("render result = %q, expected to contain year", result)
	}
}

func strPtr(s string) *string {
	return &s
}
