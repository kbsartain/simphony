package api

import "context"

// Tracker is the issue tracker adapter interface.
type Tracker interface {
	// FetchCandidateIssues returns issues in configured active states.
	FetchCandidateIssues(ctx context.Context) ([]Issue, error)

	// FetchIssuesByStates returns issues in the given states (used for startup cleanup).
	FetchIssuesByStates(ctx context.Context, states []string) ([]Issue, error)

	// FetchIssueStatesByIDs returns current states for specific issue IDs (used for reconciliation).
	FetchIssueStatesByIDs(ctx context.Context, ids []string) (map[string]Issue, error)

	// MoveIssueToFirstAvailableState moves an issue to the first available tracker state
	// from the ordered preferred state names and returns the updated issue.
	MoveIssueToFirstAvailableState(ctx context.Context, issueID string, preferredStates []string) (Issue, error)

	// MoveIssueToState moves an issue to the named tracker state and returns the updated issue.
	MoveIssueToState(ctx context.Context, issueID string, state string) (Issue, error)

	// TransitionIssueState moves an issue to the named tracker state and returns the updated issue.
	TransitionIssueState(ctx context.Context, issue Issue, state string) (Issue, error)

	// AddIssueComment posts a comment to an issue.
	AddIssueComment(ctx context.Context, issue Issue, body string) error
}
