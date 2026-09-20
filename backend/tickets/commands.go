package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/activity"
	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/providers"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// The seventeen commands this feature answers.
//
// Sixteen read. The seventeenth, `comment_ticket`, is the whole write surface, and
// `TicketCommandsTests` asserts both halves — that the comment is registered, and that no transition
// verb is.

// Credentials hands over an organisation's PAT. Declared at the consumer and asked in these terms
// rather than by key, because the key formats are `VERBATIM` contracts owned by the credential store
// (`10-security.md`).
type Credentials interface {
	ADOPAT(org string) (string, error)
}

// Reviewer is what this feature asks of the AI layer: the two orchestrations `review_changes`
// dispatches between.
type Reviewer interface {
	AnalyzeChanges(ctx context.Context, runID string, request ai.AnalyzeRequest) (ai.Result, error)
	ReviewAgainstTicket(ctx context.Context, runID string, request ai.TicketReviewRequest) (ai.Result, error)
}

// Activity files a finished review in the project's history.
type Activity interface {
	RecordJob(ctx context.Context, job activity.NewJob) (activity.JobEntry, error)
}

// Deps is what this feature needs from the composition root.
type Deps struct {
	Store      *Store
	Workspaces Workspaces
	// Prompts reads the workspace's two methodologies and its enabled contexts and MCP servers.
	Prompts     Prompts
	Credentials Credentials
	AI          Reviewer
	Activity    Activity
	Paths       platform.Paths

	// HTTP is the one client the whole process shares.
	HTTP *http.Client
	// Now is the clock a stored review's timestamp reads. A seam, so a test can pin it.
	Now func() time.Time
}

// Prompts is the workspace configuration a review runs under. The same three reads the pull-request
// pipeline makes, asked for here in this feature's own terms.
type Prompts interface {
	GetWorkspacePrompt(ctx context.Context, workspaceID, kind string) (string, error)
	ListReviewContexts(ctx context.Context, workspaceID string) ([]workspaces.ReviewContext, error)
	ListMCPs(ctx context.Context, workspaceID string) ([]workspaces.MCP, error)
}

// ErrNoADOPAT is what a call against an organisation with no saved PAT answers, in the words the
// frontend shows.
var ErrNoADOPAT = errors.New("No Azure DevOps PAT saved for this organisation") //nolint:staticcheck // ST1005: VERBATIM

// clientFor builds a Boards client for an organisation, refusing before any request when nothing is
// saved for it.
func (d Deps) clientFor(org string) (providers.AzureClient, error) {
	pat, err := d.Credentials.ADOPAT(org)
	switch {
	case errors.Is(err, providers.ErrNoCredential):
		return providers.AzureClient{}, ErrNoADOPAT
	case err != nil:
		return providers.AzureClient{}, err
	case strings.TrimSpace(pat) == "":
		// Stored-but-empty is the same state as none: a request with an empty Basic password would
		// just 401, and the sidebar would offer a retry that fails identically.
		return providers.AzureClient{}, ErrNoADOPAT
	}
	return providers.NewAzureClient(d.HTTP, org, pat), nil
}

func (d Deps) setting(ctx context.Context, key string) *string {
	value, err := d.Workspaces.GetSetting(ctx, key)
	if err != nil {
		return nil
	}
	return value
}

