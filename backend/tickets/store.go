package tickets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gastonlarap-a11y/code-flow/backend/storage"
)

// The three tables this feature owns rows in: `tickets`, `ticket_links` and `ticket_review_runs`
// (`03-storage.md`).

// Ticket is a cached work item, in the shape the renderer types it.
type Ticket struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Org      string `json:"org"`
	Project  string `json:"project"`
	// ExternalID is text, not a number: Azure numbers work items and Jira names them.
	ExternalID   string  `json:"external_id"`
	Title        string  `json:"title"`
	State        string  `json:"state"`
	WorkItemType string  `json:"work_item_type"`
	AssignedTo   *string `json:"assigned_to"`
	WebURL       string  `json:"web_url"`
	Rev          int64   `json:"rev"`
	MirrorPath   string  `json:"mirror_path"`
	SyncedAt     string  `json:"synced_at"`
}

// Link is one branch a ticket is work for.
type Link struct {
	ProjectID string `json:"project_id"`
	// ProjectName is the repository's own name — an id on screen answers nothing.
	ProjectName string `json:"project_name"`
	Branch      string `json:"branch"`
}

// WithLinks is a cached ticket and the branches it is work for.
//
// Genuinely plural: `ticket_links` is keyed `(project_id, branch)`, so one work item can be the work
// of two branches, and a list row that does not say which is unreadable as soon as a repository has
// more than one.
type WithLinks struct {
	Ticket Ticket `json:"ticket"`
	Links  []Link `json:"links"`
}

// Summary is one row of a picker — what a list shows, fetched without the rest of the work item.
type Summary struct {
	ExternalID   string  `json:"external_id"`
	Title        string  `json:"title"`
	State        string  `json:"state"`
	WorkItemType string  `json:"work_item_type"`
	AssignedTo   *string `json:"assigned_to"`
}

// ReviewResult is one finished ticket review (WI-013).
type ReviewResult struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	TicketID  string `json:"ticket_id"`
	Branch    string `json:"branch"`
	BaseRef   string `json:"base_ref"`
	HeadSHA   string `json:"head_sha"`
	Level     string `json:"level"`
	ReviewMD  string `json:"review_md"`
	// Criteria and Coverage are stored **parsed**: they came from a model's answer, and re-reading
	// that text later with a changed parser would silently rewrite history.
	Criteria  []CriterionVerdict `json:"criteria"`
	Coverage  *Coverage          `json:"coverage"`
	CreatedAt string             `json:"created_at"`
}

// ErrNotFound is what a command naming a ticket that is not cached answers.
var ErrNotFound = errors.New("ticket not found")

// Store is this feature's queries.
type Store struct {
	db    *storage.DB
	clock storage.Clock
}

// NewStore wires the store.
func NewStore(db *storage.DB, clock storage.Clock) *Store {
	if clock == nil {
		clock = storage.SystemClock{}
	}
	return &Store{db: db, clock: clock}
}

// The projection every read of a ticket uses, in the order scanTicket expects. Written out twice
// rather than derived, because a query is read as text and a generated column list is not.
const (
	ticketColumns = `id, provider, org, project, external_id, title, state, work_item_type,
	                 assigned_to, web_url, rev, mirror_path, synced_at`

	joinedTicketColumns = `t.id, t.provider, t.org, t.project, t.external_id, t.title, t.state,
	                       t.work_item_type, t.assigned_to, t.web_url, t.rev, t.mirror_path,
	                       t.synced_at`
)

func scanTicket(scan func(...any) error) (Ticket, error) {
	var ticket Ticket
	err := scan(&ticket.ID, &ticket.Provider, &ticket.Org, &ticket.Project, &ticket.ExternalID,
		&ticket.Title, &ticket.State, &ticket.WorkItemType, &ticket.AssignedTo, &ticket.WebURL,
		&ticket.Rev, &ticket.MirrorPath, &ticket.SyncedAt)
	return ticket, err
}

