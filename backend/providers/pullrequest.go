package providers

// The wire types both hosts answer in.
//
// One set, not one per host: they are what the renderer already consumes, so GitHub produces the
// exact same shapes rather than a parallel set the UI would have to learn. 2.x kept them in the
// Azure file and re-exported them into GitHub's; here they sit beside both, which is the same
// decision without the import.

// PullRequestSummary is a pull request as the sidebar and the review panel read it.
type PullRequestSummary struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Status is one of the four buckets below — never the host's own vocabulary.
	Status       string   `json:"status"`
	SourceBranch string   `json:"source_branch"`
	TargetBranch string   `json:"target_branch"`
	Author       string   `json:"author"`
	CreatedAt    string   `json:"created_at"`
	URL          string   `json:"url"`
	Provider     Provider `json:"provider"`
}

// The four buckets the sidebar groups by. Both hosts collapse their own status vocabulary into
// these, and the renderer's `PullRequestSummary["status"]` is typed as exactly this union.
const (
	StatusOpen   = "open"
	StatusDraft  = "draft"
	StatusMerged = "merged"
	StatusClosed = "closed"
)

// PrThreadComment is one comment inside a thread.
type PrThreadComment struct {
	Author        string `json:"author"`
	Content       string `json:"content"`
	PublishedDate string `json:"published_date"`
}

// PrCommentThread is a conversation on a pull request, anchored to a file range or not.
//
// The three location fields are pointers because a PR-level thread has no location at all, and the
// renderer branches on the null to decide whether to show a file badge.
type PrCommentThread struct {
	ID        int64             `json:"id"`
	FilePath  *string           `json:"file_path"`
	StartLine *int64            `json:"start_line"`
	EndLine   *int64            `json:"end_line"`
	Comments  []PrThreadComment `json:"comments"`
}

// The three decisions a viewer can already have recorded on a pull request. Each host collapses its
// own model into these — GitHub's review events, Azure's numeric votes — and nothing translates one
// host's raw verdict into the other's (`DIVERGENCE-PROV-a`).
const (
	DecisionApproved         = "approved"
	DecisionChangesRequested = "changes_requested"
	DecisionNone             = "none"
)

// NewPullRequest is what creating one needs, on either host.
type NewPullRequest struct {
	Title        string
	Description  string
	SourceBranch string
	TargetBranch string
	Draft        bool
}