// ticketsRoot is where mirrors live: the `tickets_root_dir` setting, or `{base}/tickets` (WI-002).
func (d Deps) ticketsRoot(ctx context.Context) string {
	return Root(d.setting(ctx, "tickets_root_dir"), d.Paths.Tickets())
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// reviewConfiguration is what a workspace says a review should run with.
type reviewConfiguration struct {
	Template  string
	Contexts  []ai.ReviewContext
	MCPConfig string
}

// reviewConfig reads the workspace's methodology for one kind, its enabled contexts and its MCP
// servers. A blank methodology means the built-in one, not an empty prompt.
func (d Deps) reviewConfig(ctx context.Context, workspaceID, kind string) (reviewConfiguration, error) {
	if d.Prompts == nil || workspaceID == "" {
		return reviewConfiguration{}, nil
	}

	template, err := d.Prompts.GetWorkspacePrompt(ctx, workspaceID, kind)
	if err != nil {
		return reviewConfiguration{}, err
	}
	stored, err := d.Prompts.ListReviewContexts(ctx, workspaceID)
	if err != nil {
		return reviewConfiguration{}, err
	}
	mcps, err := d.Prompts.ListMCPs(ctx, workspaceID)
	if err != nil {
		return reviewConfiguration{}, err
	}

	contexts := make([]ai.ReviewContext, 0, len(stored))
	for _, context := range stored {
		if context.Enabled {
			contexts = append(contexts, ai.ReviewContext{Name: context.Name, Content: context.Content})
		}
	}
	return reviewConfiguration{
		Template: template,
		Contexts: contexts,
		// Blank when no server is enabled, which is what leaves the flag off the engine's command
		// line entirely — the same file the pull-request review writes, from the same code.
		MCPConfig: workspaces.WriteMCPConfig(d.Paths.WorkspaceMCPConfig(workspaceID), mcps),
	}, nil
}

// Register adds the work-item commands.
func Register(r *bridge.Registry, deps Deps) {
	registerAccount(r, deps)
	registerCache(r, deps)
	registerLinks(r, deps)
	registerBoard(r, deps)
	registerReviews(r, deps)
}

// registerAccount adds the two commands that decide which board a repository's work items come from.
func registerAccount(r *bridge.Registry, deps Deps) {
	// Writing both columns together, because a project name without the organisation it was listed
	// from addresses nothing — the renderer clears the project when the organisation changes and
	// sends both in one call.
	r.Add("update_workspace_ticket_account", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		org, err := bridge.OptionalArg[string](p, "org")
		if err != nil {
			return nil, err
		}
		project, err := bridge.OptionalArg[string](p, "project")
		if err != nil {
			return nil, err
		}
		return nil, deps.Workspaces.SetWorkspaceTicketAccount(ctx, workspaceID, org, project)
	})

	r.Add("resolve_ticket_account", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, err := bridge.Arg[string](p, "projectId")
		if err != nil {
			return nil, err
		}
		return deps.ResolveAccount(ctx, projectID)
	})
}

// ResolveAccount answers which account a project's work items come from (WI-005).
//
// An unknown project id **throws**: that is a caller error, not a missing account, and answering
// `none` for it would send the user to a settings screen that cannot fix anything.
func (d Deps) ResolveAccount(ctx context.Context, projectID string) (Account, error) {
	project, err := d.Workspaces.GetProject(ctx, projectID)
	if err != nil {
		return Account{}, err
	}

	workspace, err := d.Workspaces.GetWorkspace(ctx, project.WorkspaceID)
	if err != nil {
		return Account{}, err
	}

	return ResolveAccount(project, workspace, ADOConnections(d.setting(ctx, "ado_connections"))), nil
}

// registerCache adds the commands that read and refresh the cached copy of a work item.
func registerCache(r *bridge.Registry, deps Deps) {
	r.Add("sync_ticket", func(ctx context.Context, p bridge.Params) (any, error) {
		org, project, externalID, err := boardTarget(p)
		if err != nil {
			return nil, err
		}
		return asCommandError(deps.Sync(ctx, org, project, externalID))
	})

	// Null rather than an error for a ticket that is not cached: the renderer types it as
	// `Ticket | null` and renders an empty pane for the null.
	r.Add("get_ticket", func(ctx context.Context, p bridge.Params) (any, error) {
		ticketID, err := bridge.Arg[string](p, "ticketId")
		if err != nil {
			return nil, err
		}
		ticket, err := deps.Store.Get(ctx, ticketID)
		if errors.Is(err, ErrNotFound) {
			return nil, nil //nolint:nilnil // `Ticket | null`: not cached is a state, not a failure
		}
		if err != nil {
			return nil, err
		}
		return ticket, nil
	})

	r.Add("list_tickets", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, err := bridge.Arg[string](p, "projectId")
		if err != nil {
			return nil, err
		}
		return deps.Store.List(ctx, projectID)
	})

	// Recomputed from the cached payload — **no network**, which is what makes it safe to call while
	// a picker is open.
	r.Add("get_ticket_criteria", func(ctx context.Context, p bridge.Params) (any, error) {
		ticketID, err := bridge.Arg[string](p, "ticketId")
		if err != nil {
			return nil, err
		}
		return deps.Criteria(ctx, ticketID)
	})
}

// Criteria recomputes what a cached ticket asks for (WI-007).
func (d Deps) Criteria(ctx context.Context, ticketID string) (Criteria, error) {
	ticket, err := d.Store.Get(ctx, ticketID)
	if errors.Is(err, ErrNotFound) {
		return NoCriteria(), nil
	}
	if err != nil {
		return Criteria{}, err
	}

	payload, err := d.Store.RawPayload(ctx, ticketID)
	if err != nil {
		return NoCriteria(), nil //nolint:nilerr // a ticket with no payload asks for nothing readable
	}

	var item cachedWorkItem
	if err := json.Unmarshal([]byte(payload), &item); err != nil {
		// A payload that will not parse is a ticket with no readable requirements, which the review
		// says out loud — not a failed command.
		return NoCriteria(), nil
	}
	return d.criteriaFromCache(ctx, ticket, item), nil
}

