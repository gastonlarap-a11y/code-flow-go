package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/gastonlarap-a11y/code-flow/backend/activity"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// Projects is the project row this feature reads, the link columns it writes, and the one app
// setting it needs.
//
// Declared here rather than taken as `*workspaces.Store` so the scan can be tested without a
// database — the interesting cases are "resolves but has no token" and "two remotes, one
// connected", none of which need SQLite to be interesting. `*workspaces.Store` satisfies it as it
// stands; the method names are its own.
type Projects interface {
	GetProject(ctx context.Context, id string) (workspaces.Project, error)
	// ListAllProjects crosses workspaces on purpose: a pasted link names a repository and says
	// nothing about which workspace holds its checkout.
	ListAllProjects(ctx context.Context) ([]workspaces.Project, error)
	LinkProjectGitHub(ctx context.Context, id, host, owner, repo string) error
	LinkProjectADO(ctx context.Context, id, org, project, repoID string) error
	UnlinkProject(ctx context.Context, id string) error
	GetSetting(ctx context.Context, key string) (*string, error)
}

// Activity files a finished action in the project's history. Nil when the database did not open,
// which is also when the commands that need it are not registered.
type Activity interface {
	RecordJob(ctx context.Context, job activity.NewJob) (activity.JobEntry, error)
}

// Remote is one of a repository's configured remotes.
type Remote struct {
	Name string
	URL  string
}

// RemoteLister answers a repository's remotes, in the order git lists them.
//
// The implementation is `git.ListRemotes`, adapted by the composition root. This package does not
// import git: what it needs is "the URLs this repository points at", and going through the seam
// keeps a remote scan testable without a repository on disk.
type RemoteLister interface {
	ListRemotes(ctx context.Context, repoPath string) ([]Remote, error)
}

// Credentials answers whether a host's or an organisation's credential is already saved, and hands
// it over when it is.
//
// Asked in these terms rather than by key, because the key formats are `VERBATIM` contracts owned
// by the credential store (`10-security.md`) — they are what lets 3.0 read what 2.7.x wrote, and
// this package has no business spelling them. The two `Has*` questions stay separate from the two
// reads because the scan asks them for every remote it meets: on macOS, reading a token can put a
// keychain prompt on screen, and "is this host connected" must not.
type Credentials interface {
	HasGitHubToken(host string) (bool, error)
	HasADOPAT(org string) (bool, error)
	GitHubToken(host string) (string, error)
	ADOPAT(org string) (string, error)
}

// Deps is what this feature needs from the composition root.
type Deps struct {
	Projects    Projects
	Remotes     RemoteLister
	Credentials Credentials
	Activity    Activity

	// HTTP is the one client the whole process shares. Nil falls back to the default one, which is
	// what a test that only exercises a parser wants.
	HTTP *http.Client
}

// What a credential reader reports, in this package's own terms.
//
// The composition root maps the credential store's own errors onto these two, which is what keeps
// the store's `VERBATIM` key formats in the one package that owns them while leaving the decision
// about the sentinel here, at the command boundary where it belongs (`XLANG-012`).
var (
	// ErrNoCredential: nothing is saved. Not a failure — the answer is "connect this host".
	ErrNoCredential = errors.New("no credential saved")
	// ErrCredentialRefused: the store was reached and said no — a locked keychain, a denied
	// prompt. The user can act on it, which is why it is told apart.
	ErrCredentialRefused = errors.New("the credential store refused access")
)

// ErrNoGitHubToken is what a call against an unconnected host answers, in the words the frontend
// shows: the panel turns it into an offer to open Settings.
var ErrNoGitHubToken = errors.New("No GitHub token saved for this host") //nolint:staticcheck // ST1005: VERBATIM

// gitHubFor builds a client for a host, refusing before any request when nothing is saved for it.
func (d Deps) gitHubFor(host string) (GitHubClient, error) {
	token, err := d.Credentials.GitHubToken(host)
	switch {
	case errors.Is(err, ErrNoCredential):
		return GitHubClient{}, ErrNoGitHubToken
	case err != nil:
		return GitHubClient{}, err
	case strings.TrimSpace(token) == "":
		// A stored-but-empty token is the same state as none: the credential store keeps "present"
		// and "non-empty" apart, and a request with an empty Bearer would just 401.
		return GitHubClient{}, ErrNoGitHubToken
	}
	return NewGitHubClient(d.HTTP, host, token), nil
}

