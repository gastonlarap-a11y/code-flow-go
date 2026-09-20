package providers

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// One pull request, one host, whichever host it is.
//
// The interface exists because every dispatching command asks the same six questions and only the
// answers differ — and because the review pipeline that follows has to post findings without
// knowing which host it is posting to. Two real implementations, no third: the rule for an
// interface in this repository is two implementations or a test seam, and this has both.

// PullRequestHost is a repository on a host, already addressed and already carrying its credential.
type PullRequestHost interface {
	// Provider names which host this is, for the wire and for the summary the panel shows.
	Provider() Provider

	List(ctx context.Context) ([]PullRequestSummary, error)
	Get(ctx context.Context, number int64) (PullRequestSummary, error)
	Create(ctx context.Context, pr NewPullRequest) (PullRequestSummary, error)
	Threads(ctx context.Context, number int64) ([]PrCommentThread, error)
	Decision(ctx context.Context, number int64) (string, error)
	// Diff is the pull request's unified diff as the host itself renders it — what makes a review
	// possible with no clone at all.
	Diff(ctx context.Context, number int64) (string, error)
	// Act approves, requests changes on, or closes the pull request. It does not re-read it: the
	// caller does that, so the state it returns is the one the action produced.
	Act(ctx context.Context, number int64, action, comment string) error

	// ---- publishing a review ---------------------------------------------------------------

	// EnsureUnchanged refuses a batch whose anchors were computed against a commit that is no
	// longer the pull request's head (`XLANG-014`, closing GitHub's half of `BUG-REVIEW-a`).
	//
	// Called once per publish, before anything is written. An empty `analysedHead` — a run from
	// before the SHA was recorded — cannot be compared and is allowed through.
	EnsureUnchanged(ctx context.Context, number int64, analysedHead string) error

	// OpenThread starts a conversation on the pull request and answers its id, which is what a
	// later re-review replies into. `location` is nil for a comment that is not attached to a file.
	OpenThread(ctx context.Context, number int64, content string, location *CommentLocation) (int64, error)

	// Reply adds to an existing conversation. `resolved` also marks the thread done — on GitHub
	// through the GraphQL mutation, on Azure by setting the thread's status to fixed — and that
	// second call's failure is never reported: a thread left open is a worse review, not a failed
	// post.
	Reply(ctx context.Context, number, threadID int64, content string, resolved bool) error

	// DiscussionNewestFirst says which end of the conversation the summary has to go on so that it
	// reads first (`DIVERGENCE-PROV-d`). GitHub's conversation runs oldest-first, Azure's overview
	// newest-first, and posting at the same end on both is what once buried the summary under the
	// findings it introduces.
	DiscussionNewestFirst() bool
}

// CommentLocation is the file range a comment is attached to.
//
// The field names are the renderer's own camelCase: this shape arrives from the UI inside a
// selected finding and is passed straight through.
type CommentLocation struct {
	File      string `json:"file"`
	StartLine int64  `json:"startLine"`
	EndLine   int64  `json:"endLine"`
}

// The three actions the panel offers. The renderer sends these literals.
const (
	ActionApprove        = "approve"
	ActionRequestChanges = "request_changes"
	ActionClose          = "close"
)

// defaultRequestChangesComment is what a blank "request changes" body becomes on GitHub, which
// refuses the event without one. Spanish and `VERBATIM`: it is what the reviewer's own pull request
// will show, and it has been showing exactly this since 2.x.
const defaultRequestChangesComment = "Cambios solicitados desde CodeFlow."

// unknownActionError names what was asked for rather than failing silently — the renderer sends one
// of three literals and a fourth means the two sides have drifted.
func unknownActionError(action string) error {
	return fmt.Errorf("unknown PR action: %s", action) //nolint:err113 // the message is the contract
}

// ---- GitHub -------------------------------------------------------------------------------------

type gitHubHost struct {
	client GitHubClient
	owner  string
	repo   string
}

// NewGitHubHost binds a GitHub client to one repository.
func NewGitHubHost(client GitHubClient, owner, repo string) PullRequestHost {
	return gitHubHost{client: client, owner: owner, repo: repo}
}

