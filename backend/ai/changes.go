package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Reviewing local changes, over the two axes `WI-023` exposes: which diff, and whether the work item
// is judged too.
//
// Two operations rather than three. `review_changes` dispatches between them and hands each its
// scope; neither body was rewritten to accommodate the other, so `AI-024`'s refusal rules still say
// what they said.

// The caps this file spends, all in Unicode scalars (WI-017).
const (
	// maxTicketChars is the work item's own prose. Before this existed the diff spent a deliberate
	// budget and the ticket was concatenated after it with no ceiling at all — a work item with a
	// long refinement thread would have starved the branch's own contribution out of the prompt.
	maxTicketChars = 40_000
	// maxTicketNotesChars is the user's notes on it.
	maxTicketNotesChars = 20_000
)

// ErrNothingToAnalyze refuses a clean working tree before the model is invoked (XLANG-015).
//
// The sentence is `VERBATIM`; the sentinel is put in front of it at the command boundary, never
// here. The renderer shows an empty state for it and no job row is written — filing it was right
// while reaching this needed a deliberate click, but the analyse tab starts a run when it is merely
// *opened*, and on a clean tree that left a permanent red row for a request nobody made.
var ErrNothingToAnalyze = errors.New("No hay cambios sin commitear para analizar") //nolint:staticcheck // ST1005: VERBATIM

// The two scopes, `VERBATIM` — they arrive from the renderer as these strings.
const (
	ScopeWorking = "working"
	ScopeBranch  = "branch"
)

// ScopeLine is what tells the model which diff it is looking at (WI-023).
//
// It reaches the model as a line of the payload rather than through the prompt, and that is the
// rule: `analyze_template` is a user-editable setting whose built-in text says "UNCOMMITTED
// changes", so anyone who had edited theirs would have been describing the wrong diff — silently,
// and only for the people who had customised it.
func ScopeLine(scope, baseRef string) string {
	if scope == ScopeBranch {
		return fmt.Sprintf(
			"SCOPE: la contribución completa de la rama sobre `%s` — incluye commits ya hechos y lo que falta por commitear. Júzgalo como un solo cuerpo de trabajo.",
			baseRef)
	}
	return "SCOPE: solo los cambios sin commitear del árbol de trabajo. Los commits previos de la rama NO están en este diff."
}

// CriteriaCaveat is what a criteria verdict carries when it only sees the uncommitted work
// (WI-024).
//
// Empty for a branch scope, which has the evidence. This is the combination a user asked for most
// directly and the only one with a defect of its own: with three commits done and something pending,
// the model sees only what is pending and reports met criteria as unmet — **systematically**, not as
// the occasional false positive that was explicitly accepted. A verdict wrong in that direction
// discredits the whole table.
//
// The prompt already carries the `no verificable` doctrine (`WI-012`); this is what activates it for
// this scope.
func CriteriaCaveat(scope string) string {
	if scope != ScopeWorking {
		return ""
	}
	return "AVISO SOBRE EL ALCANCE: este diff contiene ÚNICAMENTE los cambios sin commitear. " +
		"Los commits anteriores de la rama no se te están mostrando, así que la ausencia de " +
		"evidencia NO es evidencia de ausencia: si no ves cómo se cumple un criterio, responde " +
		"`no verificable` y di que la evidencia podría estar en commits previos. Nunca respondas " +
		"`no cumple` por algo que simplemente no está en este diff."
}

// AnalyzeRequest is one review of local changes with no work item (AI-024).
type AnalyzeRequest struct {
	// Contexts are the enabled project contexts, each capped by renderContexts.
	Contexts []ReviewContext
	// Diff arrives already reshaped for a prompt (`GIT-031`); this does not cut it again.
	Diff string
	// Scope and BaseRef produce the `SCOPE:` line.
	Scope      string
	BaseRef    string
	WorkingDir string
	// Template is the workspace's `analyze_template`, blank meaning the built-in one.
	Template  string
	MCPConfig string
	Agent     *AgentOverride
}

// AnalyzeChanges reviews local changes without judging a work item (AI-024).
func (o Operations) AnalyzeChanges(ctx context.Context, runID string, request AnalyzeRequest) (Result, error) {
	if strings.TrimSpace(request.Diff) == "" {
		return Result{}, ErrNothingToAnalyze
	}

	template := request.Template
	if strings.TrimSpace(template) == "" {
		template = o.router.SharedTemplate(ctx, "analyze_template", "claude_analyze_template", Prompt(PromptAnalyze))
	}

	payload := &strings.Builder{}
	if block := renderContexts(request.Contexts); block != "" {
		payload.WriteString("PROJECT CONTEXT:\n")
		payload.WriteString(block)
		payload.WriteString("\n")
	}
	payload.WriteString(ScopeLine(request.Scope, request.BaseRef))
	payload.WriteString("\n\nDIFF:\n")
	payload.WriteString(request.Diff)

	config := o.configFor(ctx, TaskAnalyze, request.Agent)
	return o.invokeWith(ctx, runID, config, Invocation{
		Prompt:       template,
		StdinContent: payload.String(),
		WorkingDir:   request.WorkingDir,
		MCPConfig:    request.MCPConfig,
		ReadOnly:     true,
	}, true)
}

