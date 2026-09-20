package tickets_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/tickets"
	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// `WI-023`'s four combinations, against a real repository: each keeps its own prompt, routing key
// and storage, and the one that was missing — a whole-branch review with **no** ticket — is the one
// the two welded axes could not express.

// reviewRepo builds a repository whose branch has one commit and one uncommitted change, so the two
// scopes have visibly different answers.
func reviewRepo(t *testing.T) string {
	t.Helper()
	path := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		command := exec.CommandContext(t.Context(), "git", append([]string{"-C", path}, args...)...)
		command.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := command.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	write := func(name, content string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(path, name), []byte(content), 0o600))
	}

	run("init", "-b", "main")
	write("app.go", "package main\n\nfunc Greet() string {\n\treturn \"hola\"\n}\n")
	run("add", ".")
	run("commit", "-m", "first")

	run("checkout", "-b", "feature/1234-exportar")
	// Committed on the branch: only the branch scope sees this.
	write("exportar.go", "package main\n\nfunc Exportar() string {\n\treturn \"pdf\"\n}\n")
	run("add", ".")
	run("commit", "-m", "exportar")

	// Uncommitted: both scopes see this.
	write("app.go", "package main\n\nfunc Greet() string {\n\treturn \"HOLA\"\n}\n")
	return path
}

type reviewFixture struct {
	deps     tickets.Deps
	store    *tickets.Store
	reviewer *fakeReviewer
	activity *fakeActivity
	repo     string
}

func newReviewFixture(t *testing.T) *reviewFixture {
	t.Helper()

	store, _ := newStore(t)
	repo := reviewRepo(t)
	reviewer := &fakeReviewer{answer: "# La revisión\n"}
	jobs := &fakeActivity{}

	return &reviewFixture{
		store:    store,
		reviewer: reviewer,
		activity: jobs,
		repo:     repo,
		deps: tickets.Deps{
			Store: store,
			Workspaces: &fakeWorkspaces{
				project:   workspaces.Project{ID: "p1", WorkspaceID: "w1", LocalPath: repo},
				workspace: workspaces.Workspace{ID: "w1"},
			},
			Credentials: fakeCredentials{pat: "the-pat"},
			AI:          reviewer,
			Activity:    jobs,
			Paths:       platform.NewPaths(t.TempDir()),
		},
	}
}

// linkTicket caches a work item, gives it a mirror on disk and links it to the branch under review.
func (f *reviewFixture) linkTicket(t *testing.T) tickets.Ticket {
	t.Helper()

	cached := sampleTicket("1234", "Exportar la factura")
	cached.MirrorPath = filepath.Join(t.TempDir(), "1234-Exportar-la-factura")

	ticket, err := f.store.Upsert(t.Context(), cached, `{
		"fields": {
			"System.Description": "<p>Hay que poder exportar la factura desde el detalle del pago.</p>",
			"Microsoft.VSTS.Common.AcceptanceCriteria":
				"<ul><li>El usuario puede exportar la factura en PDF</li><li>La exportación lleva el IVA</li></ul>"
		}
	}`)
	require.NoError(t, err)
	require.NoError(t, f.store.Link(t.Context(), "p1", "feature/1234-exportar", ticket.ID))
	return ticket
}

func (f *reviewFixture) review(ctx context.Context, scope string, withTicket bool) (string, error) {
	return f.deps.ReviewChanges(ctx, tickets.ReviewRequest{
		ProjectID: "p1", JobID: "job-1", Branch: "feature/1234-exportar",
		Scope: scope, WithTicket: withTicket, BaseRef: "main", Level: "completo",
	})
}

// ---- which diff --------------------------------------------------------------------------------

func TestTheWorkingScopeSeesOnlyWhatIsNotCommitted(t *testing.T) {
	fixture := newReviewFixture(t)

	_, err := fixture.review(t.Context(), ai.ScopeWorking, false)
	require.NoError(t, err)
	require.NotNil(t, fixture.reviewer.analyzed)

	diff := fixture.reviewer.analyzed.Diff
	assert.Contains(t, diff, "app.go", "the uncommitted change is there")
	assert.NotContains(t, diff, "exportar.go", "the branch's earlier commit is not")
}

// `GIT-039`: the branch's whole contribution is **one** comparison, not two diffs added together.
func TestTheBranchScopeSeesTheWholeContributionOnlyOnce(t *testing.T) {
	fixture := newReviewFixture(t)

	_, err := fixture.review(t.Context(), ai.ScopeBranch, false)
	require.NoError(t, err)
	require.NotNil(t, fixture.reviewer.analyzed)

	diff := fixture.reviewer.analyzed.Diff
	assert.Contains(t, diff, "exportar.go", "what the branch committed")
	assert.Contains(t, diff, "app.go", "and what it has not committed yet")

	// A file touched in a commit of the branch *and* again uncommitted must appear once: a model
	// handed the same file twice reports the same finding twice.
	assert.Equal(t, 1, strings.Count(diff, "--- app.go "), "one entry per file")
	assert.Equal(t, 1, strings.Count(diff, "--- exportar.go "))
}