func (h gitHubHost) Provider() Provider { return ProviderGitHub }

func (h gitHubHost) List(ctx context.Context) ([]PullRequestSummary, error) {
	return h.client.ListPullRequests(ctx, h.owner, h.repo)
}

func (h gitHubHost) Get(ctx context.Context, number int64) (PullRequestSummary, error) {
	return h.client.GetPullRequest(ctx, h.owner, h.repo, number)
}

func (h gitHubHost) Create(ctx context.Context, pr NewPullRequest) (PullRequestSummary, error) {
	return h.client.CreatePullRequest(ctx, h.owner, h.repo, pr)
}

func (h gitHubHost) Threads(ctx context.Context, number int64) ([]PrCommentThread, error) {
	return h.client.ListCommentThreads(ctx, h.owner, h.repo, number)
}

func (h gitHubHost) Decision(ctx context.Context, number int64) (string, error) {
	return h.client.ViewerDecision(ctx, h.owner, h.repo, number)
}

func (h gitHubHost) Diff(ctx context.Context, number int64) (string, error) {
	return h.client.PullRequestDiff(ctx, h.owner, h.repo, number)
}

// Act submits a review or closes the pull request.
//
// A blank "request changes" body is substituted rather than sent empty: GitHub refuses the event
// without one, and the refusal a user would see says nothing about what to do.
func (h gitHubHost) Act(ctx context.Context, number int64, action, comment string) error {
	switch action {
	case ActionApprove:
		return h.client.SubmitReview(ctx, h.owner, h.repo, number, GitHubEventApprove, comment)
	case ActionRequestChanges:
		if strings.TrimSpace(comment) == "" {
			comment = defaultRequestChangesComment
		}
		return h.client.SubmitReview(ctx, h.owner, h.repo, number, GitHubEventRequestChanges, comment)
	case ActionClose:
		return h.client.ClosePullRequest(ctx, h.owner, h.repo, number)
	default:
		return unknownActionError(action)
	}
}

// ---- Azure DevOps -------------------------------------------------------------------------------

type azureHost struct {
	client  AzureClient
	project string
	repoID  string
}

// NewAzureHost binds an Azure client to one repository.
func NewAzureHost(client AzureClient, project, repoID string) PullRequestHost {
	return azureHost{client: client, project: project, repoID: repoID}
}

func (h azureHost) Provider() Provider { return ProviderAzure }

func (h azureHost) List(ctx context.Context) ([]PullRequestSummary, error) {
	return h.client.ListPullRequests(ctx, h.project, h.repoID)
}

func (h azureHost) Get(ctx context.Context, number int64) (PullRequestSummary, error) {
	pull, err := h.client.GetPullRequest(ctx, h.project, h.repoID, number)
	if err != nil {
		return PullRequestSummary{}, err
	}
	return pull.Summary, nil
}

func (h azureHost) Create(ctx context.Context, pr NewPullRequest) (PullRequestSummary, error) {
	return h.client.CreatePullRequest(ctx, h.project, h.repoID, pr)
}

func (h azureHost) Threads(ctx context.Context, number int64) ([]PrCommentThread, error) {
	return h.client.ListCommentThreads(ctx, h.project, h.repoID, number)
}

func (h azureHost) Decision(ctx context.Context, number int64) (string, error) {
	return h.client.ViewerDecision(ctx, h.project, h.repoID, number)
}

// Diff assembles the pull request's diff from its blobs, against the latest iteration: Azure has no
// endpoint that returns one as text, which is the whole reason `UnifiedPatch` exists.
func (h azureHost) Diff(ctx context.Context, number int64) (string, error) {
	iteration, err := h.client.LatestIterationID(ctx, h.project, h.repoID, number)
	if err != nil {
		return "", err
	}
	return h.client.PullRequestDiff(ctx, h.project, h.repoID, number, iteration)
}

