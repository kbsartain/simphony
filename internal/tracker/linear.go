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

	"github.com/kbsartain/simphony/pkg/api"
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

// MoveIssueToFirstAvailableState moves an issue to the first workflow state
// matching preferredStates order and returns the updated issue.
func (c *LinearClient) MoveIssueToFirstAvailableState(ctx context.Context, issueID string, preferredStates []string) (api.Issue, error) {
	if issueID == "" {
		return api.Issue{}, fmt.Errorf("%s: issue id is required", api.ErrLinearUnknownPayload)
	}
	preferredStates = normalizeStatePreferences(preferredStates)
	if len(preferredStates) == 0 {
		return api.Issue{}, fmt.Errorf("%s: completion state list is empty", api.ErrLinearUnknownPayload)
	}

	stateID, stateName, err := c.findIssueWorkflowStateID(ctx, issueID, preferredStates)
	if err != nil {
		return api.Issue{}, err
	}

	respBody, err := c.doGraphQL(ctx, c.buildIssueUpdateMutation(), map[string]interface{}{
		"issueID": issueID,
		"stateID": stateID,
	})
	if err != nil {
		return api.Issue{}, err
	}

	var payload struct {
		IssueUpdate struct {
			Success bool        `json:"success"`
			Issue   linearIssue `json:"issue"`
		} `json:"issueUpdate"`
	}
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return api.Issue{}, fmt.Errorf("%s: %w", api.ErrLinearUnknownPayload, err)
	}
	if !payload.IssueUpdate.Success {
		return api.Issue{}, fmt.Errorf("%s: issueUpdate returned success=false", api.ErrLinearUnknownPayload)
	}
	issue := normalizeIssue(payload.IssueUpdate.Issue)
	if issue.ID == "" {
		issue.ID = issueID
	}
	if issue.State == "" {
		issue.State = stateName
	}
	return issue, nil
}

// MoveIssueToState moves an issue to the named Linear workflow state.
func (c *LinearClient) MoveIssueToState(ctx context.Context, issueID string, state string) (api.Issue, error) {
	return c.MoveIssueToFirstAvailableState(ctx, issueID, []string{state})
}

// TransitionIssueState moves an issue to the named Linear workflow state.
func (c *LinearClient) TransitionIssueState(ctx context.Context, issue api.Issue, state string) (api.Issue, error) {
	state = strings.TrimSpace(state)
	if state == "" || strings.EqualFold(issue.State, state) {
		return issue, nil
	}

	stateID, err := c.findIssueTeamStateID(ctx, issue.ID, state)
	if err != nil {
		return api.Issue{}, err
	}

	respBody, err := c.doGraphQL(ctx, c.buildIssueUpdateMutation(), map[string]interface{}{
		"issueID": issue.ID,
		"stateID": stateID,
	})
	if err != nil {
		return api.Issue{}, err
	}

	var payload struct {
		IssueUpdate struct {
			Success bool        `json:"success"`
			Issue   linearIssue `json:"issue"`
		} `json:"issueUpdate"`
	}
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return api.Issue{}, fmt.Errorf("%s: %w", api.ErrLinearUnknownPayload, err)
	}
	if !payload.IssueUpdate.Success {
		return api.Issue{}, fmt.Errorf("%s: issueUpdate returned success=false", api.ErrLinearUnknownPayload)
	}
	updated := normalizeIssue(payload.IssueUpdate.Issue)
	if updated.ID == "" {
		return api.Issue{}, fmt.Errorf("%s: issueUpdate response missing issue", api.ErrLinearUnknownPayload)
	}
	return updated, nil
}

// AddIssueComment posts a comment to a Linear issue.
func (c *LinearClient) AddIssueComment(ctx context.Context, issue api.Issue, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}

	respBody, err := c.doGraphQL(ctx, c.buildCommentCreateMutation(), map[string]interface{}{
		"issueID": issue.ID,
		"body":    body,
	})
	if err != nil {
		return err
	}

	var payload struct {
		CommentCreate struct {
			Success bool `json:"success"`
		} `json:"commentCreate"`
	}
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return fmt.Errorf("%s: %w", api.ErrLinearUnknownPayload, err)
	}
	if !payload.CommentCreate.Success {
		return fmt.Errorf("%s: commentCreate returned success=false", api.ErrLinearUnknownPayload)
	}
	return nil
}

