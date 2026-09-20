package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Reviewing a pull request (AI-023) and the depth it is reviewed at (AI-022).
//
// The payload is built here and nowhere else, because every part of it is a decision that was paid
// for once: the diff is reshaped rather than flattened (`GIT-031`), the code around each change is
// quoted so the model does not go and read it (`GIT-033`), a context is capped and says when it was
// cut, and the level directive is appended **after** the methodology so it can override the depth
// the methodology implies.

// maxContextChars caps one review context. Per context rather than over all of them together: a
// shared pool lets the first starve the rest, which is the failure the diff budget already fixed.
//
// A context is a free-text field the user pastes into, stored in a column that neither validates
// nor truncates — so an architecture document pasted into one used to enter the prompt entire and
// unannounced.
const maxContextChars = 30_000

// ReviewContext is one named block of standing instructions the workspace carries into a review.
type ReviewContext struct {
	Name    string
	Content string
}

// ReviewRequest is everything one pull-request review needs.
type ReviewRequest struct {
	Title       string
	Description string
	// Contexts are the enabled review contexts, in the order they reach the model.
	Contexts []ReviewContext
	// Diff is already reshaped for a prompt (`GIT-031`) — this does not cut it again.
	Diff string
	// CodeContext is the `CODE AROUND THE CHANGES` section, empty for a review with no checkout.
	CodeContext string
	// WorkingDir is the repository, or the ad-hoc directory a link review writes its two files to.
	WorkingDir string
	// Template is the workspace's review methodology, blank meaning the built-in one.
	Template string
	// Level is `basico`, `completo` or `ultra`; anything else is `completo`.
	Level string
	// Explorable is false for a review with no checkout, which changes what `ultra` is allowed to
	// ask for.
	Explorable bool
	MCPConfig  string
	// AgentOverride routes this run through an agent's own provider and model instead of the
	// per-task cascade.
	Agent *AgentOverride
}

// ErrNothingToReview is refused before the model is invoked: a pull request with no diff has
// nothing to say about it, and asking anyway bills for an answer nobody can act on.
var ErrNothingToReview = errors.New("This pull request has no changes to review") //nolint:staticcheck // ST1005: VERBATIM

// Review runs one pull-request review and answers the markdown the model produced (AI-023).
//
// The footer is **not** stamped here. It has to be the last thing in the text, and the caller
// appends the resolved-findings history after this returns — stamped here, the renderer's
// end-anchored footer pattern stopped matching. Half of what belongs in it is known there and not
// here anyway: how long the whole operation took, how much of the change reached the model, what
// the findings did since the last review.
func (o Operations) Review(ctx context.Context, runID string, request ReviewRequest) (Result, error) {
	if strings.TrimSpace(request.Diff) == "" {
		return Result{}, ErrNothingToReview
	}

	template := request.Template
	if strings.TrimSpace(template) == "" {
		template = Prompt(PromptPRReviewStandard)
	}

	// An agent driving this run on its own routing, or the ordinary per-task cascade — the same
	// decision a chat turn makes, and made the same way.
	config := o.configFor(ctx, TaskReview, request.Agent)
	config.AllowedTools = ReviewTools(request.Level, request.Explorable)

	return o.invokeWith(ctx, runID, config, Invocation{
		Prompt:       template + "\n\n" + ReviewLevelDirective(request.Level, request.Explorable),
		StdinContent: reviewPayload(request),
		WorkingDir:   request.WorkingDir,
		MCPConfig:    request.MCPConfig,
		// A review reads and judges; it never writes. That is what makes it safe to repeat after a
		// transient network failure.
		ReadOnly: true,
	}, true)
}

// reviewPayload is what the model reads on stdin (AI-023).
//
// Order matters and is the order a person would read it in: what the change claims to be, the
// standing instructions it is judged against, the change itself, then the code it lands in.
func reviewPayload(request ReviewRequest) string {
	payload := &strings.Builder{}

	fmt.Fprintf(payload, "PR TITLE: %s\n", request.Title)

	description := strings.TrimSpace(request.Description)
	if description == "" {
		description = "(no description)"
	}
	fmt.Fprintf(payload, "PR DESCRIPTION: %s\n", description)

	if block := renderContexts(request.Contexts); block != "" {
		payload.WriteString("\nPROJECT REVIEW CONTEXT:\n")
		payload.WriteString(block)
	}

	payload.WriteString("\nDIFF:\n")
	payload.WriteString(request.Diff)

	if strings.TrimSpace(request.CodeContext) != "" {
		payload.WriteString("\n\n")
		payload.WriteString(request.CodeContext)
	}
	return payload.String()
}

// renderContexts writes one line per context, each capped and saying when it was cut.
func renderContexts(contexts []ReviewContext) string {
	block := &strings.Builder{}
	for _, context := range contexts {
		content := strings.TrimSpace(context.Content)
		if content == "" {
			continue
		}
		if len([]rune(content)) > maxContextChars {
			dropped := len([]rune(content)) - maxContextChars
			content = truncate(content, maxContextChars) +
				fmt.Sprintf("\n… (%d characters of this context were left out)", dropped)
		}
		fmt.Fprintf(block, "- %s: %s\n", context.Name, content)
	}
	return block.String()
}

// ReviewLevelDirective is the depth block appended after the methodology (AI-022).
//
// Appended **after** the template on purpose: it overrides whatever depth the methodology implies,
// which is what makes one stored methodology usable at three depths.
//
// An unrecognised level is `completo`, never an error: the level arrives from the renderer as a
// string, and a review that refuses to run because of a typo in it is worse than one that runs at
// the middle depth.
func ReviewLevelDirective(level string, explorable bool) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "basico", "básico":
		return Prompt(PromptReviewLevelBasico)
	case "ultra":
		// A link review has no checkout, and `ultra` used to tell it to read the method around
		// every change anyway — an instruction that reached the model on argv while stdin told it
		// the opposite. Neither read as wrong on its own.
		if !explorable {
			return Prompt(PromptReviewLevelUltraNoClone)
		}
		return Prompt(PromptReviewLevelUltra)
	default:
		return Prompt(PromptReviewLevelCompleto)
	}
}

// ReviewTools is what a review is allowed to do, by depth (REVIEW-039).
//
// A review judges a change it has already been given: the diff is in the payload and the code
// around it is quoted beside it. Only `ultra` gets to look further, and only at a checkout — a link
// review gets nothing at any level, because there is nothing there to read.
func ReviewTools(level string, explorable bool) []string {
	if !explorable {
		return []string{}
	}
	if strings.EqualFold(strings.TrimSpace(level), "ultra") {
		return []string{"Read", "Grep", "Glob"}
	}
	return []string{}
}
