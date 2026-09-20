package tickets_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/storage"
	"github.com/gastonlarap-a11y/code-flow/backend/tickets"
)

var clock = storage.FixedClock{At: time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)}

func newStore(t *testing.T) (*tickets.Store, *storage.DB) {
	t.Helper()
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "codeflow.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workspaces (id, name, created_at) VALUES ('w1', 'Work', 'now')`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO projects (id, workspace_id, name, local_path, created_at)
			 VALUES ('p1', 'w1', 'payments-api', '/tmp/api', 'now')`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO projects (id, workspace_id, name, local_path, created_at)
			 VALUES ('p2', 'w1', 'payments-web', '/tmp/web', 'now')`)
		return err
	}))
	return tickets.NewStore(db, clock), db
}

func sampleTicket(externalID, title string) tickets.Ticket {
	return tickets.Ticket{
		ID:           tickets.ID("azure", "contoso", "Payments", externalID),
		Provider:     "azure",
		Org:          "contoso",
		Project:      "Payments",
		ExternalID:   externalID,
		Title:        title,
		State:        "Active",
		WorkItemType: "User Story",
		WebURL:       "https://dev.azure.com/contoso/Payments/_workitems/edit/" + externalID,
		Rev:          3,
		MirrorPath:   "/root/contoso/Payments/" + externalID + "-" + tickets.Slug(title),
	}
}

func TestUpsertRoundTrip(t *testing.T) {
	store, _ := newStore(t)

	stored, err := store.Upsert(t.Context(), sampleTicket("1234", "Exportar la factura"), `{"id":1234}`)
	require.NoError(t, err)
	assert.Equal(t, clock.Now(), stored.SyncedAt)

	read, err := store.Get(t.Context(), stored.ID)
	require.NoError(t, err)
	assert.Equal(t, stored, read)

	payload, err := store.RawPayload(t.Context(), stored.ID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":1234}`, payload)
}

func TestGetAnswersNotFoundRatherThanAnEmptyTicket(t *testing.T) {
	store, _ := newStore(t)

	_, err := store.Get(t.Context(), "azure:contoso:Payments:9999")
	assert.ErrorIs(t, err, tickets.ErrNotFound)
}

// `WI-021`: a ticket worked on from two branches comes back as one entry with two links.
//
// `SELECT DISTINCT` was what threw the branch away — the join already produces a row per link — so
// the query groups instead of collapsing.
func TestListGroupsATicketWorkedOnFromTwoBranches(t *testing.T) {
	store, _ := newStore(t)

	ticket, err := store.Upsert(t.Context(), sampleTicket("1234", "Exportar la factura"), "{}")
	require.NoError(t, err)
	other, err := store.Upsert(t.Context(), sampleTicket("5678", "Importar pagos"), "{}")
	require.NoError(t, err)

	require.NoError(t, store.Link(t.Context(), "p1", "feature/1234-exportar", ticket.ID))
	require.NoError(t, store.Link(t.Context(), "p1", "fix/1234-el-iva", ticket.ID))
	require.NoError(t, store.Link(t.Context(), "p1", "feature/5678", other.ID))
	// Another repository's link to the same ticket: this list is scoped to a project, so it is not
	// in the answer.
	require.NoError(t, store.Link(t.Context(), "p2", "feature/1234-exportar", ticket.ID))

	listed, err := store.List(t.Context(), "p1")
	require.NoError(t, err)
	require.Len(t, listed, 2)

	byID := map[string]tickets.WithLinks{}
	for _, entry := range listed {
		byID[entry.Ticket.ID] = entry
	}

	exporting := byID[ticket.ID]
	require.Len(t, exporting.Links, 2, "two branches, two links — not one chosen arbitrarily")
	assert.Equal(t, "feature/1234-exportar", exporting.Links[0].Branch, "links are ordered by branch")
	assert.Equal(t, "fix/1234-el-iva", exporting.Links[1].Branch)

	for _, link := range exporting.Links {
		assert.Equal(t, "payments-api", link.ProjectName, "an id on screen answers nothing")
	}
	assert.Len(t, byID[other.ID].Links, 1)
}

func TestListIsEmptyRatherThanNilForAProjectWithNoTickets(t *testing.T) {
	store, _ := newStore(t)

	listed, err := store.List(t.Context(), "p1")
	require.NoError(t, err)
	assert.NotNil(t, listed, "a nil slice marshals as null and the panel that maps over it crashes")
	assert.Empty(t, listed)

	encoded, err := json.Marshal(listed)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(encoded))
}

