package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/gastonlarap-a11y/code-flow/backend/activity"
	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// `review_changes`: one panel, two axes (WI-023).
//
// A review of local changes is described by two independent choices — **which diff** (the
// uncommitted tree, or everything the branch contributes over a base) and **whether the work item is
// judged too**. Each of the four combinations keeps its own prompt, routing key and storage:
//
//	working · no ticket → analyze_template       · analyze       · job_history
//	branch  · no ticket → analyze_template       · analyze       · job_history
//	working · ticket    → ticket_review_standard · ticket_review · ticket_review_runs
//	branch  · ticket    → ticket_review_standard · ticket_review · ticket_review_runs
//
// The two axes used to be welded together — the pre-commit analysis was always the working tree and
// never the ticket, the ticket review always the whole branch — so only two of the four existed. The
// one that was wanted and missing is a whole-branch review with **no** ticket: looking over what you
// have before opening a pull request, in a repository that keeps no tickets.
//
// **A dispatcher, not a third implementation.** This chooses between the two orchestrations and
// hands each its scope; both bodies stay in the feature that owns them.

// ReviewRequest is what the panel asks for.
type ReviewRequest struct {
	ProjectID string
	// JobID doubles as the run id, so the job row the renderer already created shows this run's live
	// output and its stop button with no second id to plumb (AI-051).
	JobID      string
	Branch     string
	Scope      string
	WithTicket bool
	BaseRef    string
	Level      string
	Agent      *ai.AgentOverride
}

// ErrNoTicketLinked and ErrTicketUnreadable are the two ticket refusals (XLANG-017).
//
// They are different kinds of thing and the renderer treats them differently. The first is a
// **state**: the branch has no work item, the section shows how to link one, and the row does not
// stand in for the branch's last real review. The second is a genuine failure and is reported as
// one — hiding a sync failure behind a calm empty state is how a review silently stops running.
var (
	ErrNoTicketLinked = errors.New("Esta rama no tiene un work item vinculado") //nolint:staticcheck // ST1005: VERBATIM
	// ErrTicketUnreadable is raised only when **both** halves are true: the fetch failed and nothing
	// usable was cached. A fetch that fails over a cache holding the work item runs the review anyway
	// and says how old the copy is, because refusing would also withhold the finding half of the
	// answer, which never needed the network.
	ErrTicketUnreadable = errors.New("No se pudo leer el work item y no hay copia en caché") //nolint:staticcheck // ST1005: VERBATIM
)

// ReviewChanges dispatches to the orchestration the two axes name (WI-023).
//
// The diff is **not** computed here, and that ordering is the rule rather than a detail: a branch
// with no linked work item has to answer `TICKET_NOT_LINKED` — a state the panel renders as an offer
// to link one — and computing the diff first makes it answer whatever git had to say instead. Each
// half asks for its own diff once it knows it has something to review.
func (d Deps) ReviewChanges(ctx context.Context, request ReviewRequest) (string, error) {
	project, err := d.Workspaces.GetProject(ctx, request.ProjectID)
	if err != nil {
		return "", err
	}

	if request.WithTicket {
		return d.reviewAgainstTicket(ctx, project, request)
	}
	return d.analyze(ctx, project, request)
}

// diffFor is the "which diff" axis (WI-023, GIT-039), already reshaped for a prompt.
func (d Deps) diffFor(ctx context.Context, repoPath string, request ReviewRequest) (string, error) {
	var files []git.FileDiff
	var err error

	if request.Scope == ai.ScopeBranch {
		files, err = git.BranchContribution(ctx, repoPath, request.BaseRef)
	} else {
		files, err = git.WorkingDiff(ctx, repoPath)
	}
	if err != nil {
		return "", err
	}

	diff, _ := git.ShapeForPrompt(files, git.PromptBudgetChars, nil)
	return diff, nil
}