// Get reads one cached ticket by id.
func (s *Store) Get(ctx context.Context, id string) (Ticket, error) {
	var ticket Ticket
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		row := db.QueryRowContext(ctx, `SELECT `+ticketColumns+` FROM tickets WHERE id = ?`, id)

		found, err := scanTicket(row.Scan)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read ticket %s: %w", id, err)
		}
		ticket = found
		return nil
	})
	return ticket, err
}

// RawPayload is the cached `raw_json` of a ticket — what the mirror is rewritten from with no fetch,
// and what the criteria are re-read from.
func (s *Store) RawPayload(ctx context.Context, id string) (string, error) {
	var payload string
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		row := db.QueryRowContext(ctx, `SELECT raw_json FROM tickets WHERE id = ?`, id)
		switch err := row.Scan(&payload); {
		case errors.Is(err, sql.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return fmt.Errorf("read ticket payload %s: %w", id, err)
		}
		return nil
	})
	return payload, err
}

// Upsert writes a fetched work item into the cache and answers it as stored.
//
// The mirror path is **not** recomputed on an existing row: `MirrorFor` decides it once, and a
// ticket renamed on the board keeps the directory its notes are in (`WI-022`).
func (s *Store) Upsert(ctx context.Context, ticket Ticket, rawJSON string) (Ticket, error) {
	ticket.SyncedAt = s.clock.Now()

	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO tickets (id, provider, org, project, external_id, title, state,
			                     work_item_type, assigned_to, web_url, rev, raw_json,
			                     mirror_path, synced_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
			    title          = excluded.title,
			    state          = excluded.state,
			    work_item_type = excluded.work_item_type,
			    assigned_to    = excluded.assigned_to,
			    web_url        = excluded.web_url,
			    rev            = excluded.rev,
			    raw_json       = excluded.raw_json,
			    mirror_path    = excluded.mirror_path,
			    synced_at      = excluded.synced_at`,
			ticket.ID, ticket.Provider, ticket.Org, ticket.Project, ticket.ExternalID, ticket.Title,
			ticket.State, ticket.WorkItemType, ticket.AssignedTo, ticket.WebURL, ticket.Rev,
			rawJSON, ticket.MirrorPath, ticket.SyncedAt)
		if err != nil {
			return fmt.Errorf("upsert ticket %s: %w", ticket.ID, err)
		}
		return nil
	})
	if err != nil {
		return Ticket{}, err
	}
	return ticket, nil
}

// List answers a project's tickets with the `(project, branch)` pairs each is linked to (WI-021).
//
// Scoped to a **project**, not a workspace: the module answers "what is this repository working on",
// and mixing in another repository's tickets answers something this view never asked.
//
// It groups rather than using `SELECT DISTINCT`, and that distinction is the defect it was written
// to fix: the join already produces one row per link, so collapsing threw the branch away and a
// ticket worked on from two branches came back with one of them chosen arbitrarily.
func (s *Store) List(ctx context.Context, projectID string) ([]WithLinks, error) {
	out := make([]WithLinks, 0, 16)

	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `
			SELECT `+joinedTicketColumns+`,
			       l.project_id, COALESCE(p.name, ''), l.branch
			  FROM tickets t
			  JOIN ticket_links l ON l.ticket_id = t.id
			  LEFT JOIN projects p ON p.id = l.project_id
			 WHERE l.project_id = ?
			 ORDER BY t.synced_at DESC, l.branch ASC`, projectID)
		if err != nil {
			return fmt.Errorf("list tickets: %w", err)
		}
		defer func() { _ = rows.Close() }()

		byID := map[string]int{}
		for rows.Next() {
			var link Link
			ticket, err := scanTicketWithLink(rows, &link)
			if err != nil {
				return err
			}

			index, seen := byID[ticket.ID]
			if !seen {
				byID[ticket.ID] = len(out)
				out = append(out, WithLinks{Ticket: ticket, Links: make([]Link, 0, 2)})
				index = len(out) - 1
			}
			out[index].Links = append(out[index].Links, link)
		}
		return rows.Err()
	})
	return out, err
}

func scanTicketWithLink(rows *sql.Rows, link *Link) (Ticket, error) {
	var ticket Ticket
	err := rows.Scan(&ticket.ID, &ticket.Provider, &ticket.Org, &ticket.Project, &ticket.ExternalID,
		&ticket.Title, &ticket.State, &ticket.WorkItemType, &ticket.AssignedTo, &ticket.WebURL,
		&ticket.Rev, &ticket.MirrorPath, &ticket.SyncedAt,
		&link.ProjectID, &link.ProjectName, &link.Branch)
	if err != nil {
		return Ticket{}, fmt.Errorf("scan ticket row: %w", err)
	}
	return ticket, nil
}

// Link records that a branch is work for a ticket. One ticket per branch: the key is
// `(project_id, branch)`, so linking a second one replaces the first.
func (s *Store) Link(ctx context.Context, projectID, branch, ticketID string) error {
	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO ticket_links (project_id, branch, ticket_id, linked_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(project_id, branch) DO UPDATE SET
			    ticket_id = excluded.ticket_id,
			    linked_at = excluded.linked_at`,
			projectID, branch, ticketID, s.clock.Now())
		if err != nil {
			return fmt.Errorf("link branch %s: %w", branch, err)
		}
		return nil
	})
}