// asCommandError puts the refused-credential sentinel at position 0, and nowhere else.
//
// Applied in a handler, never at the read site: a refusal that reached this function is being
// reported to the renderer, which matches the prefix with `startsWith` and offers "reconnect"
// instead of a retry that would fail identically.
func asCommandError(err error) error {
	if err == nil || !errors.Is(err, ErrCredentialRefused) {
		return err
	}
	message := strings.TrimPrefix(err.Error(), sentinel.CredentialRefused)
	return errors.New(sentinel.CredentialRefused + message) //nolint:staticcheck // ST1005: VERBATIM sentinel
}

// Register adds the provider commands ported so far.
//
// Two of the names 2.x registered here are gone from the backend and are not missing: the renderer
// does `open_repo_in_browser` itself, as `repo_web_url` plus the shell's open, and
// `open_external_url` never reaches Go at all now — a capture-phase listener routes external links
// to the host service (§7.4).
func Register(r *bridge.Registry, deps Deps) {
	// The parameter is `projectId` here and `id` on the manual links — the renderer's own
	// inconsistency, carried over character for character, because a mismatch is a runtime
	// "missing required parameter" and nothing at build time.
	r.Add("repo_web_url", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, err := bridge.Arg[string](p, "projectId")
		if err != nil {
			return nil, err
		}
		return deps.RepoWebURL(ctx, projectID)
	})

	r.Add("auto_link_project", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, err := bridge.Arg[string](p, "projectId")
		if err != nil {
			return nil, err
		}
		return deps.AutoLink(ctx, projectID)
	})

	r.Add("link_project_github", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		owner, err := bridge.Arg[string](p, "githubOwner")
		if err != nil {
			return nil, err
		}
		repo, err := bridge.Arg[string](p, "githubRepo")
		if err != nil {
			return nil, err
		}
		host, err := bridge.Arg[string](p, "githubHost")
		if err != nil {
			return nil, err
		}
		return nil, deps.Projects.LinkProjectGitHub(ctx, id, host, owner, repo)
	})

	r.Add("link_project_ado", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		org, err := bridge.Arg[string](p, "adoOrg")
		if err != nil {
			return nil, err
		}
		project, err := bridge.Arg[string](p, "adoProject")
		if err != nil {
			return nil, err
		}
		repoID, err := bridge.Arg[string](p, "adoRepoId")
		if err != nil {
			return nil, err
		}
		return nil, deps.Projects.LinkProjectADO(ctx, id, org, project, repoID)
	})

	// Clears whichever of the two links the project has — the renderer offers one button, because
	// a project is linked to one host at a time as far as the user is concerned.
	r.Add("unlink_project", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		return nil, deps.Projects.UnlinkProject(ctx, id)
	})

	// Whose account is this token? Settings calls it right after a token is pasted, which is what
	// makes a typo'd token a message at the moment of typing rather than a failed review later.
	r.Add("github_authenticated_user", func(ctx context.Context, p bridge.Params) (any, error) {
		host, err := bridge.Arg[string](p, "host")
		if err != nil {
			return nil, err
		}
		client, err := deps.gitHubFor(host)
		if err != nil {
			return nil, asCommandError(err)
		}
		return client.AuthenticatedUser(ctx)
	})

	registerAzureDialogs(r, deps)
	registerPullRequests(r, deps)
	registerPRLinks(r, deps)
}

// registerAzureDialogs adds the two lookups the manual-link dialog makes.
func registerAzureDialogs(r *bridge.Registry, deps Deps) {
	r.Add("ado_list_projects", func(ctx context.Context, p bridge.Params) (any, error) {
		org, err := bridge.Arg[string](p, "org")
		if err != nil {
			return nil, err
		}
		client, err := deps.azureFor(org)
		if err != nil {
			return nil, asCommandError(err)
		}
		return client.ListProjects(ctx)
	})

	r.Add("ado_list_repos", func(ctx context.Context, p bridge.Params) (any, error) {
		org, err := bridge.Arg[string](p, "org")
		if err != nil {
			return nil, err
		}
		project, err := bridge.Arg[string](p, "project")
		if err != nil {
			return nil, err
		}
		client, err := deps.azureFor(org)
		if err != nil {
			return nil, asCommandError(err)
		}
		return client.ListRepos(ctx, project)
	})
}

