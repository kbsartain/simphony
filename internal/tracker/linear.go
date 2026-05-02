package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"simphony/pkg/api"
)

const (
	defaultLinearEndpoint = "https://api.linear.app/graphql"
	linearTimeout         = 30000 * time.Millisecond
	linearPageSize        = 50
)

// LinearClient implements api.Tracker for Linear.
type LinearClient struct {
	cfg        api.TrackerConfig
	endpoint   string
	httpClient *http.Client
}

// NewLinearClient creates a new Linear tracker client.
func NewLinearClient(cfg api.TrackerConfig) (*LinearClient, error) {
	if cfg.Kind != "linear" {
		return nil, fmt.Errorf("%s: expected kind linear, got %q", api.ErrUnsupportedTrackerKind, cfg.Kind)
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("%s: api_key is required", api.ErrMissingTrackerAPIKey)
	}
	if cfg.ProjectSlug == "" {
		return nil, fmt.Errorf("%s: project_slug is required", api.ErrMissingTrackerProjectSlug)
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultLinearEndpoint
	}

	return &LinearClient{
		cfg:      cfg,
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: linearTimeout,
		},
	}, nil
}

// FetchCandidateIssues returns issues in the project's active states.
func (c *LinearClient) FetchCandidateIssues(ctx context.Context) ([]api.Issue, error) {
	return c.fetchIssuesByStates(ctx, c.cfg.ActiveStates)
}

// FetchIssuesByStates returns issues in the given states.
func (c *LinearClient) FetchIssuesByStates(ctx context.Context, states []string) ([]api.Issue, error) {
	return c.fetchIssuesByStates(ctx, states)
}

// FetchIssueStatesByIDs returns current states for specific issue IDs.
func (c *LinearClient) FetchIssueStatesByIDs(ctx context.Context, ids []string) (map[string]api.Issue, error) {
	if len(ids) == 0 {
		return map[string]api.Issue{}, nil
	}

	query := c.buildIssuesByIDsQuery()
	variables := map[string]interface{}{
		"ids": ids,
	}

	respBody, err := c.doGraphQL(ctx, query, variables)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Issues struct {
			Nodes []linearIssue `json:"nodes"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return nil, fmt.Errorf("%s: %w", api.ErrLinearUnknownPayload, err)
	}

	result := make(map[string]api.Issue, len(payload.Issues.Nodes))
	for _, li := range payload.Issues.Nodes {
		issue := normalizeIssue(li)
		result[issue.ID] = issue
	}
	return result, nil
}

func (c *LinearClient) fetchIssuesByStates(ctx context.Context, states []string) ([]api.Issue, error) {
	if len(states) == 0 {
		return []api.Issue{}, nil
	}

	query := c.buildCandidateQuery()
	var allIssues []api.Issue
	var after *string

	for {
		variables := map[string]interface{}{
			"projectSlug": c.cfg.ProjectSlug,
			"stateNames":  states,
		}
		if after != nil {
			variables["after"] = *after
		}

		respBody, err := c.doGraphQL(ctx, query, variables)
		if err != nil {
			return nil, err
		}

		var payload struct {
			Issues struct {
				Nodes    []linearIssue `json:"nodes"`
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"issues"`
		}
		if err := json.Unmarshal(respBody, &payload); err != nil {
			return nil, fmt.Errorf("%s: %w", api.ErrLinearUnknownPayload, err)
		}

		for _, li := range payload.Issues.Nodes {
			allIssues = append(allIssues, normalizeIssue(li))
		}

		if !payload.Issues.PageInfo.HasNextPage {
			break
		}
		if payload.Issues.PageInfo.EndCursor == "" {
			return nil, fmt.Errorf("%s: hasNextPage true but endCursor missing", api.ErrLinearMissingEndCursor)
		}
		after = &payload.Issues.PageInfo.EndCursor
	}

	return allIssues, nil
}

func (c *LinearClient) doGraphQL(ctx context.Context, query string, variables map[string]interface{}) ([]byte, error) {
	body := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", api.ErrLinearUnknownPayload, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", api.ErrLinearAPIRequest, err)
	}
	req.Header.Set("Authorization", c.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", api.ErrLinearAPIRequest, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		body := strings.TrimSpace(string(respBytes))
		if body != "" {
			return nil, fmt.Errorf("%s: received HTTP %d: %s", api.ErrLinearAPIStatus, resp.StatusCode, body)
		}
		return nil, fmt.Errorf("%s: received HTTP %d", api.ErrLinearAPIStatus, resp.StatusCode)
	}

	var respPayload struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respPayload); err != nil {
		return nil, fmt.Errorf("%s: %w", api.ErrLinearUnknownPayload, err)
	}

	if len(respPayload.Errors) > 0 {
		msgs := make([]string, len(respPayload.Errors))
		for i, e := range respPayload.Errors {
			msgs[i] = e.Message
		}
		return nil, fmt.Errorf("%s: %s", api.ErrLinearGraphQLErrors, strings.Join(msgs, "; "))
	}

	return respPayload.Data, nil
}

