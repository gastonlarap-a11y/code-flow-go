package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/activity"
	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/gastonlarap-a11y/code-flow/backend/providers"
	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// What the pipeline needs from the rest of the application, and the small pieces it assembles for
// itself: the configuration a review runs under, the MCP file it writes, the footer it stamps and
// the ad-hoc directory a link review works in.

// Projects is the project row and the workspace settings a review is configured from.
type Projects interface {
	GetProject(ctx context.Context, id string) (workspaces.Project, error)
	GetWorkspacePrompt(ctx context.Context, workspaceID, kind string) (string, error)
	ListReviewContexts(ctx context.Context, workspaceID string) ([]workspaces.ReviewContext, error)
	ListMCPs(ctx context.Context, workspaceID string) ([]workspaces.MCP, error)
}

// Reviewer runs the model. One method, because that is all the pipeline asks of the AI layer.
type Reviewer interface {
	Review(ctx context.Context, runID string, request ai.ReviewRequest) (ai.Result, error)
}

// Activity files a finished run in the project's history.
type Activity interface {
	RecordJob(ctx context.Context, job activity.NewJob) (activity.JobEntry, error)
}

// Fetcher brings refs in before a review reads them. Every call through it is best-effort at the
// call site: a review of what is already local beats no review.
type Fetcher interface {
	Fetch(ctx context.Context, repo string) error
	FetchRefspecs(ctx context.Context, repo, remote string, refspecs []string) error
}

// reviewConfiguration is what a workspace says a review should run with.
type reviewConfiguration struct {
	Template string
	Contexts []ai.ReviewContext
	MCPs     []workspaces.MCP
}

// reviewConfig reads the workspace's methodology, its enabled contexts and its MCP servers.
//
// A blank methodology means the built-in one, not an empty prompt: clearing the field in Settings
// writes an empty string and the user means "use the default" by it.
func (d PipelineDeps) reviewConfig(ctx context.Context, workspaceID string) (reviewConfiguration, error) {
	if d.Projects == nil || workspaceID == "" {
		return reviewConfiguration{}, nil
	}

	template, err := d.Projects.GetWorkspacePrompt(ctx, workspaceID, "review_standard")
	if err != nil {
		return reviewConfiguration{}, err
	}
	stored, err := d.Projects.ListReviewContexts(ctx, workspaceID)
	if err != nil {
		return reviewConfiguration{}, err
	}
	mcps, err := d.Projects.ListMCPs(ctx, workspaceID)
	if err != nil {
		return reviewConfiguration{}, err
	}

	contexts := make([]ai.ReviewContext, 0, len(stored))
	for _, context := range stored {
		if context.Enabled {
			contexts = append(contexts, ai.ReviewContext{Name: context.Name, Content: context.Content})
		}
	}
	return reviewConfiguration{Template: template, Contexts: contexts, MCPs: mcps}, nil
}

// mcpConfig writes the per-review MCP file and answers its path (REVIEW-004).
//
// The writing itself belongs to `workspaces`, which owns the rows it comes from and is where the
// pre-commit review reads the same file from. What is decided here is only *where* it goes.
func (d PipelineDeps) mcpConfig(workspaceID string, mcps []workspaces.MCP) string {
	if d.Paths.Base() == "" {
		return ""
	}
	return workspaces.WriteMCPConfig(d.Paths.WorkspaceMCPConfig(workspaceID), mcps)
}

// fileJob records a finished review in the project's history (REVIEW-022).
//
// Best-effort on both branches: a history write that fails must not turn a good review into a
// reported failure, nor swallow the error that actually happened.
func (d PipelineDeps) fileJob(
	ctx context.Context,
	request RunRequest,
	projectID string,
	pull providers.PullRequestSummary,
	status string,
	result *string,
	failure error,
) {
	if d.Activity == nil {
		return
	}

	job := activity.NewJob{
		ID:        request.JobID,
		ProjectID: projectID,
		Kind:      "pr-review",
		Label:     fmt.Sprintf("#%d %s", pull.ID, pull.Title),
		Status:    status,
		Result:    result,
		Meta: fmt.Sprintf(`{"prId":%d,"prTitle":%s,"level":%s}`,
			pull.ID, jsonText(pull.Title), jsonText(request.Level)),
	}
	if failure != nil {
		message := failure.Error()
		job.Error = &message
	}
	_, _ = d.Activity.RecordJob(ctx, job)
}