// Unlink removes a branch's link. **The only `DELETE` in this feature**: nothing removes a row when
// a git branch is deleted, because a merged branch is deleted as a matter of course and the record
// of what it was work for is precisely what you want afterwards (`WI-021`).
func (s *Store) Unlink(ctx context.Context, projectID, branch string) error {
	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`DELETE FROM ticket_links WHERE project_id = ? AND branch = ?`, projectID, branch)
		if err != nil {
			return fmt.Errorf("unlink branch %s: %w", branch, err)
		}
		return nil
	})
}

// ForBranch answers the ticket a branch is **explicitly** linked to.
//
// The name heuristic never answers here (`WI-006`): a review judged against a work item nobody chose
// is a review of the wrong requirements, presented with the same confidence as a right one.
func (s *Store) ForBranch(ctx context.Context, projectID, branch string) (Ticket, error) {
	var ticket Ticket
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		row := db.QueryRowContext(ctx, `
			SELECT `+joinedTicketColumns+`
			  FROM tickets t
			  JOIN ticket_links l ON l.ticket_id = t.id
			 WHERE l.project_id = ? AND l.branch = ?`, projectID, branch)

		found, err := scanTicket(row.Scan)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read the ticket for branch %s: %w", branch, err)
		}
		ticket = found
		return nil
	})
	return ticket, err
}

// OthersOfType is the same field on other cached work items of one board and type, for the template
// comparison (`WI-008`). At most twenty, newest first.
func (s *Store) OthersOfType(ctx context.Context, org, project, workItemType, excludeID string) ([]string, error) {
	payloads := make([]string, 0, 20)

	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `
			SELECT raw_json FROM tickets
			 WHERE org = ? AND project = ? AND work_item_type = ? AND id <> ?
			 ORDER BY synced_at DESC LIMIT 20`, org, project, workItemType, excludeID)
		if err != nil {
			return fmt.Errorf("read other tickets of this type: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var payload string
			if err := rows.Scan(&payload); err != nil {
				return fmt.Errorf("scan a cached payload: %w", err)
			}
			payloads = append(payloads, payload)
		}
		return rows.Err()
	})
	return payloads, err
}

// NewReview is one finished ticket review, on its way into the table.
type NewReview struct {
	ID          string
	ProjectID   string
	WorkspaceID string
	TicketID    string
	Branch      string
	BaseRef     string
	HeadSHA     string
	Level       string
	Meta        string
	ReviewMD    string
	Diff        string
	Findings    string
	Criteria    []CriterionVerdict
	Coverage    *Coverage
}