// One ticket per branch: the key is `(project_id, branch)`, so linking a second one replaces the
// first rather than adding a row nothing would ever read.
func TestLinkingASecondTicketToOneBranchReplacesTheFirst(t *testing.T) {
	store, _ := newStore(t)

	first, err := store.Upsert(t.Context(), sampleTicket("1234", "Exportar"), "{}")
	require.NoError(t, err)
	second, err := store.Upsert(t.Context(), sampleTicket("5678", "Importar"), "{}")
	require.NoError(t, err)

	require.NoError(t, store.Link(t.Context(), "p1", "feature/algo", first.ID))
	require.NoError(t, store.Link(t.Context(), "p1", "feature/algo", second.ID))

	linked, err := store.ForBranch(t.Context(), "p1", "feature/algo")
	require.NoError(t, err)
	assert.Equal(t, second.ID, linked.ID)

	listed, err := store.List(t.Context(), "p1")
	require.NoError(t, err)
	assert.Len(t, listed, 1)
}

func TestForBranchAnswersOnlyFromTheExplicitLink(t *testing.T) {
	store, _ := newStore(t)

	_, err := store.Upsert(t.Context(), sampleTicket("1234", "Exportar"), "{}")
	require.NoError(t, err)

	// The branch name looks exactly like work item 1234, and that is not enough: the heuristic is a
	// suggestion and never a link, so no review is judged against a ticket nobody chose.
	_, err = store.ForBranch(t.Context(), "p1", "feature/1234-exportar")
	assert.ErrorIs(t, err, tickets.ErrNotFound)
}

func TestUnlinkIsTheOnlyDelete(t *testing.T) {
	store, _ := newStore(t)

	ticket, err := store.Upsert(t.Context(), sampleTicket("1234", "Exportar"), "{}")
	require.NoError(t, err)
	require.NoError(t, store.Link(t.Context(), "p1", "feature/1234", ticket.ID))
	require.NoError(t, store.Unlink(t.Context(), "p1", "feature/1234"))

	_, err = store.ForBranch(t.Context(), "p1", "feature/1234")
	assert.ErrorIs(t, err, tickets.ErrNotFound)

	// The ticket itself stays cached: unlinking a branch is not forgetting the work item.
	_, err = store.Get(t.Context(), ticket.ID)
	assert.NoError(t, err)
}

// `WI-008`'s corpus: at most twenty others, of the same board and type, excluding this one.
func TestOthersOfType(t *testing.T) {
	store, _ := newStore(t)

	for i := range 25 {
		id := string(rune('a'+i)) + "00"
		ticket := sampleTicket(id, "Historia "+id)
		_, err := store.Upsert(t.Context(), ticket, `{"fields":{"System.Title":"`+id+`"}}`)
		require.NoError(t, err)
	}
	// A different type on the same board, which must not be compared against.
	bug := sampleTicket("9999", "Un bug")
	bug.WorkItemType = "Bug"
	_, err := store.Upsert(t.Context(), bug, `{"fields":{}}`)
	require.NoError(t, err)

	// The exclusion is by ticket **id**, not by external id: the id is what the primary key holds.
	others, err := store.OthersOfType(t.Context(), "contoso", "Payments", "User Story",
		tickets.ID("azure", "contoso", "Payments", "a00"))
	require.NoError(t, err)
	assert.Len(t, others, 20)
	for _, payload := range others {
		assert.NotContains(t, payload, `"System.Title":"a00"`, "never the ticket being read")
	}
}