// analyze is the no-ticket half: `AI-024`'s body, handed a scope (AI-051).
//
// One job-history row on completion, unless the run was cancelled — the person who stopped it did
// not ask for a record of having done so. A refusal for an empty tree writes no row either
// (`XLANG-015`).
func (d Deps) analyze(
	ctx context.Context,
	project workspaces.Project,
	request ReviewRequest,
) (string, error) {
	diff, err := d.diffFor(ctx, project.LocalPath, request)
	if err != nil {
		return "", err
	}

	config, err := d.reviewConfig(ctx, project.WorkspaceID, "analyze_template")
	if err != nil {
		return "", err
	}

	result, err := d.AI.AnalyzeChanges(ctx, request.JobID, ai.AnalyzeRequest{
		Contexts:   contextsWithAgent(config.Contexts, request.Agent),
		Diff:       diff,
		Scope:      request.Scope,
		BaseRef:    request.BaseRef,
		WorkingDir: project.LocalPath,
		Template:   config.Template,
		MCPConfig:  config.MCPConfig,
		Agent:      request.Agent,
	})
	if err != nil {
		if !isCancelled(err) && !errors.Is(err, ai.ErrNothingToAnalyze) {
			d.fileJob(ctx, request, project.ID, "analyze", "error", nil, err)
		}
		return "", err
	}

	text := result.Text
	d.fileJob(ctx, request, project.ID, "analyze", "done", &text, nil)
	return text, nil
}

// reviewAgainstTicket is the ticket half (WI-011…017, WI-026).
//
// The review reads the work item, writes its own row and stops. Publishing the verdict onto the
// board is a separate act the user takes afterwards, with a button (`WI-022`).
func (d Deps) reviewAgainstTicket(
	ctx context.Context,
	project workspaces.Project,
	request ReviewRequest,
) (string, error) {
	// The linked work item first, before git is asked anything: with no ticket this answers a state
	// the panel renders, and it must not be preceded by a git failure about the same branch.
	ticket, err := d.Store.ForBranch(ctx, request.ProjectID, request.Branch)
	if errors.Is(err, ErrNotFound) {
		return "", ErrNoTicketLinked
	}
	if err != nil {
		return "", err
	}

	diff, err := d.diffFor(ctx, project.LocalPath, request)
	if err != nil {
		return "", err
	}

	// Best-effort, immediately before the review, so the criteria being judged are current
	// (`WI-009`). A failure here is only fatal when there is no usable cache behind it.
	if refreshed, syncErr := d.Sync(ctx, ticket.Org, ticket.Project, ticket.ExternalID); syncErr == nil {
		ticket = refreshed
	} else if strings.TrimSpace(ticket.Title) == "" {
		return "", ErrTicketUnreadable
	}

	block, err := d.ticketBlock(ctx, ticket)
	if err != nil {
		return "", err
	}

	config, err := d.reviewConfig(ctx, project.WorkspaceID, "ticket_review_standard")
	if err != nil {
		return "", err
	}

	result, err := d.AI.ReviewAgainstTicket(ctx, request.JobID, ai.TicketReviewRequest{
		Ticket:     block,
		Contexts:   contextsWithAgent(config.Contexts, request.Agent),
		Diff:       diff,
		Scope:      request.Scope,
		BaseRef:    request.BaseRef,
		WorkingDir: project.LocalPath,
		Template:   config.Template,
		Level:      request.Level,
		MCPConfig:  config.MCPConfig,
		Agent:      request.Agent,
	})
	if err != nil {
		if !isCancelled(err) && !errors.Is(err, ai.ErrNothingToAnalyze) {
			d.fileJob(ctx, request, project.ID, "ticket-review", "error", nil, err)
		}
		return "", err
	}

	text := result.Text
	d.storeReview(ctx, project, ticket, request, text, diff)
	d.fileJob(ctx, request, project.ID, "ticket-review", "done", &text, nil)
	return text, nil
}

// ticketBlock assembles what the model is told about the work item.
func (d Deps) ticketBlock(ctx context.Context, ticket Ticket) (ai.TicketBlock, error) {
	criteria := NoCriteria()
	description := ""

	payload, err := d.Store.RawPayload(ctx, ticket.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		// The row was there a moment ago: another window deleted it. The review still runs, with the
		// work item's headline fields and no requirements — which reads as `mode: none` and is said
		// out loud rather than guessed at.
	case err != nil:
		return ai.TicketBlock{}, err
	default:
		var item cachedWorkItem
		if err := json.Unmarshal([]byte(payload), &item); err == nil {
			description = ToMarkdown(item.Fields[FieldDescription])
			criteria = d.criteriaFromCache(ctx, ticket, item)
		}
	}

	return ai.TicketBlock{
		ExternalID:       ticket.ExternalID,
		WorkItemType:     ticket.WorkItemType,
		Title:            ticket.Title,
		State:            ticket.State,
		Description:      description,
		CriteriaMode:     criteria.Mode,
		CriteriaMarkdown: criteria.Markdown,
		Notes:            ReadNotes(ticket.MirrorPath),
	}, nil
}

