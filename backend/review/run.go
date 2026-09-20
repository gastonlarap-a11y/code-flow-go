package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/gastonlarap-a11y/code-flow/backend/providers"
	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// Running a review (REVIEW-018…023).
//
// Sixteen steps in the specification, and the order of the last three is what makes the difference
// between a review a person can trust and one they cannot: the text is stamped with what it cost
// **last**, after the history section is appended, because the renderer's footer pattern is
// end-anchored and a footer with anything after it matches nothing at all.

// ReviewRequest is one project-backed review.
type RunRequest struct {
	ProjectID string
	PRID      int64
	JobID     string
	Level     string
	Agent     *ai.AgentOverride
}

// ErrPullRequestNotFound is what a pull request the host does not list answers.
var ErrPullRequestNotFound = errorString("Pull request not found")

type errorString string

func (e errorString) Error() string { return string(e) }

// unchangedSince is the answer a review gives when the pull request has not moved (REVIEW-020).
//
// `VERBATIM`, Spanish, and the eight-character SHA is part of it: it is what lets a reader tell
// "nothing changed" from "the review failed quietly".
func unchangedSince(sha string) string {
	return fmt.Sprintf("🔁 Sin cambios desde la última revisión (mismo commit `%s`). No se volvió a analizar.",
		abbreviate(sha))
}

func abbreviate(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
}

// Run reviews a pull request against its local clone (REVIEW-018…022).
func (d PipelineDeps) Run(ctx context.Context, request RunRequest) (string, error) {
	started := time.Now()

	project, err := d.Projects.GetProject(ctx, request.ProjectID)
	if err != nil {
		return "", err
	}
	host, err := d.Hosts.HostForProject(ctx, request.ProjectID)
	if err != nil {
		return "", err
	}

	// The whole list, then find — never the single-pull-request endpoint. Preserved from 2.x, and
	// it has a cost worth naming: GitHub's list caps at the newest hundred, so a pull request older
	// than that is unreachable from here even though the host could read it directly.
	pulls, err := host.List(ctx)
	if err != nil {
		return "", err
	}
	pull, found := findPull(pulls, request.PRID)
	if !found {
		return "", ErrPullRequestNotFound
	}

	config, err := d.reviewConfig(ctx, project.WorkspaceID)
	if err != nil {
		return "", err
	}

	// Best-effort: offline, or an expired credential on a remote, must not block a review of what
	// is already local.
	if d.Fetcher != nil {
		_ = d.Fetcher.Fetch(ctx, project.LocalPath)
	}

	headRef := d.headRef(ctx, host, project.LocalPath, pull, request.PRID)
	headSHA, _ := git.ResolveSHA(ctx, project.LocalPath, headRef)

	previous, hasPrevious := d.previousRun(ctx, request.ProjectID, request.PRID)
	if hasPrevious && headSHA != "" && AnalysedHead(previous.Meta) == headSHA {
		// Nothing has been pushed since the last review. No model call, no rows: this path is a
		// read and an early return, and saying so beats charging for the same answer twice.
		return unchangedSince(headSHA), nil
	}

	// Only for a re-review, and only when the previous head is known: this is what tells
	// reconciliation which files this run actually looked at.
	var changedFiles []string
	if hasPrevious {
		if previousHead := AnalysedHead(previous.Meta); previousHead != "" {
			changedFiles, _ = git.ChangedFilesBetween(ctx, project.LocalPath, previousHead, headRef)
		}
	}

	files, err := git.BranchDiffFiles(ctx, project.LocalPath, pull.TargetBranch, headRef)
	if err != nil {
		return "", err
	}
	diff, coverage := git.ShapeForPrompt(files, git.PromptBudgetChars, nil)

	result, runErr := d.AI.Review(ctx, request.JobID, ai.ReviewRequest{
		Title:       pull.Title,
		Description: pull.Description,
		Contexts:    reviewContexts(config.Contexts, request.Agent, false),
		Diff:        diff,
		CodeContext: git.RenderChangeContext(files, git.ChangeContextBudgetChars),
		WorkingDir:  project.LocalPath,
		Template:    config.Template,
		Level:       request.Level,
		Explorable:  true,
		MCPConfig:   d.mcpConfig(project.WorkspaceID, config.MCPs),
		Agent:       request.Agent,
	})
	if runErr != nil {
		// A cancelled run leaves nothing behind — no history row, no saved review. The user stopped
		// it; filing it would put a failure in Activity for something nobody wanted recorded.
		if !isCancelled(runErr) {
			d.fileJob(ctx, request, project.ID, pull, "error", nil, runErr)
		}
		return "", runErr
	}

	text := d.persist(ctx, request, project, pull, result.Text, diff, changedFiles, coverage, started)
	d.fileJob(ctx, request, project.ID, pull, "done", &text, nil)
	return text, nil
}

func findPull(pulls []providers.PullRequestSummary, prID int64) (providers.PullRequestSummary, bool) {
	for _, pull := range pulls {
		if pull.ID == prID {
			return pull, true
		}
	}
	return providers.PullRequestSummary{}, false
}

