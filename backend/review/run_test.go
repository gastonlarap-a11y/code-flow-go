package review_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/activity"
	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/providers"
	"github.com/gastonlarap-a11y/code-flow/backend/review"
	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// The port of ReviewRunTests and ReviewFromLinkTests. The expensive rules live here: a review that
// re-runs when nothing changed bills for an answer nobody asked for, and one that files a cancelled
// run leaves a red row in Activity for something the user stopped on purpose.

// ---- a repository with one real commit ---------------------------------------------------------

// reviewRepo builds a repository with a `main` and a branch that changes one file, which is the
// smallest thing a review can run against.
func reviewRepo(t *testing.T) (path, headSHA string) {
	t.Helper()
	path = t.TempDir()

	run := func(args ...string) string {
		t.Helper()
		command := exec.CommandContext(t.Context(), "git", append([]string{"-C", path}, args...)...)
		command.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := command.CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}

	run("init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(path, "app.go"),
		[]byte("package main\n\nfunc Greet() string {\n\treturn \"hola\"\n}\n"), 0o600))
	run("add", ".")
	run("commit", "-m", "first")

	run("checkout", "-b", "feat/thing")
	require.NoError(t, os.WriteFile(filepath.Join(path, "app.go"),
		[]byte("package main\n\nfunc Greet() string {\n\treturn \"HOLA\"\n}\n"), 0o600))
	run("add", ".")
	run("commit", "-m", "shout")

	return path, run("rev-parse", "HEAD")
}

// ---- fakes -------------------------------------------------------------------------------------

// fakeActivity records what a finished run filed in the project's history.
type fakeActivity struct {
	recorded []activity.NewJob
	err      error
}

func (f *fakeActivity) RecordJob(_ context.Context, job activity.NewJob) (activity.JobEntry, error) {
	f.recorded = append(f.recorded, job)
	if f.err != nil {
		return activity.JobEntry{}, f.err
	}
	return activity.JobEntry{ID: job.ID, Kind: job.Kind, Status: job.Status, Label: job.Label}, nil
}

type fakeReviewer struct {
	reply    string
	err      error
	requests []ai.ReviewRequest
}

func (f *fakeReviewer) Review(_ context.Context, _ string, request ai.ReviewRequest) (ai.Result, error) {
	f.requests = append(f.requests, request)
	if f.err != nil {
		return ai.Result{}, f.err
	}
	return ai.Result{Text: f.reply}, nil
}

func (f *fakeReviewer) last(t *testing.T) ai.ReviewRequest {
	t.Helper()
	require.NotEmpty(t, f.requests, "the model was never asked anything")
	return f.requests[len(f.requests)-1]
}

type fakeWorkspace struct {
	project  workspaces.Project
	template string
	contexts []workspaces.ReviewContext
	mcps     []workspaces.MCP
}

func (f *fakeWorkspace) GetProject(context.Context, string) (workspaces.Project, error) {
	return f.project, nil
}

func (f *fakeWorkspace) GetWorkspacePrompt(context.Context, string, string) (string, error) {
	return f.template, nil
}

func (f *fakeWorkspace) ListReviewContexts(context.Context, string) ([]workspaces.ReviewContext, error) {
	return f.contexts, nil
}

func (f *fakeWorkspace) ListMCPs(context.Context, string) ([]workspaces.MCP, error) {
	return f.mcps, nil
}

// fakeFetcher records what a review asked to be brought in, and can refuse — which is the ordinary
// offline case.
type fakeFetcher struct {
	refspecs []string
	err      error
}

func (f *fakeFetcher) Fetch(context.Context, string) error { return f.err }

func (f *fakeFetcher) FetchRefspecs(_ context.Context, _, _ string, refspecs []string) error {
	f.refspecs = append(f.refspecs, refspecs...)
	return f.err
}

// listingHost answers one pull request and records nothing else.
type listingHost struct {
	*fakeHost
	pull providers.PullRequestSummary
}