// storeReview writes the finished review into its own table (WI-013).
//
// Best-effort: a failed write costs the history row, not the review the user is reading. Not a row
// in `review_runs`, and the reason is structural — that table's `pr_id` is `NOT NULL` and its index
// is `(project_id, pr_id, created_at)`, so a pre-commit review would need a fake id.
func (d Deps) storeReview(
	ctx context.Context,
	project workspaces.Project,
	ticket Ticket,
	request ReviewRequest,
	text, diff string,
) {
	verdict := ParseVerdict(text)

	var criteria []CriterionVerdict
	var coverage *Coverage
	if verdict != nil {
		criteria, coverage = verdict.Criteria, verdict.Coverage
	}

	// The explaining sentences ride in `meta`; the single word a history list filters on is its own
	// column, written by the store.
	meta, err := json.Marshal(map[string]any{
		"coverage":  coverage,
		"scope":     request.Scope,
		"timestamp": d.now().Format(time.RFC3339),
	})
	if err != nil {
		meta = []byte("{}")
	}

	head, _ := git.ResolveSHA(ctx, project.LocalPath, "HEAD")

	_ = d.Store.AddReview(ctx, NewReview{
		ID:          reviewID(request.JobID),
		ProjectID:   project.ID,
		WorkspaceID: project.WorkspaceID,
		TicketID:    ticket.ID,
		Branch:      request.Branch,
		BaseRef:     request.BaseRef,
		HeadSHA:     head,
		Level:       request.Level,
		Meta:        string(meta),
		ReviewMD:    text,
		Diff:        diff,
		Criteria:    criteria,
		Coverage:    coverage,
	})
}

// reviewID keys the row by the job it was run under, so a row and the Activity entry beside it name
// the same run. A blank job id gets one of its own rather than colliding on the empty string.
func reviewID(jobID string) string {
	if strings.TrimSpace(jobID) == "" {
		return uuid.NewString()
	}
	return jobID
}

// cachedWorkItem is the stored payload read back, with its fields already as text: what the mirror
// is rewritten from and what the criteria are re-derived from, both with no fetch.
type cachedWorkItem struct {
	Fields FieldMap `json:"fields"`
}

// criteriaFromCache recomputes what the ticket asks for from the cached payload — no network, which
// is what `get_ticket_criteria` promises.
func (d Deps) criteriaFromCache(ctx context.Context, ticket Ticket, item cachedWorkItem) Criteria {
	order := CriteriaFieldOrder(d.setting(ctx,
		fmt.Sprintf("ticket_criteria_fields:%s:%s", ticket.Org, ticket.Project)))

	payloads, err := d.Store.OthersOfType(ctx, ticket.Org, ticket.Project, ticket.WorkItemType, ticket.ID)
	if err != nil {
		payloads = nil
	}

	others := map[string][]string{}
	for _, payload := range payloads {
		var cached cachedWorkItem
		if err := json.Unmarshal([]byte(payload), &cached); err != nil {
			continue
		}
		for _, field := range order {
			if value := cached.Fields[field]; value != "" {
				others[field] = append(others[field], value)
			}
		}
	}
	return ReadCriteria(item.Fields, order, others)
}

// fileJob records a finished review in the project's history (AI-051).
func (d Deps) fileJob(
	ctx context.Context,
	request ReviewRequest,
	projectID, kind, status string,
	result *string,
	failure error,
) {
	if d.Activity == nil {
		return
	}

	job := activity.NewJob{
		ID:        reviewID(request.JobID),
		ProjectID: projectID,
		Kind:      kind,
		Label:     request.Branch,
		Status:    status,
		Result:    result,
		Meta: fmt.Sprintf(`{"branch":%s,"scope":%s,"level":%s}`,
			jsonText(request.Branch), jsonText(request.Scope), jsonText(request.Level)),
	}
	if failure != nil {
		message := failure.Error()
		job.Error = &message
	}
	// Best-effort: a history write that fails must not turn a good review into a reported failure.
	_, _ = d.Activity.RecordJob(ctx, job)
}

func jsonText(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// contextsWithAgent puts an agent's own instructions first when there is one: they are what the
// person driving this run asked for, and a standing project rule should not outrank them.
func contextsWithAgent(contexts []ai.ReviewContext, agent *ai.AgentOverride) []ai.ReviewContext {
	if agent == nil || strings.TrimSpace(agent.Prompt) == "" {
		return contexts
	}
	return append([]ai.ReviewContext{{Name: "Agent", Content: agent.Prompt}}, contexts...)
}

// isCancelled reports a run the user stopped.
func isCancelled(err error) bool {
	return err != nil && strings.Contains(err.Error(), "RUN_CANCELLED::")
}