// headRef is the ref the review reads (REVIEW-019).
//
// GitHub's pull request can come from a fork, where its head branch is on nobody's `origin`: one
// targeted fetch brings it into a tracking ref of this application's own. Azure has no fork model,
// so its source branch is already there. Both best-effort — a review of what is local beats none.
func (d PipelineDeps) headRef(ctx context.Context, host providers.PullRequestHost, repoPath string, pull providers.PullRequestSummary, prID int64) string {
	if host.Provider() != providers.ProviderGitHub || d.Fetcher == nil {
		return pull.SourceBranch
	}

	refspec, localRef := git.PullRequestHeadRefspec(prID)
	if err := d.Fetcher.FetchRefspecs(ctx, repoPath, "origin", []string{refspec}); err != nil {
		return pull.SourceBranch
	}

	// The fetch reporting success is not enough: what matters is whether the ref is **there**, and
	// a review pointed at a ref that does not resolve fails two steps later with a message about a
	// branch nobody named. Asking git is one command and settles it.
	if _, err := git.ResolveSHA(ctx, repoPath, localRef); err != nil {
		return pull.SourceBranch
	}
	return localRef
}

func (d PipelineDeps) previousRun(ctx context.Context, projectID string, prID int64) (RunDetail, bool) {
	run, err := d.Store.LatestRun(ctx, projectID, prID)
	if err != nil {
		// A database that will not answer degrades to "there is no previous run", which reviews
		// everything as new — wrong, but never wrong in the direction of losing a finding.
		return RunDetail{}, false
	}
	return run, true
}

// reviewContexts assembles what frames the review, in the order it reaches the model.
//
// The agent's own instructions go first when there is one: they are what the person driving this
// run asked for, and a standing project rule should not outrank them.
func reviewContexts(contexts []ai.ReviewContext, agent *ai.AgentOverride, noClone bool) []ai.ReviewContext {
	assembled := make([]ai.ReviewContext, 0, len(contexts)+2)

	if noClone {
		assembled = append(assembled, ai.ReviewContext{Name: "Modo de revisión", Content: NoCloneContext})
	}
	if agent != nil && strings.TrimSpace(agent.Prompt) != "" {
		assembled = append([]ai.ReviewContext{{Name: "Agent", Content: agent.Prompt}}, assembled...)
	}
	return append(assembled, contexts...)
}

// persist saves the run and returns the text the user sees, which is the text that was stored
// (REVIEW-023).
//
// Best-effort end to end: a memory write that fails must never turn a good review into a reported
// failure. What it costs is that a failed write means the next re-review has nothing to reconcile
// against and treats every finding as new.
func (d PipelineDeps) persist(
	ctx context.Context,
	request RunRequest,
	project workspaces.Project,
	pull providers.PullRequestSummary,
	reviewText, diff string,
	changedFiles []string,
	coverage git.DiffCoverage,
	started time.Time,
) string {
	prior, _ := d.Store.CountRuns(ctx, request.ProjectID, request.PRID)
	parsed := ParseFindings(reviewText)

	findings := parsed
	var delta *ReviewDelta
	text := reviewText

	if prior > 0 {
		previous, _ := d.Store.LatestRun(ctx, request.ProjectID, request.PRID)
		merged, computed := Reconcile(DecodeFindings(previous.Findings), parsed, prior, changedFiles, request.Level)
		findings, delta = merged, &computed
		text = RenumberHeaders(text, merged[:min(len(parsed), len(merged))])
	} else {
		for i := range findings {
			// A first review's findings are all introduced by it. The parser leaves 0 as "not
			// assigned"; nothing downstream should ever see that sentinel in a stored row.
			findings[i].IntroducidoEnIter = 1
		}
	}

	// Order matters: the history is appended, **then** the banner is prepended, and the footer is
	// stamped last of all — the renderer's footer pattern is anchored to the end of the text.
	text += PersistingSection(findings, parsed) + ResolvedHistorySection(findings)
	if delta != nil {
		text = DeltaBanner(*delta) + text
	}
	text += d.footer(request.Level, started, &coverage, delta, findings)

	iter := prior + 1
	meta, _ := json.Marshal(map[string]any{
		"iter":      iter,
		"head_sha":  currentHead(ctx, project.LocalPath, pull),
		"pr_title":  pull.Title,
		"timestamp": time.Now().Format(time.RFC3339),
	})

	// The write's failure is swallowed on purpose, and this is the one place in the pipeline where
	// that is true of a database error.
	_ = d.Store.AddRun(ctx, NewRun{
		ID:          request.JobID,
		ProjectID:   project.ID,
		WorkspaceID: project.WorkspaceID,
		PRID:        request.PRID,
		Iter:        iter,
		Level:       request.Level,
		Meta:        string(meta),
		ReviewMD:    text,
		Diff:        diff,
		Findings:    EncodeFindings(findings),
	})
	return text
}

// currentHead re-resolves the head for the stored meta, tolerating a ref that no longer resolves:
// a review whose SHA could not be recorded is still a review, and the next one simply cannot
// short-circuit against it.
func currentHead(ctx context.Context, repoPath string, pull providers.PullRequestSummary) string {
	sha, _ := git.ResolveSHA(ctx, repoPath, pull.SourceBranch)
	return sha
}