// registerLinks adds the commands that bind a branch to a work item, and the two parsers that
// suggest one.
func registerLinks(r *bridge.Registry, deps Deps) {
	r.Add("link_branch_ticket", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, branch, err := branchTarget(p)
		if err != nil {
			return nil, err
		}
		ticketID, err := bridge.Arg[string](p, "ticketId")
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.Link(ctx, projectID, branch, ticketID)
	})

	r.Add("unlink_branch_ticket", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, branch, err := branchTarget(p)
		if err != nil {
			return nil, err
		}
		return nil, deps.Store.Unlink(ctx, projectID, branch)
	})

	// Only the explicit link answers here. The name heuristic is a suggestion and never a link, so
	// no review is ever judged against a work item nobody chose (`WI-006`).
	r.Add("ticket_for_branch", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, branch, err := branchTarget(p)
		if err != nil {
			return nil, err
		}
		ticket, err := deps.Store.ForBranch(ctx, projectID, branch)
		if errors.Is(err, ErrNotFound) {
			return nil, nil //nolint:nilnil // `Ticket | null`: an unlinked branch is the ordinary case
		}
		if err != nil {
			return nil, err
		}
		return ticket, nil
	})

	// A pasted address names its own board and wins (`WI-019`): the organisation and project come
	// back from the URL, and the caller fills them from the workspace only for a bare id.
	r.Add("resolve_ticket_link", func(ctx context.Context, p bridge.Params) (any, error) {
		text, err := bridge.Arg[string](p, "text")
		if err != nil {
			return nil, err
		}
		address, ok := providers.ParseWorkItemLink(text)
		if !ok {
			return nil, nil //nolint:nilnil // `TicketLinkRef | null`: not an address is the answer
		}
		// `WorkItemAddress` already carries `TicketLinkRef`'s three fields under its own tags;
		// re-declaring the shape here would be a second wire contract able to drift from the first.
		return address, nil
	})

	r.Add("suggest_ticket_for_branch", func(_ context.Context, p bridge.Params) (any, error) {
		branch, err := bridge.Arg[string](p, "branch")
		if err != nil {
			return nil, err
		}
		suggestion := SuggestForBranch(branch)
		if suggestion == nil {
			return nil, nil //nolint:nilnil // `TicketSuggestion | null`: no guess is a valid answer
		}
		return suggestion, nil
	})
}

// registerBoard adds the three board reads the link dialog makes.
func registerBoard(r *bridge.Registry, deps Deps) {
	r.Add("list_sprint_tickets", func(ctx context.Context, p bridge.Params) (any, error) {
		org, err := bridge.Arg[string](p, "org")
		if err != nil {
			return nil, err
		}
		project, err := bridge.Arg[string](p, "project")
		if err != nil {
			return nil, err
		}
		team, err := bridge.OptionalArg[string](p, "team")
		if err != nil {
			return nil, err
		}
		return asCommandError(deps.SprintTickets(ctx, org, project, optionalText(team)))
	})

	r.Add("list_my_tickets", func(ctx context.Context, p bridge.Params) (any, error) {
		org, err := bridge.Arg[string](p, "org")
		if err != nil {
			return nil, err
		}
		project, err := bridge.Arg[string](p, "project")
		if err != nil {
			return nil, err
		}
		return asCommandError(deps.MyTickets(ctx, org, project))
	})

	// A batch of one id over the summary fields, deliberately **not** `sync_ticket`: this runs while
	// somebody is still typing, and syncing would write the cache, rewrite the mirror and download up
	// to sixteen megabytes of attachments on every keystroke.
	r.Add("preview_ticket", func(ctx context.Context, p bridge.Params) (any, error) {
		org, project, externalID, err := boardTarget(p)
		if err != nil {
			return nil, err
		}
		summary, err := deps.Preview(ctx, org, project, externalID)
		if err != nil {
			return nil, asCommandErrorOnly(err)
		}
		if summary == nil {
			return nil, nil //nolint:nilnil // `TicketSummary | null`: a half-typed id is no work item
		}
		return summary, nil
	})
}

