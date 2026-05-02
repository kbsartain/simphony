// Package prompt renders workflow prompt templates using Liquid-compatible syntax.
package prompt

import (
	"fmt"
	"strings"

	"github.com/osteele/liquid"
	"simphony/pkg/api"
)

// Renderer handles prompt template rendering.
type Renderer struct {
	engine *liquid.Engine
}

// NewRenderer creates a new prompt renderer with strict variable and filter checking.
func NewRenderer() *Renderer {
	engine := liquid.NewEngine()
	engine.StrictVariables()
	// Filters are strict by default (LaxFilters is not called).
	return &Renderer{engine: engine}
}

// Render renders a workflow prompt template with the given issue and attempt metadata.
// It returns an error if the template is invalid or if unknown variables/filters are used.
func (r *Renderer) Render(template string, issue api.Issue, attempt *int) (string, error) {
	if strings.TrimSpace(template) == "" {
		return "You are working on an issue from Linear.", nil
	}

	tpl, err := r.engine.ParseString(template)
	if err != nil {
		return "", fmt.Errorf("%s: %w", api.ErrTemplateParseError, err)
	}

	bindings := buildBindings(issue, attempt)
	result, err := tpl.RenderString(bindings)
	if err != nil {
		return "", fmt.Errorf("%s: %w", api.ErrTemplateRenderError, err)
	}

	return result, nil
}

// buildBindings converts the api.Issue into a map suitable for Liquid rendering.
// The spec requires: issue object keys as strings, nested arrays/maps preserved.
func buildBindings(issue api.Issue, attempt *int) map[string]interface{} {
	labels := make([]string, len(issue.Labels))
	copy(labels, issue.Labels)

	blockers := make([]map[string]interface{}, len(issue.BlockedBy))
	for i, b := range issue.BlockedBy {
		m := map[string]interface{}{}
		if b.ID != nil {
			m["id"] = *b.ID
		}
		if b.Identifier != nil {
			m["identifier"] = *b.Identifier
		}
		if b.State != nil {
			m["state"] = *b.State
		}
		blockers[i] = m
	}

	issueMap := map[string]interface{}{
		"id":          issue.ID,
		"identifier":  issue.Identifier,
		"title":       issue.Title,
		"state":       issue.State,
		"labels":      labels,
		"blocked_by":  blockers,
		"description": "",
		"branch_name": "",
		"url":         "",
		"priority":    "",
		"created_at":  "",
		"updated_at":  "",
	}

	if issue.Description != nil {
		issueMap["description"] = *issue.Description
	}
	if issue.BranchName != nil {
		issueMap["branch_name"] = *issue.BranchName
	}
	if issue.URL != nil {
		issueMap["url"] = *issue.URL
	}
	if issue.Priority != nil {
		issueMap["priority"] = *issue.Priority
	}
	if issue.CreatedAt != nil {
		issueMap["created_at"] = issue.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if issue.UpdatedAt != nil {
		issueMap["updated_at"] = issue.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
	}

	bindings := map[string]interface{}{
		"issue": issueMap,
	}

	if attempt != nil {
		bindings["attempt"] = *attempt
	}

	return bindings
}