func (h listingHost) List(context.Context) ([]providers.PullRequestSummary, error) {
	return []providers.PullRequestSummary{h.pull}, nil
}

func (h listingHost) Get(context.Context, int64) (providers.PullRequestSummary, error) {
	return h.pull, nil
}

const reviewReply = "📈 CALIDAD: Fiabilidad=B Seguridad=A Mantenibilidad=B\n" +
	"🚦 Quality Gate: FAILED\n\n" +
	"### 🔴 [Blocker · Bug] race-condition · F-001\n\n" +
	"El saludo se grita\n\n" +
	"📍 Ubicación: app.go:4\n" +
	"🎯 Confianza: 80\n"

// runPipeline wires a pipeline over a real repository and a fake host and model.
func runPipeline(t *testing.T, reviewer *fakeReviewer, fetcher *fakeFetcher) (review.PipelineDeps, *review.Store, string) {
	t.Helper()
	store, db := newStore(t)
	repo, _ := reviewRepo(t)

	pull := providers.PullRequestSummary{
		ID: 7, Title: "Shout the greeting", Description: "why",
		SourceBranch: "feat/thing", TargetBranch: "main",
		Status: "open", Provider: providers.ProviderGitHub, URL: "https://example.test/pr/7",
	}
	host := listingHost{fakeHost: gitHubHost(), pull: pull}

	deps := review.PipelineDeps{
		Store: review.NewStore(db, clock),
		Hosts: &fakeHosts{host: host, link: providers.PRLink{
			Provider: providers.ProviderGitHub, Number: 7,
			GitHub: providers.GitHubRepo{Host: "github.com", Owner: "acme", Repo: "widget"},
		}},
		Projects: &fakeWorkspace{project: workspaces.Project{
			ID: "p1", WorkspaceID: "w1", Name: "Repo", LocalPath: repo,
			GitHubOwner: new("acme"), GitHubRepo: new("widget"),
		}},
		AI:       reviewer,
		Fetcher:  fetcher,
		Activity: &fakeActivity{},
		Paths:    platform.NewPaths(t.TempDir()),
	}
	return deps, store, repo
}

// ---- the run -----------------------------------------------------------------------------------