// SprintTickets is the current sprint's work items — the picker's default list, because it is what
// the taskboard shows.
//
// Omitting the team uses the first one with a current iteration. Azure says which sprint is current
// itself, rather than leaving it to a date comparison against this machine's clock and time zone.
func (d Deps) SprintTickets(ctx context.Context, org, project, team string) ([]Summary, error) {
	client, err := d.clientFor(org)
	if err != nil {
		return nil, err
	}

	teams := []providers.AdoRef{{Name: team}}
	if strings.TrimSpace(team) == "" {
		teams, err = client.ListTeams(ctx, project)
		if err != nil {
			return nil, err
		}
	}

	for _, candidate := range teams {
		iterations, err := client.TeamIterations(ctx, project, candidate.Name)
		if err != nil {
			continue
		}
		for _, iteration := range iterations {
			if !iteration.Current() {
				continue
			}
			ids, err := client.IterationWorkItems(ctx, project, candidate.Name, iteration.ID)
			if err != nil {
				return nil, err
			}
			return d.summaries(ctx, client, project, ids)
		}
	}
	// No team has a current iteration: an empty list, not an error. A board between sprints is an
	// ordinary state, and the dialog's address field works without this half anyway (`WI-020`).
	return []Summary{}, nil
}

// MyTickets is what the signed-in user is assigned on this board.
func (d Deps) MyTickets(ctx context.Context, org, project string) ([]Summary, error) {
	client, err := d.clientFor(org)
	if err != nil {
		return nil, err
	}

	ids, err := client.QueryIDs(ctx, project,
		"[System.AssignedTo] = @Me AND [System.State] <> 'Closed' AND [System.State] <> 'Removed'", 200)
	if err != nil {
		return nil, err
	}
	return d.summaries(ctx, client, project, ids)
}

// Preview reads one work item over the summary fields alone (WI-019).
func (d Deps) Preview(ctx context.Context, org, project, externalID string) (*Summary, error) {
	id, err := numericID(externalID)
	if err != nil {
		// A half-typed id is not a work item, and it is not an error either: the field is debounced
		// and this runs on every keystroke.
		return nil, nil //nolint:nilnil // no such work item is the answer, not a failure
	}

	client, err := d.clientFor(org)
	if err != nil {
		return nil, err
	}

	items, err := client.BatchWorkItems(ctx, project, []int64{id}, summaryFields)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil //nolint:nilnil // `errorPolicy: omit` answers an unknown id with no row
	}
	summary := summaryFrom(items[0])
	return &summary, nil
}

// summaries reads a list of ids over the summary fields, in one batched call per two hundred.
func (d Deps) summaries(
	ctx context.Context,
	client providers.AzureClient,
	project string,
	ids []int64,
) ([]Summary, error) {
	if len(ids) == 0 {
		return []Summary{}, nil
	}

	items, err := client.BatchWorkItems(ctx, project, ids, summaryFields)
	if err != nil {
		return nil, err
	}

	summaries := make([]Summary, 0, len(items))
	for _, item := range items {
		summaries = append(summaries, summaryFrom(item))
	}
	return summaries, nil
}

// registerReviews adds the review dispatcher, its history, and the one write.
func registerReviews(r *bridge.Registry, deps Deps) {
	r.Add("list_ticket_reviews", func(ctx context.Context, p bridge.Params) (any, error) {
		projectID, branch, err := branchTarget(p)
		if err != nil {
			return nil, err
		}
		return deps.Store.ReviewsForBranch(ctx, projectID, branch)
	})

	// Every parameter is read before anything is resolved: a missing one is a disagreement between
	// the two sides, and reporting it as a ticket state would send the reader looking in the wrong
	// place.
	r.Add("review_changes", func(ctx context.Context, p bridge.Params) (any, error) {
		request, err := reviewRequest(p)
		if err != nil {
			return nil, err
		}
		text, err := deps.ReviewChanges(ctx, request)
		if err != nil {
			return nil, reviewSentinels(err)
		}
		return text, nil
	})

	// The one write. A publish in flight is blocked in the renderer's store rather than only on the
	// button, because a duplicate comment cannot be taken back from inside the app.
	r.Add("comment_ticket", func(ctx context.Context, p bridge.Params) (any, error) {
		ticketID, err := bridge.Arg[string](p, "ticketId")
		if err != nil {
			return nil, err
		}
		body, err := bridge.Arg[string](p, "body")
		if err != nil {
			return nil, err
		}
		return asCommandError(deps.Comment(ctx, ticketID, body))
	})
}