func TestAddReviewAndListForBranch(t *testing.T) {
	store, _ := newStore(t)

	ticket, err := store.Upsert(t.Context(), sampleTicket("1234", "Exportar"), "{}")
	require.NoError(t, err)

	confidence := int64(80)
	coverage := &tickets.Coverage{
		Coverage: tickets.CoverageIncomplete,
		Missing:  "AC-3",
		Summary:  "falta el envío",
		Relevant: true,
	}
	meta, err := json.Marshal(map[string]any{"coverage": coverage, "scope": "branch"})
	require.NoError(t, err)

	require.NoError(t, store.AddReview(t.Context(), tickets.NewReview{
		ID:          "run-1",
		ProjectID:   "p1",
		WorkspaceID: "w1",
		TicketID:    ticket.ID,
		Branch:      "feature/1234",
		BaseRef:     "main",
		HeadSHA:     "abc1234",
		Level:       "completo",
		Meta:        string(meta),
		ReviewMD:    "# La revisión",
		Diff:        "diff --git a b",
		Criteria: []tickets.CriterionVerdict{{
			ID: "AC-1", Criterion: "Exporta", Verdict: tickets.VerdictMet,
			Evidence: "a.go:1-2", Confidence: &confidence,
		}},
		Coverage: coverage,
	}))

	listed, err := store.ReviewsForBranch(t.Context(), "p1", "feature/1234")
	require.NoError(t, err)
	require.Len(t, listed, 1)

	stored := listed[0]
	assert.Equal(t, "run-1", stored.ID)
	assert.Equal(t, "# La revisión", stored.ReviewMD)
	require.Len(t, stored.Criteria, 1)
	assert.Equal(t, tickets.VerdictMet, stored.Criteria[0].Verdict)
	require.NotNil(t, stored.Criteria[0].Confidence)
	assert.Equal(t, int64(80), *stored.Criteria[0].Confidence)

	require.NotNil(t, stored.Coverage)
	assert.Equal(t, tickets.CoverageIncomplete, stored.Coverage.Coverage)
	assert.True(t, stored.Coverage.Relevant)
}

// The coverage word a history list filters on is its own column, and it is written from the parsed
// block rather than re-derived on read.
func TestAddReviewWritesTheCoverageWordToItsOwnColumn(t *testing.T) {
	store, db := newStore(t)

	ticket, err := store.Upsert(t.Context(), sampleTicket("1234", "Exportar"), "{}")
	require.NoError(t, err)
	require.NoError(t, store.AddReview(t.Context(), tickets.NewReview{
		ID: "run-1", ProjectID: "p1", WorkspaceID: "w1", TicketID: ticket.ID,
		Branch: "feature/1234", ReviewMD: "# x",
		Coverage: &tickets.Coverage{Coverage: tickets.CoverageComplete},
	}))

	var word string
	require.NoError(t, db.Read(t.Context(), func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx,
			`SELECT coverage_verdict FROM ticket_review_runs WHERE id = 'run-1'`).Scan(&word)
	}))
	assert.Equal(t, tickets.CoverageComplete, word)
}

// A row whose payload will not parse still renders its markdown, so one bad row cannot take the
// history list down.
func TestARowWithUnparseableCriteriaStillReturnsItsMarkdown(t *testing.T) {
	store, db := newStore(t)

	ticket, err := store.Upsert(t.Context(), sampleTicket("1234", "Exportar"), "{}")
	require.NoError(t, err)

	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO ticket_review_runs (id, project_id, workspace_id, ticket_id, branch,
			                                base_ref, head_sha, level, meta, review_md, criteria,
			                                created_at)
			VALUES ('bad', 'p1', 'w1', ?, 'feature/1234', 'main', 'abc', 'completo',
			        'no es json', '# Sigue siendo legible', 'tampoco es json', 'now')`, ticket.ID)
		return err
	}))

	listed, err := store.ReviewsForBranch(t.Context(), "p1", "feature/1234")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "# Sigue siendo legible", listed[0].ReviewMD)
	assert.Empty(t, listed[0].Criteria)
	assert.NotNil(t, listed[0].Criteria, "empty, never nil")
	assert.Nil(t, listed[0].Coverage)
}

func TestReviewsForBranchIsNewestFirst(t *testing.T) {
	store, db := newStore(t)

	ticket, err := store.Upsert(t.Context(), sampleTicket("1234", "Exportar"), "{}")
	require.NoError(t, err)

	for _, run := range []struct{ id, at string }{{"old", "2026-09-17T10:00:00Z"}, {"new", "2026-09-18T10:00:00Z"}} {
		require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO ticket_review_runs (id, project_id, workspace_id, ticket_id, branch,
				                                base_ref, head_sha, level, review_md, created_at)
				VALUES (?, 'p1', 'w1', ?, 'feature/1234', 'main', 'abc', 'completo', '# x', ?)`,
				run.id, ticket.ID, run.at)
			return err
		}))
	}

	listed, err := store.ReviewsForBranch(t.Context(), "p1", "feature/1234")
	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, "new", listed[0].ID)
}