// Act votes or abandons.
//
// Azure has no "review" to submit and no comment to carry with a vote, so the comment the caller
// passed is not sent anywhere — unlike GitHub, where it becomes the review's body. That asymmetry
// is the two hosts' own (`DIVERGENCE-PROV-a`), not something this port introduces.
func (h azureHost) Act(ctx context.Context, number int64, action, _ string) error {
	switch action {
	case ActionApprove:
		return h.client.SetReviewerVote(ctx, h.project, h.repoID, number, AzureVoteApproved)
	case ActionRequestChanges:
		return h.client.SetReviewerVote(ctx, h.project, h.repoID, number, AzureVoteRejected)
	case ActionClose:
		return h.client.AbandonPullRequest(ctx, h.project, h.repoID, number)
	default:
		return unknownActionError(action)
	}
}

// ---- resolving a host ---------------------------------------------------------------------------

// ErrNoADOPAT is what a call against an unconnected organisation answers.
var ErrNoADOPAT = errors.New("No Azure DevOps token saved for this organisation") //nolint:staticcheck // ST1005: VERBATIM

// HostForProject resolves a project row to the host it is linked to, with its credential.
//
// Exported because the review pipeline publishes through the same seam the commands here dispatch
// through: one resolution, so a review can never post to a host a listing would not have read from.
//
// Two failures, told apart because the user does different things about them: the project is not
// linked to anything (`ErrNotLinked`), or it is linked to a host they have not connected.
func (d Deps) HostForProject(ctx context.Context, projectID string) (PullRequestHost, error) {
	return d.hostForProject(ctx, projectID)
}

// HostForLink resolves a pasted pull-request URL to its host, and hands back the parsed link so the
// caller knows which pull request it is.
func (d Deps) HostForLink(ctx context.Context, rawURL string) (PullRequestHost, PRLink, error) {
	hosts, err := d.knownGitHubHosts(ctx)
	if err != nil {
		return nil, PRLink{}, err
	}
	link, ok := ParsePRLink(rawURL, hosts)
	if !ok {
		return nil, PRLink{}, ErrUnreadableLink
	}
	host, err := d.hostForLink(ctx, link)
	if err != nil {
		return nil, PRLink{}, err
	}
	return host, link, nil
}

func (d Deps) hostForProject(ctx context.Context, projectID string) (PullRequestHost, error) {
	project, err := d.Projects.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}

	linked, err := LinkedRepoFor(project)
	if err != nil {
		return nil, err
	}

	switch linked.Provider {
	case ProviderGitHub:
		client, err := d.gitHubFor(linked.GitHub.Host)
		if err != nil {
			return nil, err
		}
		return NewGitHubHost(client, linked.GitHub.Owner, linked.GitHub.Repo), nil

	default:
		client, err := d.azureFor(linked.Azure.Org)
		if err != nil {
			return nil, err
		}
		return NewAzureHost(client, linked.Azure.Project, linked.Azure.Repo), nil
	}
}

// azureFor builds a client for an organisation, refusing before any request when nothing is saved.
func (d Deps) azureFor(org string) (AzureClient, error) {
	pat, err := d.Credentials.ADOPAT(org)
	switch {
	case errors.Is(err, ErrNoCredential):
		return AzureClient{}, ErrNoADOPAT
	case err != nil:
		return AzureClient{}, err
	case strings.TrimSpace(pat) == "":
		return AzureClient{}, ErrNoADOPAT
	}
	return NewAzureClient(d.HTTP, org, pat), nil
}

// hostForLink resolves a pasted link to a host, with its credential.
//
// The Azure half also recovers the project's and the repository's real names: a link that carries
// GUIDs — Azure's own notification mails are full of them — addresses the API fine but matches no
// local clone, whose remote spells out names.
func (d Deps) hostForLink(ctx context.Context, link PRLink) (PullRequestHost, error) {
	if link.Provider == ProviderGitHub {
		client, err := d.gitHubFor(link.GitHub.Host)
		if err != nil {
			return nil, err
		}
		return NewGitHubHost(client, link.GitHub.Owner, link.GitHub.Repo), nil
	}

	client, err := d.azureFor(link.Azure.Org)
	if err != nil {
		return nil, err
	}

	pull, err := client.GetPullRequest(ctx, link.Azure.Project, link.Azure.Repo, link.Number)
	if err != nil {
		return nil, err
	}
	return NewAzureHost(client, pull.ProjectName, pull.RepoName), nil
}