func TestAnUntrackedFileIsPartOfTheBranchContribution(t *testing.T) {
	fixture := newReviewFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(fixture.repo, "nuevo.go"),
		[]byte("package main\n\nvar Nuevo = 1\n"), 0o600))

	_, err := fixture.review(t.Context(), ai.ScopeBranch, false)
	require.NoError(t, err)
	assert.Contains(t, fixture.reviewer.analyzed.Diff, "nuevo.go",
		"a file the branch adds and has not staged is part of what it contributes")
}

// ---- the SCOPE line ----------------------------------------------------------------------------

// `WI-023`: the scope reaches the model as a payload line, **not** through the prompt.
//
// `analyze_template` is a user-editable setting whose built-in text says "UNCOMMITTED changes", so
// anyone who had edited theirs would have been describing the wrong diff — silently, and only for
// the people who had customised it.
func TestTheScopeReachesTheModelAsAPayloadLine(t *testing.T) {
	tests := []struct {
		scope    string
		contains string
	}{
		{scope: ai.ScopeWorking, contains: "solo los cambios sin commitear"},
		{scope: ai.ScopeBranch, contains: "la contribución completa de la rama sobre `main`"},
	}

	for _, test := range tests {
		t.Run(test.scope, func(t *testing.T) {
			fixture := newReviewFixture(t)

			_, err := fixture.review(t.Context(), test.scope, false)
			require.NoError(t, err)

			line := ai.ScopeLine(test.scope, "main")
			assert.Contains(t, line, test.contains)
			assert.True(t, strings.HasPrefix(line, "SCOPE: "))

			// It is in the template's own text nowhere: the template is the user's to edit.
			assert.NotContains(t, fixture.reviewer.analyzed.Template, "SCOPE:")
		})
	}
}

// `WI-024`: judging uncommitted work against criteria carries a caveat, and only that combination
// does.
func TestOnlyTheUncommittedTicketReviewCarriesTheCriteriaCaveat(t *testing.T) {
	assert.Contains(t, ai.CriteriaCaveat(ai.ScopeWorking), "no verificable")
	assert.Contains(t, ai.CriteriaCaveat(ai.ScopeWorking), "ausencia de evidencia")
	assert.Empty(t, ai.CriteriaCaveat(ai.ScopeBranch), "a branch scope has the evidence")

	// The caveat says the one thing that stops the defect: with three commits done and something
	// pending, the model sees only what is pending and reports met criteria as unmet.
	assert.Contains(t, ai.CriteriaCaveat(ai.ScopeWorking), "Nunca respondas")
}

// ---- which prompt, which storage -----------------------------------------------------------------

func TestTheTicketAxisPicksTheOtherOrchestration(t *testing.T) {
	fixture := newReviewFixture(t)
	ticket := fixture.linkTicket(t)

	_, err := fixture.review(t.Context(), ai.ScopeBranch, true)
	require.NoError(t, err)

	require.NotNil(t, fixture.reviewer.judged, "the ticket half ran")
	assert.Nil(t, fixture.reviewer.analyzed, "and the analyse half did not")

	block := fixture.reviewer.judged.Ticket
	assert.Equal(t, "1234", block.ExternalID)
	assert.Equal(t, "Exportar la factura", block.Title)
	assert.Equal(t, tickets.ModeList, block.CriteriaMode)
	assert.Contains(t, block.CriteriaMarkdown, "exportar la factura en PDF")
	assert.Contains(t, block.Description, "desde el detalle del pago")

	// And it landed in its own table rather than in `review_runs`.
	stored, err := fixture.store.ReviewsForBranch(t.Context(), "p1", "feature/1234-exportar")
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.Equal(t, ticket.ID, stored[0].TicketID)
	assert.Equal(t, "# La revisión\n", stored[0].ReviewMD)
}

func TestANoTicketReviewStoresNothingInTheTicketTable(t *testing.T) {
	fixture := newReviewFixture(t)
	fixture.linkTicket(t)

	// The branch has a ticket and the run was asked not to judge it: that answer belongs in
	// `job_history`, not in the ticket's own history.
	_, err := fixture.review(t.Context(), ai.ScopeWorking, false)
	require.NoError(t, err)

	stored, err := fixture.store.ReviewsForBranch(t.Context(), "p1", "feature/1234-exportar")
	require.NoError(t, err)
	assert.Empty(t, stored)

	require.Len(t, fixture.activity.jobs, 1)
	assert.Equal(t, "analyze", fixture.activity.jobs[0].Kind)
}