// TicketBlock is the work item as the review is told about it (WI-017).
type TicketBlock struct {
	ExternalID   string
	WorkItemType string
	Title        string
	State        string
	// Description is capped at 40 000 characters.
	Description string
	// CriteriaMode is `list`, `prose` or `none`, and CriteriaMarkdown is **never capped**: the
	// criteria are what the change is being judged against, and truncating them turns "the model
	// did not check AC-7" into a finding about the work rather than about the prompt.
	CriteriaMode     string
	CriteriaMarkdown string
	// Notes are the `.md`/`.txt` files in the mirror's `notes/`, capped at 20 000 characters.
	Notes string
}

// TicketReviewRequest is one review of local changes judged against a work item.
type TicketReviewRequest struct {
	Ticket     TicketBlock
	Contexts   []ReviewContext
	Diff       string
	Scope      string
	BaseRef    string
	WorkingDir string
	// Template is the workspace's `ticket_review_standard`, blank meaning the built-in one.
	Template  string
	Level     string
	MCPConfig string
	Agent     *AgentOverride
}

// ReviewAgainstTicket judges a branch's work against the work item it was written for.
//
// Its own task key (`ticket_review`), because the model that judges a branch against a work item can
// differ from the one that reads a pull request — usually a larger one, because the question is
// harder. It joins the judging tasks for a reason `analyze` and `review` do not have: it is asked
// whether a criterion is *met*, and a criterion is regularly satisfied by code the diff does not
// touch, so without `Read`/`Grep`/`Glob` the honest answer would be `no verificable` every time.
func (o Operations) ReviewAgainstTicket(ctx context.Context, runID string, request TicketReviewRequest) (Result, error) {
	if strings.TrimSpace(request.Diff) == "" {
		return Result{}, ErrNothingToAnalyze
	}

	template := request.Template
	if strings.TrimSpace(template) == "" {
		template = Prompt(PromptTicketReviewStandard)
	}

	config := o.configFor(ctx, TaskTicketReview, request.Agent)
	config.AllowedTools = ReviewTools(request.Level, true)

	return o.invokeWith(ctx, runID, config, Invocation{
		Prompt:       template + "\n\n" + ReviewLevelDirective(request.Level, true),
		StdinContent: ticketReviewPayload(request),
		WorkingDir:   request.WorkingDir,
		MCPConfig:    request.MCPConfig,
		ReadOnly:     true,
	}, true)
}

// ticketReviewPayload is what the model reads on stdin, in the order the prompt describes it: the
// work item, what it asks for, what the user added, the project's standing rules, then the change.
func ticketReviewPayload(request TicketReviewRequest) string {
	ticket := request.Ticket
	payload := &strings.Builder{}

	fmt.Fprintf(payload, "TICKET: %s %s — %s\nESTADO: %s\n\n%s\n",
		ticket.WorkItemType, ticket.ExternalID, ticket.Title, ticket.State,
		truncate(strings.TrimSpace(ticket.Description), maxTicketChars))

	fmt.Fprintf(payload, "\nCRITERIA MODE: %s\n", ticket.CriteriaMode)
	payload.WriteString("\nACCEPTANCE CRITERIA:\n")
	if criteria := strings.TrimSpace(ticket.CriteriaMarkdown); criteria != "" {
		payload.WriteString(criteria + "\n")
	} else {
		payload.WriteString("(el work item no declara criterios verificables)\n")
	}

	if notes := strings.TrimSpace(ticket.Notes); notes != "" {
		payload.WriteString("\nUSER NOTES ON THIS TICKET:\n")
		payload.WriteString(truncate(notes, maxTicketNotesChars) + "\n")
	}

	if block := renderContexts(request.Contexts); block != "" {
		payload.WriteString("\nPROJECT REVIEW CONTEXT:\n")
		payload.WriteString(block)
	}

	fmt.Fprintf(payload, "\n%s\n", ScopeLine(request.Scope, request.BaseRef))
	if caveat := CriteriaCaveat(request.Scope); caveat != "" {
		payload.WriteString("\n" + caveat + "\n")
	}

	payload.WriteString("\nDIFF:\n")
	payload.WriteString(request.Diff)
	return payload.String()
}

// configFor resolves the cascade for a task, or an agent's own provider and model when the person
// driving this run chose one. The same decision a chat turn and a pull-request review make.
func (o Operations) configFor(ctx context.Context, task Task, agent *AgentOverride) Config {
	if agent != nil &&
		strings.TrimSpace(agent.Provider) != "" && strings.TrimSpace(agent.Model) != "" {
		return o.router.ResolveWith(ctx, agent.Provider, agent.Model, task)
	}
	return o.router.Resolve(ctx, task)
}