func reviewRequest(p bridge.Params) (ReviewRequest, error) {
	projectID, err := bridge.Arg[string](p, "projectId")
	if err != nil {
		return ReviewRequest{}, err
	}
	jobID, err := bridge.Arg[string](p, "jobId")
	if err != nil {
		return ReviewRequest{}, err
	}
	branch, err := bridge.Arg[string](p, "branch")
	if err != nil {
		return ReviewRequest{}, err
	}
	scope, err := bridge.Arg[string](p, "scope")
	if err != nil {
		return ReviewRequest{}, err
	}
	withTicket, err := bridge.Arg[bool](p, "withTicket")
	if err != nil {
		return ReviewRequest{}, err
	}
	baseRef, err := bridge.OptionalArg[string](p, "baseRef")
	if err != nil {
		return ReviewRequest{}, err
	}
	level, err := bridge.Arg[string](p, "level")
	if err != nil {
		return ReviewRequest{}, err
	}
	agent, err := agentOverride(p)
	if err != nil {
		return ReviewRequest{}, err
	}

	return ReviewRequest{
		ProjectID:  projectID,
		JobID:      jobID,
		Branch:     branch,
		Scope:      scope,
		WithTicket: withTicket,
		BaseRef:    optionalText(baseRef),
		Level:      level,
		Agent:      agent,
	}, nil
}

// agentOverride reads the three optional parameters that route a run through an agent's own
// provider and model instead of the per-task cascade.
func agentOverride(p bridge.Params) (*ai.AgentOverride, error) {
	provider, err := bridge.OptionalArg[string](p, "agentProvider")
	if err != nil {
		return nil, err
	}
	model, err := bridge.OptionalArg[string](p, "agentModel")
	if err != nil {
		return nil, err
	}
	prompt, err := bridge.OptionalArg[string](p, "agentPrompt")
	if err != nil {
		return nil, err
	}
	if provider == nil && model == nil && prompt == nil {
		return nil, nil //nolint:nilnil // no override is the ordinary case, not a failure
	}
	return &ai.AgentOverride{
		Provider: optionalText(provider), Model: optionalText(model), Prompt: optionalText(prompt),
	}, nil
}

// reviewSentinels puts the three review refusals at position 0, and only there (XLANG-015,
// XLANG-017).
//
// Applied at the command boundary and never at the throw site: inside the process these are typed
// errors that `errors.Is` matches, and it is only on the way out that the renderer needs a prefix it
// can test with `startsWith`.
func reviewSentinels(err error) error {
	switch {
	case errors.Is(err, ai.ErrNothingToAnalyze):
		return errors.New(sentinel.NothingToAnalyze + err.Error()) //nolint:staticcheck // ST1005: VERBATIM sentinel
	case errors.Is(err, ErrNoTicketLinked):
		return errors.New(sentinel.TicketNotLinked + err.Error()) //nolint:staticcheck // ST1005: VERBATIM sentinel
	case errors.Is(err, ErrTicketUnreadable):
		return errors.New(sentinel.TicketSyncFailed + err.Error()) //nolint:staticcheck // ST1005: VERBATIM sentinel
	default:
		return asCommandErrorOnly(err)
	}
}

// asCommandError puts the refused-credential sentinel at position 0 of a failed call's error.
func asCommandError[T any](value T, err error) (any, error) {
	if err != nil {
		return nil, asCommandErrorOnly(err)
	}
	return value, nil
}

func asCommandErrorOnly(err error) error {
	if err == nil {
		return nil
	}

	var azure *providers.AzureError
	if errors.As(err, &azure) && azure.Unauthorized {
		return errors.New(sentinel.CredentialRefused + err.Error()) //nolint:staticcheck // ST1005: VERBATIM sentinel
	}
	if errors.Is(err, providers.ErrCredentialRefused) {
		message := strings.TrimPrefix(err.Error(), sentinel.CredentialRefused)
		return errors.New(sentinel.CredentialRefused + message) //nolint:staticcheck // ST1005: VERBATIM sentinel
	}
	return err
}

// boardTarget reads the three parameters that address one work item on a board.
func boardTarget(p bridge.Params) (org, project, externalID string, err error) {
	if org, err = bridge.Arg[string](p, "org"); err != nil {
		return "", "", "", err
	}
	if project, err = bridge.Arg[string](p, "project"); err != nil {
		return "", "", "", err
	}
	if externalID, err = bridge.Arg[string](p, "externalId"); err != nil {
		return "", "", "", err
	}
	return org, project, externalID, nil
}

// branchTarget reads the two parameters that address one branch of one repository.
func branchTarget(p bridge.Params) (projectID, branch string, err error) {
	if projectID, err = bridge.Arg[string](p, "projectId"); err != nil {
		return "", "", err
	}
	if branch, err = bridge.Arg[string](p, "branch"); err != nil {
		return "", "", err
	}
	return projectID, branch, nil
}

func optionalText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