func TestTheStoredVerdictIsWhatTheParserRead(t *testing.T) {
	fixture := newReviewFixture(t)
	fixture.linkTicket(t)
	fixture.reviewer.answer = strings.Join([]string{
		"# La revisión",
		"",
		"## VERIFICACIÓN DE CRITERIOS DE ACEPTACIÓN",
		"",
		"### AC-1: El usuario puede exportar la factura en PDF",
		"Veredicto: cumple",
		"Evidencia: exportar.go:1-5",
		"🎯 Confianza: 85/100",
		"",
		"## VEREDICTO DE COBERTURA",
		"",
		"Cobertura: incompleta",
		"Faltante: el IVA",
		"Resumen: falta la mitad",
	}, "\n")

	_, err := fixture.review(t.Context(), ai.ScopeBranch, true)
	require.NoError(t, err)

	stored, err := fixture.store.ReviewsForBranch(t.Context(), "p1", "feature/1234-exportar")
	require.NoError(t, err)
	require.Len(t, stored, 1)

	// Stored **parsed**: re-reading the text later with a changed parser would silently rewrite
	// history.
	require.Len(t, stored[0].Criteria, 1)
	assert.Equal(t, "AC-1", stored[0].Criteria[0].ID)
	assert.Equal(t, tickets.VerdictMet, stored[0].Criteria[0].Verdict)
	require.NotNil(t, stored[0].Coverage)
	assert.Equal(t, tickets.CoverageIncomplete, stored[0].Coverage.Coverage)
}

// ---- what the run leaves behind ------------------------------------------------------------------

func TestACancelledReviewLeavesNothingBehind(t *testing.T) {
	fixture := newReviewFixture(t)
	fixture.linkTicket(t)
	fixture.reviewer.err = errors.New("RUN_CANCELLED:: el usuario paró la ejecución")

	_, err := fixture.review(t.Context(), ai.ScopeBranch, true)
	require.Error(t, err)

	// The person who stopped it did not ask for a record of having done so.
	assert.Empty(t, fixture.activity.jobs)
	stored, listErr := fixture.store.ReviewsForBranch(t.Context(), "p1", "feature/1234-exportar")
	require.NoError(t, listErr)
	assert.Empty(t, stored)
}

func TestAFailedReviewIsFiledAsOne(t *testing.T) {
	fixture := newReviewFixture(t)
	fixture.reviewer.err = errors.New("the engine exploded")

	_, err := fixture.review(t.Context(), ai.ScopeWorking, false)
	require.Error(t, err)

	require.Len(t, fixture.activity.jobs, 1)
	assert.Equal(t, "error", fixture.activity.jobs[0].Status)
	require.NotNil(t, fixture.activity.jobs[0].Error)
	assert.Contains(t, *fixture.activity.jobs[0].Error, "exploded")
}

// `XLANG-015`: a refusal for an empty tree writes no row. Filing it left a permanent red row in
// Activity for a request nobody made, because the panel starts a run when it is merely opened.
func TestARefusalForAnEmptyTreeIsNotFiled(t *testing.T) {
	fixture := newReviewFixture(t)
	// Put the working tree back the way the last commit left it.
	require.NoError(t, os.WriteFile(filepath.Join(fixture.repo, "app.go"),
		[]byte("package main\n\nfunc Greet() string {\n\treturn \"hola\"\n}\n"), 0o600))

	_, err := fixture.review(t.Context(), ai.ScopeWorking, false)
	require.ErrorIs(t, err, ai.ErrNothingToAnalyze)
	assert.Empty(t, fixture.activity.jobs)
}

// `WI-016`: the user's notes on the ticket reach the model, and reading them writes nothing.
func TestTheUsersNotesReachTheReview(t *testing.T) {
	fixture := newReviewFixture(t)
	ticket := fixture.linkTicket(t)

	notes := filepath.Join(ticket.MirrorPath, "notes")
	require.NoError(t, os.MkdirAll(notes, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(notes, "reunion.md"),
		[]byte("el IVA se calcula aparte, lo confirmó finanzas"), 0o600))

	_, err := fixture.review(t.Context(), ai.ScopeBranch, true)
	require.NoError(t, err)

	require.NotNil(t, fixture.reviewer.judged)
	assert.Contains(t, fixture.reviewer.judged.Ticket.Notes, "lo confirmó finanzas")
}

// `WI-009`: the sync before a review is best-effort. A fetch that fails over a cache holding the
// work item runs the review anyway, because refusing would also withhold the finding half of the
// answer, which never needed the network.
func TestAFailedSyncOverAUsableCacheStillReviews(t *testing.T) {
	fixture := newReviewFixture(t)
	fixture.linkTicket(t)
	// No PAT at all, so the sync cannot even be attempted.
	fixture.deps.Credentials = fakeCredentials{err: errors.New("keychain locked")}

	text, err := fixture.review(t.Context(), ai.ScopeBranch, true)
	require.NoError(t, err)
	assert.Equal(t, "# La revisión\n", text)
	assert.Equal(t, "Exportar la factura", fixture.reviewer.judged.Ticket.Title,
		"the cached copy is what was judged")
}