func TestAFirstReviewSavesItsFindingsAndItsHistoryRow(t *testing.T) {
	reviewer := &fakeReviewer{reply: reviewReply}
	log := &fakeActivity{}
	deps, store, _ := runPipeline(t, reviewer, &fakeFetcher{})
	deps.Activity = log

	text, err := deps.Run(t.Context(), review.RunRequest{
		ProjectID: "p1", PRID: 7, JobID: "job-1", Level: "completo",
	})

	require.NoError(t, err)
	assert.Contains(t, text, "### 🔴 [Blocker · Bug] race-condition · F-001")
	assert.NotContains(t, text, "🔁 Re-revisión", "a first review has nothing to compare against")

	run, err := store.GetRun(t.Context(), "job-1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), run.Iter)
	assert.Equal(t, text, run.ReviewMD, "what the user sees is what was stored")

	findings := review.DecodeFindings(run.Findings)
	require.Len(t, findings, 1)
	assert.Equal(t, int64(1), findings[0].IntroducidoEnIter, "never the 'not assigned' sentinel")

	require.Len(t, log.recorded, 1)
	assert.Equal(t, "pr-review", log.recorded[0].Kind)
	assert.Equal(t, "done", log.recorded[0].Status)
	assert.Equal(t, "#7 Shout the greeting", log.recorded[0].Label)
}

// The payload is what the review is judged from, so what reaches it is asserted rather than assumed.
func TestTheModelIsGivenTheChangeAndTheCodeAroundIt(t *testing.T) {
	reviewer := &fakeReviewer{reply: reviewReply}
	deps, _, repo := runPipeline(t, reviewer, &fakeFetcher{})

	_, err := deps.Run(t.Context(), review.RunRequest{
		ProjectID: "p1", PRID: 7, JobID: "job-1", Level: "ultra",
	})
	require.NoError(t, err)

	request := reviewer.last(t)
	assert.Equal(t, "Shout the greeting", request.Title)
	assert.Contains(t, request.Diff, "+\treturn \"HOLA\"", "the change itself")
	assert.Contains(t, request.CodeContext, "func Greet", "and the declaration it lands in")
	assert.Equal(t, repo, request.WorkingDir, "the real clone")
	assert.True(t, request.Explorable, "a project-backed review has a checkout")
	assert.Equal(t, "ultra", request.Level)
}

// GitHub's pull request can live in a fork, where its head is on nobody's origin: one targeted
// fetch brings it in.
func TestAGitHubReviewFetchesThePullRequestsOwnHead(t *testing.T) {
	fetcher := &fakeFetcher{}
	deps, _, _ := runPipeline(t, &fakeReviewer{reply: reviewReply}, fetcher)

	_, err := deps.Run(t.Context(), review.RunRequest{
		ProjectID: "p1", PRID: 7, JobID: "job-1", Level: "completo",
	})

	require.NoError(t, err)
	assert.Contains(t, fetcher.refspecs, "+refs/pull/7/head:refs/remotes/origin/codeflow-pr-7")
}

// Offline is not a failed review: the fetch is best-effort and the diff is built from what is local.
func TestAFailedFetchDoesNotStopTheReview(t *testing.T) {
	deps, _, _ := runPipeline(t, &fakeReviewer{reply: reviewReply},
		&fakeFetcher{err: errors.New("could not resolve host")})

	text, err := deps.Run(t.Context(), review.RunRequest{
		ProjectID: "p1", PRID: 7, JobID: "job-1", Level: "completo",
	})

	require.NoError(t, err)
	assert.Contains(t, text, "F-001")
}

// Nothing pushed since the last review means no model call at all — a pure read and an early
// return, in the words a reader sees.
func TestAReviewOfAnUnchangedPullRequestShortCircuits(t *testing.T) {
	reviewer := &fakeReviewer{reply: reviewReply}
	deps, store, repo := runPipeline(t, reviewer, &fakeFetcher{})

	first, err := deps.Run(t.Context(), review.RunRequest{
		ProjectID: "p1", PRID: 7, JobID: "job-1", Level: "completo",
	})
	require.NoError(t, err)
	require.Contains(t, first, "F-001")
	require.Len(t, reviewer.requests, 1)

	second, err := deps.Run(t.Context(), review.RunRequest{
		ProjectID: "p1", PRID: 7, JobID: "job-2", Level: "completo",
	})

	require.NoError(t, err)
	assert.Contains(t, second, "🔁 Sin cambios desde la última revisión (mismo commit `")
	assert.Contains(t, second, "No se volvió a analizar.")
	assert.Len(t, reviewer.requests, 1, "the model was not asked again")

	// And nothing was filed for the second run.
	_, err = store.GetRun(t.Context(), "job-2")
	assert.ErrorIs(t, err, review.ErrNotFound)
	_ = repo
}

// A re-review over a moved head reconciles: the banner, the ids and the history all come from the
// previous run.
func TestAReReviewReconcilesAgainstTheLastOne(t *testing.T) {
	reviewer := &fakeReviewer{reply: reviewReply}
	deps, store, repo := runPipeline(t, reviewer, &fakeFetcher{})

	_, err := deps.Run(t.Context(), review.RunRequest{
		ProjectID: "p1", PRID: 7, JobID: "job-1", Level: "completo",
	})
	require.NoError(t, err)

	// Move the branch on, so the head no longer matches the stored one.
	commit(t, repo, "app.go", "package main\n\nfunc Greet() string {\n\treturn \"HOLA!\"\n}\n")

	// This time the model reports nothing, so the finding is resolved.
	reviewer.reply = "📈 CALIDAD: Fiabilidad=A Seguridad=A Mantenibilidad=A\n🚦 Quality Gate: PASSED\n\n✅ Sin hallazgos.\n"

	text, err := deps.Run(t.Context(), review.RunRequest{
		ProjectID: "p1", PRID: 7, JobID: "job-2", Level: "completo",
	})

	require.NoError(t, err)
	assert.Contains(t, text, "🔁 Re-revisión (iter 1 → 2): 0 nuevos · 0 persisten · 1 resueltos")
	assert.Contains(t, text, "### 🕘 Historial de hallazgos resueltos (trazabilidad)")
	assert.Contains(t, text, "- `race-condition` · app.go — introducido iter 1 · resuelto iter 2")

	run, err := store.GetRun(t.Context(), "job-2")
	require.NoError(t, err)
	assert.Equal(t, int64(2), run.Iter)
	findings := review.DecodeFindings(run.Findings)
	require.Len(t, findings, 1)
	assert.Equal(t, "resuelto", findings[0].Estado)
}

// A cancelled run leaves nothing behind: the user stopped it, and filing it would put a failure in
// Activity for something nobody wanted recorded.
func TestACancelledRunLeavesNothingBehind(t *testing.T) {
	log := &fakeActivity{}
	deps, store, _ := runPipeline(t,
		&fakeReviewer{err: errors.New("RUN_CANCELLED:: el usuario detuvo la ejecución")}, &fakeFetcher{})
	deps.Activity = log

	_, err := deps.Run(t.Context(), review.RunRequest{
		ProjectID: "p1", PRID: 7, JobID: "job-1", Level: "completo",
	})

	require.Error(t, err)
	assert.Empty(t, log.recorded, "no history row")
	_, err = store.GetRun(t.Context(), "job-1")
	assert.ErrorIs(t, err, review.ErrNotFound, "no saved run")
}

// Any other failure **is** filed, with the error on it: that is a run the user wants to find later.
func TestAFailedRunIsFiledWithItsError(t *testing.T) {
	log := &fakeActivity{}
	deps, _, _ := runPipeline(t, &fakeReviewer{err: errors.New("GitHub returned 500: boom")}, &fakeFetcher{})
	deps.Activity = log

	_, err := deps.Run(t.Context(), review.RunRequest{
		ProjectID: "p1", PRID: 7, JobID: "job-1", Level: "completo",
	})

	require.Error(t, err)
	require.Len(t, log.recorded, 1)
	assert.Equal(t, "error", log.recorded[0].Status)
	require.NotNil(t, log.recorded[0].Error)
	assert.Contains(t, *log.recorded[0].Error, "boom")
}

func TestAPullRequestTheHostDoesNotListIsRefused(t *testing.T) {
	deps, _, _ := runPipeline(t, &fakeReviewer{reply: reviewReply}, &fakeFetcher{})

	_, err := deps.Run(t.Context(), review.RunRequest{
		ProjectID: "p1", PRID: 999, JobID: "job-1", Level: "completo",
	})

	require.Error(t, err)
	assert.Equal(t, "Pull request not found", err.Error())
}

// The footer is stamped last, after the history — the renderer's pattern is anchored to the end of
// the text, and a footer with anything after it matches nothing at all.
func TestTheFooterIsTheLastThingInTheText(t *testing.T) {
	deps, _, _ := runPipeline(t, &fakeReviewer{reply: reviewReply}, &fakeFetcher{})

	text, err := deps.Run(t.Context(), review.RunRequest{
		ProjectID: "p1", PRID: 7, JobID: "job-1", Level: "basico",
	})

	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	footer := lines[len(lines)-1]
	assert.True(t, strings.HasPrefix(footer, "🤖 Análisis automatizado (pr-review)"), footer)
	assert.Contains(t, footer, "nivel básico")
	assert.Contains(t, footer, "diff: 1 archivos")
	assert.NotContains(t, strings.TrimPrefix(footer, "🤖 Análisis automatizado (pr-review)"), "·  ·",
		"no empty segment")
}

// ---- the link review ---------------------------------------------------------------------------

func TestALinkReviewWritesItsOwnWorkspaceAndNeverSaves(t *testing.T) {
	reviewer := &fakeReviewer{reply: reviewReply}
	deps, store, _ := runPipeline(t, reviewer, &fakeFetcher{})

	github := gitHubHost()
	github.diff = "diff --git a/app.go b/app.go\n--- a/app.go\n+++ b/app.go\n@@ -1,2 +1,2 @@\n-uno\n+dos\n"
	host := listingHost{fakeHost: github, pull: providers.PullRequestSummary{
		ID: 7, Title: "Shout the greeting", Description: "why",
		SourceBranch: "feat/thing", TargetBranch: "main", URL: "https://example.test/pr/7",
	}}
	deps.Hosts = &fakeHosts{host: host, link: providers.PRLink{
		Provider: providers.ProviderGitHub, Number: 7,
		GitHub: providers.GitHubRepo{Host: "github.com", Owner: "acme", Repo: "widget"},
	}}

	text, err := deps.RunFromLink(t.Context(), review.LinkRunRequest{
		URL: "https://github.com/acme/widget/pull/7", JobID: "job-1",
		Level: "ultra", WorkspaceID: "w1",
	})

	require.NoError(t, err)
	assert.Contains(t, text, "F-001")
	assert.Contains(t, text, "🤖 Análisis automatizado")
	assert.NotContains(t, text, "diff:", "a link review has no coverage to report")

	// Nothing is saved: no run, so a later one has nothing to reconcile against.
	_, err = store.GetRun(t.Context(), "job-1")
	assert.ErrorIs(t, err, review.ErrNotFound)

	request := reviewer.last(t)
	assert.False(t, request.Explorable, "there is no checkout")
	assert.Empty(t, request.CodeContext, "and so no code to quote around the change")
	require.NotEmpty(t, request.Contexts)
	assert.Equal(t, "Modo de revisión", request.Contexts[0].Name, "the warning comes first")
	assert.Contains(t, request.Contexts[0].Content, "SIN un clon local")

	// The two files a person can open are where the run happened.
	summary, err := os.ReadFile(filepath.Join(request.WorkingDir, "PULL_REQUEST.md"))
	require.NoError(t, err)
	assert.Contains(t, string(summary), "Shout the greeting")

	changes, err := os.ReadFile(filepath.Join(request.WorkingDir, "changes.diff"))
	require.NoError(t, err)
	assert.Contains(t, string(changes), "+dos", "the provider's own diff, for a person to scroll")
}

// An agent's own instructions frame the review ahead of the no-clone warning: they are what the
// person driving this run asked for.
func TestAnAgentsInstructionsComeBeforeTheWarning(t *testing.T) {
	reviewer := &fakeReviewer{reply: reviewReply}
	deps, _, _ := runPipeline(t, reviewer, &fakeFetcher{})
	github := gitHubHost()
	github.diff = "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-x\n+y\n"
	host := listingHost{fakeHost: github, pull: providers.PullRequestSummary{ID: 7, Title: "t"}}
	deps.Hosts = &fakeHosts{host: host, link: providers.PRLink{Provider: providers.ProviderGitHub, Number: 7}}

	_, err := deps.RunFromLink(t.Context(), review.LinkRunRequest{
		URL: "https://github.com/acme/widget/pull/7", JobID: "job-1", Level: "completo", WorkspaceID: "w1",
		Agent: &ai.AgentOverride{Provider: "claude", Model: "sonnet", Prompt: "revisa como SRE"},
	})

	require.NoError(t, err)
	contexts := reviewer.last(t).Contexts
	require.Len(t, contexts, 2)
	assert.Equal(t, "Agent", contexts[0].Name)
	assert.Equal(t, "Modo de revisión", contexts[1].Name)
}

// ---- the MCP file (REVIEW-004) -----------------------------------------------------------------

func TestTheEnabledMCPServersAreWrittenForTheRun(t *testing.T) {
	reviewer := &fakeReviewer{reply: reviewReply}
	deps, _, _ := runPipeline(t, reviewer, &fakeFetcher{})
	deps.Projects = &fakeWorkspace{
		project: workspaces.Project{ID: "p1", WorkspaceID: "w1", LocalPath: mustRepo(t, deps)},
		mcps: []workspaces.MCP{
			{Name: "docs", Command: "npx", Args: "-y @modelcontextprotocol/server-docs", Env: "TOKEN=abc\nbroken", Enabled: true},
			{Name: "off", Command: "nope", Enabled: false},
		},
	}

	_, err := deps.Run(t.Context(), review.RunRequest{
		ProjectID: "p1", PRID: 7, JobID: "job-1", Level: "completo",
	})
	require.NoError(t, err)

	path := reviewer.last(t).MCPConfig
	require.NotEmpty(t, path, "the flag only travels when a server is enabled")

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var written struct {
		Servers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(raw, &written))
	require.Contains(t, written.Servers, "docs")
	assert.NotContains(t, written.Servers, "off", "a disabled server is not written")
	assert.Equal(t, []string{"-y", "@modelcontextprotocol/server-docs"}, written.Servers["docs"].Args)
	assert.Equal(t, map[string]string{"TOKEN": "abc"}, written.Servers["docs"].Env,
		"a line with no = is dropped rather than erroring")
}

func TestNoEnabledServerMeansNoFlagAtAll(t *testing.T) {
	reviewer := &fakeReviewer{reply: reviewReply}
	deps, _, _ := runPipeline(t, reviewer, &fakeFetcher{})

	_, err := deps.Run(t.Context(), review.RunRequest{
		ProjectID: "p1", PRID: 7, JobID: "job-1", Level: "completo",
	})

	require.NoError(t, err)
	assert.Empty(t, reviewer.last(t).MCPConfig)
}

// ---- the commands ------------------------------------------------------------------------------

func TestTheRunCommandsAreRegisteredAndNameAMissingParameter(t *testing.T) {
	deps, _, _ := runPipeline(t, &fakeReviewer{reply: reviewReply}, &fakeFetcher{})

	r := bridge.NewRegistry()
	review.RegisterPipeline(r, deps)
	r.Seal()
	assert.Equal(t, []string{"review_pr_from_link", "review_pull_request"}, r.Names())

	svc := bridge.NewService(r, nil)
	for method, params := range map[string]string{
		"review_pull_request": `{"projectId":"p1","prId":7}`,
		"review_pr_from_link": `{"url":"https://github.com/acme/widget/pull/7"}`,
	} {
		t.Run(method, func(t *testing.T) {
			_, err := svc.Invoke(t.Context(), method, json.RawMessage(params))

			require.Error(t, err)
			assert.Contains(t, err.Error(), "missing required parameter")
		})
	}
}

func TestTheReviewCommandAnswersTheMarkdown(t *testing.T) {
	deps, _, _ := runPipeline(t, &fakeReviewer{reply: reviewReply}, &fakeFetcher{})
	r := bridge.NewRegistry()
	review.RegisterPipeline(r, deps)
	r.Seal()

	out, err := bridge.NewService(r, nil).Invoke(t.Context(), "review_pull_request",
		json.RawMessage(`{"projectId":"p1","prId":7,"jobId":"job-1","level":"completo","agentProvider":null,"agentModel":null,"agentPrompt":null}`))

	require.NoError(t, err)
	var text string
	require.NoError(t, json.Unmarshal(out, &text))
	assert.Contains(t, text, "F-001")
}

// ---- helpers -----------------------------------------------------------------------------------

func commit(t *testing.T, repo, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte(content), 0o600))

	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "more"}} {
		command := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		command.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := command.CombinedOutput()
		require.NoError(t, err, string(out))
	}
}

// mustRepo reaches back for the repository runPipeline built, so a case can rebuild the workspace
// around the same clone without the helper returning five values.
func mustRepo(t *testing.T, deps review.PipelineDeps) string {
	t.Helper()
	project, err := deps.Projects.GetProject(t.Context(), "p1")
	require.NoError(t, err)
	return project.LocalPath
}