func (c *LinearClient) findIssueWorkflowStateID(ctx context.Context, issueID string, preferredStates []string) (string, string, error) {
	byName := make(map[string]struct {
		id   string
		name string
	})
	var after *string

	for {
		variables := map[string]interface{}{"issueID": issueID}
		if after != nil {
			variables["after"] = *after
		}

		respBody, err := c.doGraphQL(ctx, c.buildIssueWorkflowStatesQuery(), variables)
		if err != nil {
			return "", "", err
		}

		var payload struct {
			Issue *struct {
				Team struct {
					States struct {
						Nodes []struct {
							ID   string `json:"id"`
							Name string `json:"name"`
						} `json:"nodes"`
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
					} `json:"states"`
				} `json:"team"`
			} `json:"issue"`
		}
		if err := json.Unmarshal(respBody, &payload); err != nil {
			return "", "", fmt.Errorf("%s: %w", api.ErrLinearUnknownPayload, err)
		}
		if payload.Issue == nil {
			return "", "", fmt.Errorf("%s: issue %q not found", api.ErrLinearUnknownPayload, issueID)
		}

		for _, state := range payload.Issue.Team.States.Nodes {
			name := strings.TrimSpace(state.Name)
			if state.ID == "" || name == "" {
				continue
			}
			byName[strings.ToLower(name)] = struct {
				id   string
				name string
			}{id: state.ID, name: name}
		}

		if !payload.Issue.Team.States.PageInfo.HasNextPage {
			break
		}
		if payload.Issue.Team.States.PageInfo.EndCursor == "" {
			return "", "", fmt.Errorf("%s: workflow states hasNextPage true but endCursor missing", api.ErrLinearMissingEndCursor)
		}
		after = &payload.Issue.Team.States.PageInfo.EndCursor
	}

	for _, preferred := range preferredStates {
		if state, ok := byName[strings.ToLower(preferred)]; ok {
			return state.id, state.name, nil
		}
	}
	return "", "", fmt.Errorf("%s: none of preferred completion states exist: %s", api.ErrLinearUnknownPayload, strings.Join(preferredStates, ", "))
}

func (c *LinearClient) findIssueTeamStateID(ctx context.Context, issueID string, state string) (string, error) {
	respBody, err := c.doGraphQL(ctx, c.buildIssueTeamStatesQuery(), map[string]interface{}{
		"issueID": issueID,
	})
	if err != nil {
		return "", err
	}

	var payload struct {
		Issue struct {
			Team struct {
				States struct {
					Nodes []struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"states"`
			} `json:"team"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return "", fmt.Errorf("%s: %w", api.ErrLinearUnknownPayload, err)
	}

	for _, node := range payload.Issue.Team.States.Nodes {
		if strings.EqualFold(node.Name, state) && node.ID != "" {
			return node.ID, nil
		}
	}
	return "", fmt.Errorf("%s: Linear state %q not found for issue %s", api.ErrLinearUnknownPayload, state, issueID)
}

func normalizeStatePreferences(states []string) []string {
	seen := make(map[string]struct{}, len(states))
	out := make([]string, 0, len(states))
	for _, state := range states {
		state = strings.TrimSpace(state)
		if state == "" {
			continue
		}
		key := strings.ToLower(state)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, state)
	}
	return out
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
		body := c.redactSecrets(strings.TrimSpace(string(respBytes)))
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
			msgs[i] = c.redactSecrets(e.Message)
		}
		return nil, fmt.Errorf("%s: %s", api.ErrLinearGraphQLErrors, strings.Join(msgs, "; "))
	}

	return respPayload.Data, nil
}

func (c *LinearClient) redactSecrets(text string) string {
	apiKey := strings.TrimSpace(c.cfg.APIKey)
	if text == "" || len(apiKey) < 4 {
		return text
	}
	return strings.ReplaceAll(text, apiKey, "********")
}

func (c *LinearClient) buildCommentCreateMutation() string {
	return `mutation CreateComment($issueID: String!, $body: String!) {
  commentCreate(input: { issueId: $issueID, body: $body }) {
    success
  }
}`
}

func (c *LinearClient) buildIssueTeamStatesQuery() string {
	return `query IssueTeamStates($issueID: String!) {
  issue(id: $issueID) {
    team {
      states(first: 100) {
        nodes {
          id
          name
        }
      }
    }
  }
}`
}

func (c *LinearClient) buildIssueWorkflowStatesQuery() string {
	return `query IssueWorkflowStates($issueID: String!, $after: String) {
  issue(id: $issueID) {
    team {
      states(first: 100, after: $after) {
        nodes {
          id
          name
        }
        pageInfo {
          hasNextPage
          endCursor
        }
      }
    }
  }
}`
}

func (c *LinearClient) buildIssueUpdateMutation() string {
	return `mutation UpdateIssueState($issueID: String!, $stateID: String!) {
  issueUpdate(id: $issueID, input: { stateId: $stateID }) {
    success
    issue {
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
			if !strings.EqualFold(strings.TrimSpace(rel.Type), "blocks") {
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