func jsonText(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// isCancelled reports a run the user stopped. A cancelled review leaves nothing behind — no history
// row, no saved run — because the person cancelling it did not ask for a record of having done so.
func isCancelled(err error) bool {
	return err != nil && strings.Contains(err.Error(), "RUN_CANCELLED::")
}

// footer is the stats line under a review (REVIEW-038).
//
// One line, `·`-separated, and **no segment may contain a `·`**: the renderer splits on it to lay
// the line out as chips. Stamped last, after the history section, because the renderer's footer
// pattern is anchored to the end of the text — stamped earlier, with the history after it, the
// panel matched nothing and the review tab showed no stats at all.
//
// A link review has no coverage and no reconciliation, so it stamps the level and the duration
// alone.
func (d PipelineDeps) footer(
	level string,
	started time.Time,
	coverage *git.DiffCoverage,
	delta *ReviewDelta,
	findings []MemoryFinding,
) string {
	segments := []string{
		"🤖 Análisis automatizado (pr-review)",
		d.now().Format("2006-01-02 15:04"),
		"nivel " + levelLabel(level),
		"duración " + humanDuration(time.Since(started)),
	}

	if coverage != nil {
		whole := coverage.Shown - coverage.Truncated
		segments = append(segments, fmt.Sprintf("diff: %d archivos", coverage.Touched))
		segments = append(segments, fmt.Sprintf("vio %d (%d enteros, %d recortados)",
			coverage.Shown, whole, coverage.Truncated))
		if left := len(coverage.Excluded) + len(coverage.Omitted) + len(coverage.Carried); left > 0 {
			segments = append(segments, fmt.Sprintf("%d sin revisar", left))
		}
	}

	if delta != nil {
		segments = append(segments, fmt.Sprintf("%d hallazgos: %d nuevos, %d persisten, %d resueltos",
			len(findings), delta.Nuevos, delta.Persisten, delta.Resueltos))
	} else if len(findings) > 0 {
		segments = append(segments, fmt.Sprintf("%d hallazgos", len(findings)))
	}

	return "\n\n---\n" + strings.Join(segments, " · ")
}

// levelLabel is the level as the footer names it, defaulting the way the directive does.
func levelLabel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "basico", "básico":
		return "básico"
	case "ultra":
		return "ultra"
	default:
		return "completo"
	}
}

// humanDuration is `6 min 23 s`, and never carries a `·`.
func humanDuration(elapsed time.Duration) string {
	seconds := int(elapsed.Round(time.Second).Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%d s", seconds)
	}
	return fmt.Sprintf("%d min %d s", seconds/60, seconds%60)
}

func (d PipelineDeps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// NoCloneContext is what a link review is told about where it is running, `VERBATIM` (Spanish).
//
// It rides at index 0 of the contexts, and it is now **enforced** as well as stated: a link review
// gets no tools at any level and, at `ultra`, a directive that does not ask it to read the
// surrounding code. Before that, the working directory held two files while the tool grant and the
// `ultra` directive both said otherwise — three instructions reaching the model down two channels
// of one invocation, only one of which could be true.
const NoCloneContext = "Esta revisión corre SIN un clon local del repositorio. Por stdin recibes el " +
	"diff completo del pull request, y el directorio de trabajo solo contiene `PULL_REQUEST.md` y " +
	"`changes.diff`. No intentes explorar el árbol del repositorio ni abrir archivos que no estén " +
	"ahí. Basa la revisión en el diff: cuando un hallazgo dependa de código que no ves (una función " +
	"llamada pero no incluida, un contrato definido en otro archivo), decláralo explícitamente y " +
	"baja la confianza en consecuencia, o clasifícalo como Security Hotspot en lugar de afirmar un " +
	"bug que no puedes demostrar."

// linkReviewWorkspace writes the two files a link review works inside, and answers the directory
// (REVIEW-009).
//
// Reused and overwritten for the same link, so re-reviewing one pull request does not accumulate
// directories. Nothing here ever deletes one for a link that is never revisited.
func (d PipelineDeps) linkReviewWorkspace(link providers.PRLink, pull providers.PullRequestSummary, diff string) (string, error) {
	directory := filepath.Join(d.Paths.PRLinkReviews(), linkSlug(link))
	// 0700 like the rest of `{base}`: a pull request's diff is the user's to read, not the
	// machine's other accounts'.
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("make the review directory: %w", err)
	}

	description := strings.TrimSpace(pull.Description)
	if description == "" {
		// VERBATIM, Spanish: it is what the file shows a reader.
		description = "(sin descripción)"
	}

	summary := fmt.Sprintf("# %s\n\n- Autor: %s\n- Rama origen: %s\n- Rama destino: %s\n- URL: %s\n\n%s\n",
		pull.Title, pull.Author, pull.SourceBranch, pull.TargetBranch, pull.URL, description)

	if err := os.WriteFile(filepath.Join(directory, "PULL_REQUEST.md"), []byte(summary), 0o600); err != nil {
		return "", fmt.Errorf("write PULL_REQUEST.md: %w", err)
	}
	// The diff as the provider wrote it, not the reshaped one: this file is for a person to scroll,
	// and the shaped version is what the model reads.
	if err := os.WriteFile(filepath.Join(directory, "changes.diff"), []byte(diff), 0o600); err != nil {
		return "", fmt.Errorf("write changes.diff: %w", err)
	}
	return directory, nil
}

// linkSlug names the directory after the pull request it holds.
func linkSlug(link providers.PRLink) string {
	if link.Provider == providers.ProviderGitHub {
		return slugify(fmt.Sprintf("github-%s-%s-%s-%d",
			link.GitHub.Host, link.GitHub.Owner, link.GitHub.Repo, link.Number))
	}
	return slugify(fmt.Sprintf("azure-%s-%s-%s-%d",
		link.Azure.Org, link.Azure.Project, link.Azure.Repo, link.Number))
}

// slugify maps everything that is not `[A-Za-z0-9_-]` to `-`, so the name is a directory on every
// platform this ships to.
func slugify(value string) string {
	out := make([]rune, 0, len(value))
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}