// registerPullRequests adds the commands that address a pull request through a **project**, each
// dispatching to whichever host the project is linked to (REVIEW-001).
func registerPullRequests(r *bridge.Registry, deps Deps) {
	// A host failure here is marked for the sidebar: it is the one call the panel makes on every
	// selection, so an expired credential surfaces here first (`XLANG-012`).
	r.Add("list_pull_requests", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, err := bridge.Arg[string](p, "projectId")
		if err != nil {
			return nil, err
		}
		host, err := deps.hostForProject(ctx, projectID)
		if err != nil {
			return nil, asCommandError(err)
		}
		return withHostSentinels(host.List(ctx))
	})

	r.Add("list_pr_comment_threads", func(ctx context.Context, p bridge.Params) (any, error) {
		host, prID, err := deps.hostAndPR(ctx, p)
		if err != nil {
			return nil, err
		}
		return withHostSentinels(host.Threads(ctx, prID))
	})

	r.Add("pr_review_decision", func(ctx context.Context, p bridge.Params) (any, error) {
		host, prID, err := deps.hostAndPR(ctx, p)
		if err != nil {
			return nil, err
		}
		return withHostSentinels(host.Decision(ctx, prID))
	})

	r.Add("create_pull_request", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, err := bridge.Arg[string](p, "projectId")
		if err != nil {
			return nil, err
		}
		title, err := bridge.Arg[string](p, "title")
		if err != nil {
			return nil, err
		}
		description, err := bridge.Arg[string](p, "description")
		if err != nil {
			return nil, err
		}
		source, err := bridge.Arg[string](p, "sourceBranch")
		if err != nil {
			return nil, err
		}
		target, err := bridge.Arg[string](p, "targetBranch")
		if err != nil {
			return nil, err
		}
		draft, err := bridge.Arg[bool](p, "draft")
		if err != nil {
			return nil, err
		}

		host, err := deps.hostForProject(ctx, projectID)
		if err != nil {
			return nil, asCommandError(err)
		}
		return withHostSentinels(host.Create(ctx, NewPullRequest{
			Title:        title,
			Description:  description,
			SourceBranch: source,
			TargetBranch: target,
			Draft:        draft,
		}))
	})

	// Every parameter is read before anything is resolved: a missing one is a disagreement between
	// the two sides, and reporting it as "this project isn't linked" would send the reader looking
	// in the wrong place.
	r.Add("act_on_pull_request", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, err := bridge.Arg[string](p, "projectId")
		if err != nil {
			return nil, err
		}
		prID, err := bridge.Arg[int64](p, "prId")
		if err != nil {
			return nil, err
		}
		action, err := bridge.Arg[string](p, "action")
		if err != nil {
			return nil, err
		}
		body, err := bridge.OptionalArg[string](p, "body")
		if err != nil {
			return nil, err
		}

		host, err := deps.hostForProject(ctx, projectID)
		if err != nil {
			return nil, asCommandError(err)
		}
		return deps.actOnProjectPR(ctx, host, projectID, prID, action, body)
	})
}

// registerPRLinks adds the commands that address a pull request through a **pasted URL**, with no
// project and no local checkout behind them.
func registerPRLinks(r *bridge.Registry, deps Deps) {
	r.Add("resolve_pr_link", func(ctx context.Context, p bridge.Params) (any, error) {
		url, err := bridge.Arg[string](p, "url")
		if err != nil {
			return nil, err
		}
		return deps.ResolvePRLink(ctx, url)
	})

	// The three reads below **throw** rather than reporting a state: the modal has already
	// resolved the link by the time it calls them, so a failure here is a failure, not a screen.
	r.Add("pr_link_pull_request", func(ctx context.Context, p bridge.Params) (any, error) {
		host, link, err := deps.hostFromLinkParam(ctx, p)
		if err != nil {
			return nil, err
		}
		return withHostSentinels(host.Get(ctx, link.Number))
	})

	r.Add("pr_link_comment_threads", func(ctx context.Context, p bridge.Params) (any, error) {
		host, link, err := deps.hostFromLinkParam(ctx, p)
		if err != nil {
			return nil, err
		}
		return withHostSentinels(host.Threads(ctx, link.Number))
	})

	r.Add("pr_link_decision", func(ctx context.Context, p bridge.Params) (any, error) {
		host, link, err := deps.hostFromLinkParam(ctx, p)
		if err != nil {
			return nil, err
		}
		return withHostSentinels(host.Decision(ctx, link.Number))
	})

	// No Activity row here, unlike its project-linked twin: that table belongs to a project and a
	// link review has none.
	r.Add("act_on_pr_link", func(ctx context.Context, p bridge.Params) (any, error) {
		host, link, err := deps.hostFromLinkParam(ctx, p)
		if err != nil {
			return nil, err
		}
		action, err := bridge.Arg[string](p, "action")
		if err != nil {
			return nil, err
		}
		body, err := bridge.OptionalArg[string](p, "body")
		if err != nil {
			return nil, err
		}

		if err := host.Act(ctx, link.Number, action, optionalText(body)); err != nil {
			return nil, hostSentinels(err)
		}
		// Re-read, so the caller sees the state the action produced rather than an optimistic guess.
		return withHostSentinels(host.Get(ctx, link.Number))
	})
}