func (c *LinearClient) buildCandidateQuery() string {
	return `query Issues($projectSlug: String!, $stateNames: [String!]!, $after: String) {
  issues(
    filter: {
      project: { slugId: { eq: $projectSlug } }
      state: { name: { in: $stateNames } }
    }
    first: 50
    after: $after
  ) {
    nodes {
      id
      identifier
      title
      description
      priority
      state { name }
      branchName
      url
      labels { nodes { name } }
      createdAt
      updatedAt
      inverseRelations {
        nodes {
          type
          issue {
            id
            identifier
            state { name }
          }
        }
      }
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}`
}

func (c *LinearClient) buildIssuesByIDsQuery() string {
	return `query IssuesByIds($ids: [ID!]!) {
  issues(filter: { id: { in: $ids } }) {
    nodes {
      id
      identifier
      title
      description
      priority
      state { name }
      branchName
      url
      labels { nodes { name } }
      createdAt
      updatedAt
      inverseRelations {
        nodes {
          type
          issue {
            id
            identifier
            state { name }
          }
        }
      }
    }
  }
}`
}

type linearIssue struct {
	ID          string      `json:"id"`
	Identifier  string      `json:"identifier"`
	Title       string      `json:"title"`
	Description *string     `json:"description"`
	Priority    interface{} `json:"priority"`
	State       struct {
		Name string `json:"name"`
	} `json:"state"`
	BranchName *string `json:"branchName"`
	URL        *string `json:"url"`
	Labels     struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
	InverseRelations struct {
		Nodes []struct {
			Type  string `json:"type"`
			Issue struct {
				ID         string `json:"id"`
				Identifier string `json:"identifier"`
				State      struct {
					Name string `json:"name"`
				} `json:"state"`
			} `json:"issue"`
		} `json:"nodes"`
	} `json:"inverseRelations"`
}

func normalizeIssue(li linearIssue) api.Issue {
	issue := api.Issue{
		ID:          li.ID,
		Identifier:  li.Identifier,
		Title:       li.Title,
		Description: li.Description,
		State:       li.State.Name,
		BranchName:  li.BranchName,
		URL:         li.URL,
	}

	// Priority: integer only.
	if li.Priority != nil {
		switch v := li.Priority.(type) {
		case float64:
			if v == float64(int(v)) {
				p := int(v)
				issue.Priority = &p
			}
		case int:
			issue.Priority = &v
		case int64:
			p := int(v)
			issue.Priority = &p
		}
	}

	// Labels: lowercase.
	if len(li.Labels.Nodes) > 0 {
		issue.Labels = make([]string, 0, len(li.Labels.Nodes))
		for _, l := range li.Labels.Nodes {
			issue.Labels = append(issue.Labels, strings.ToLower(l.Name))
		}
	}

	// Blocked by: inverse relations where type is "blocks".
	if len(li.InverseRelations.Nodes) > 0 {
		issue.BlockedBy = make([]api.Blocker, 0, len(li.InverseRelations.Nodes))
		for _, rel := range li.InverseRelations.Nodes {
			if rel.Type != "blocks" {
				continue
			}
			if rel.Issue.ID == "" && rel.Issue.Identifier == "" {
				continue
			}
			b := api.Blocker{
				ID:         &rel.Issue.ID,
				Identifier: &rel.Issue.Identifier,
			}
			if rel.Issue.State.Name != "" {
				b.State = &rel.Issue.State.Name
			}
			issue.BlockedBy = append(issue.BlockedBy, b)
		}
	}

	// Timestamps.
	if li.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339, li.CreatedAt); err == nil {
			issue.CreatedAt = &t
		} else if t, err := time.Parse(time.RFC3339Nano, li.CreatedAt); err == nil {
			issue.CreatedAt = &t
		}
	}
	if li.UpdatedAt != "" {
		if t, err := time.Parse(time.RFC3339, li.UpdatedAt); err == nil {
			issue.UpdatedAt = &t
		} else if t, err := time.Parse(time.RFC3339Nano, li.UpdatedAt); err == nil {
			issue.UpdatedAt = &t
		}
	}

	return issue
}
