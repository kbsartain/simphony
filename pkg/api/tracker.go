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
}