// actOnProjectPR performs the action and files it in the project's history (REVIEW-037).
//
// The Activity write is **not** best-effort: a database failure fails the command even though the
// host-side action already went through. That is 2.x's behaviour and it is the honest one — the
// panel would otherwise show an action that left no trace. The row is only ever written on success:
// a failed approve reaches none of this.
func (d Deps) actOnProjectPR(
	ctx context.Context,
	host PullRequestHost,
	projectID string,
	prID int64,
	action string,
	body *string,
) (any, error) {
	if err := host.Act(ctx, prID, action, optionalText(body)); err != nil {
		return nil, hostSentinels(err)
	}

	pull, err := host.Get(ctx, prID)
	if err != nil {
		return nil, hostSentinels(err)
	}

	entry, err := d.Activity.RecordJob(ctx, activity.NewJob{
		ID:        uuid.NewString(),
		ProjectID: projectID,
		Kind:      "pr-action",
		Label:     fmt.Sprintf("#%d %s", pull.ID, pull.Title),
		Status:    "done",
		Result:    &pull.URL,
		Meta: fmt.Sprintf(`{"prId":%d,"prTitle":%s,"action":%s}`,
			pull.ID, jsonText(pull.Title), jsonText(action)),
	})
	if err != nil {
		return nil, err
	}

	return PrActionOutcome{PR: pull, Activity: entry}, nil
}

// PrActionOutcome is what a decision left behind: the pull request as the host now reports it, and
// the Activity row the action was filed under.
type PrActionOutcome struct {
	PR       PullRequestSummary `json:"pr"`
	Activity activity.JobEntry  `json:"activity"`
}

// hostAndPR reads the two parameters every project-linked pull-request command takes.
func (d Deps) hostAndPR(ctx context.Context, p bridge.Params) (PullRequestHost, int64, error) {
	projectID, err := bridge.Arg[string](p, "projectId")
	if err != nil {
		return nil, 0, err
	}
	prID, err := bridge.Arg[int64](p, "prId")
	if err != nil {
		return nil, 0, err
	}
	host, err := d.hostForProject(ctx, projectID)
	if err != nil {
		return nil, 0, asCommandError(err)
	}
	return host, prID, nil
}

// hostFromLinkParam re-parses the `url` parameter and resolves its host.
func (d Deps) hostFromLinkParam(ctx context.Context, p bridge.Params) (PullRequestHost, PRLink, error) {
	url, err := bridge.Arg[string](p, "url")
	if err != nil {
		return nil, PRLink{}, err
	}
	host, link, err := d.HostForLink(ctx, url)
	if err != nil {
		return nil, PRLink{}, asCommandError(hostSentinels(err))
	}
	return host, link, nil
}

// ErrUnreadableLink is what the link commands answer for a URL that is not a pull request.
var ErrUnreadableLink = errors.New("That isn't a pull-request link CodeFlow can read") //nolint:staticcheck // ST1005: VERBATIM

// withHostSentinels is the generic form of hostSentinels, for a handler that returns a value.
func withHostSentinels[T any](value T, err error) (any, error) {
	if err != nil {
		return nil, hostSentinels(err)
	}
	return value, nil
}

// hostSentinels puts a host failure's sentinel at position 0, and only the two that have one.
//
// Applied here, at the command boundary, and never at the call site: a sentinel inside a
// review-posting summary is a sentence a person reads, and `XLANG-012` exists because that
// happened.
func hostSentinels(err error) error {
	if err == nil {
		return nil
	}

	var github *GitHubError
	if errors.As(err, &github) && github.SelfApproval {
		return errors.New(sentinel.SelfApproval + err.Error()) //nolint:staticcheck // ST1005: VERBATIM sentinel
	}

	var azure *AzureError
	if errors.As(err, &azure) && azure.Unauthorized {
		return errors.New(sentinel.CredentialRefused + err.Error()) //nolint:staticcheck // ST1005: VERBATIM sentinel
	}
	return asCommandError(err)
}

func optionalText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// jsonText quotes a string for the hand-built meta object, so a title with a quote in it does not
// produce invalid JSON.
func jsonText(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}