// AddReview stores a finished ticket review (WI-013).
//
// `coverage_verdict` holds the single word a history list filters on; the sentences explaining it
// ride in `meta` beside the provider and the model that produced the run.
func (s *Store) AddReview(ctx context.Context, review NewReview) error {
	criteria, err := json.Marshal(nonNilCriteria(review.Criteria))
	if err != nil {
		return fmt.Errorf("encode the criteria verdicts: %w", err)
	}

	verdict := ""
	if review.Coverage != nil {
		verdict = review.Coverage.Coverage
	}

	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO ticket_review_runs (id, project_id, workspace_id, ticket_id, branch,
			                                base_ref, head_sha, level, meta, review_md, diff,
			                                findings, criteria, coverage_verdict, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO NOTHING`,
			review.ID, review.ProjectID, review.WorkspaceID, review.TicketID, review.Branch,
			review.BaseRef, review.HeadSHA, review.Level, defaultJSON(review.Meta, "{}"),
			review.ReviewMD, review.Diff, defaultJSON(review.Findings, "[]"), string(criteria),
			verdict, s.clock.Now())
		if err != nil {
			return fmt.Errorf("store the ticket review: %w", err)
		}
		return nil
	})
}

func defaultJSON(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func nonNilCriteria(criteria []CriterionVerdict) []CriterionVerdict {
	if criteria == nil {
		return []CriterionVerdict{}
	}
	return criteria
}

// ReviewsForBranch lists a branch's stored reviews, newest first.
//
// `diff` is deliberately not selected: it is the largest column in the table and it exists so a
// stored verdict is re-checkable, not so it can be listed.
//
// A row whose JSON will not parse still returns its markdown — one bad row cannot take the history
// list down, which is the failure mode of decoding eagerly and returning the error.
func (s *Store) ReviewsForBranch(ctx context.Context, projectID, branch string) ([]ReviewResult, error) {
	out := make([]ReviewResult, 0, 8)

	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `
			SELECT id, project_id, ticket_id, branch, base_ref, head_sha, level, review_md,
			       criteria, meta, created_at
			  FROM ticket_review_runs
			 WHERE project_id = ? AND branch = ?
			 ORDER BY created_at DESC`, projectID, branch)
		if err != nil {
			return fmt.Errorf("list ticket reviews: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var review ReviewResult
			var criteria, meta string

			if err := rows.Scan(&review.ID, &review.ProjectID, &review.TicketID, &review.Branch,
				&review.BaseRef, &review.HeadSHA, &review.Level, &review.ReviewMD,
				&criteria, &meta, &review.CreatedAt); err != nil {
				return fmt.Errorf("scan a ticket review: %w", err)
			}

			review.Criteria = decodeCriteria(criteria)
			review.Coverage = decodeCoverage(meta)
			out = append(out, review)
		}
		return rows.Err()
	})
	return out, err
}

// decodeCriteria tolerates a payload an older parser wrote: an unreadable one renders as no table
// rather than as no review.
func decodeCriteria(payload string) []CriterionVerdict {
	criteria := []CriterionVerdict{}
	if payload == "" {
		return criteria
	}
	if err := json.Unmarshal([]byte(payload), &criteria); err != nil {
		return []CriterionVerdict{}
	}
	return criteria
}

// decodeCoverage reads the coverage block back out of `meta`, where the sentences live beside the
// word `coverage_verdict` keeps for filtering.
func decodeCoverage(meta string) *Coverage {
	if meta == "" {
		return nil
	}
	var stored struct {
		Coverage *Coverage `json:"coverage"`
	}
	if err := json.Unmarshal([]byte(meta), &stored); err != nil {
		return nil
	}
	return stored.Coverage
}
