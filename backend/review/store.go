// Package review owns the durable memory of every completed pull-request review.
//
// A run is kept in the database rather than on disk so it travels with `codeflow.db`, and it holds
// the rendered review, the exact diff that was reviewed and the parsed findings. That last part is
// what a re-review reads back to work out which findings are new, which persist and which were
// resolved — the feature the whole table exists for.
//
// Phase 2 ports the *store*: listing, reading, marking and deleting saved runs. The pipeline that
// produces them arrives in Phase 5.
package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/storage"
)

// RunSummary is the list row. It is a projection, not a stored shape: `project_name` comes from a
// join and `pr_title` and `findings_count` are read out of JSON columns.
type RunSummary struct {
	ID            string `json:"id"`
	ProjectID     string `json:"project_id"`
	ProjectName   string `json:"project_name"`
	PRID          int64  `json:"pr_id"`
	PRTitle       string `json:"pr_title"`
	Iter          int64  `json:"iter"`
	Level         string `json:"level"`
	FindingsCount int64  `json:"findings_count"`
	CreatedAt     string `json:"created_at"`
}

// RunDetail is one saved run in full. `meta` and `findings` stay JSON *strings* rather than decoded
// structures: the renderer parses them itself, and re-encoding here would risk reordering keys or
// changing number formatting in a blob that other code matches against.
type RunDetail struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	PRID      int64  `json:"pr_id"`
	Iter      int64  `json:"iter"`
	Level     string `json:"level"`
	Meta      string `json:"meta"`
	ReviewMD  string `json:"review_md"`
	Diff      string `json:"diff"`
	Findings  string `json:"findings"`
	CreatedAt string `json:"created_at"`
}

// ErrNotFound is returned when a run a command names does not exist.
var ErrNotFound = errors.New("review run not found")

// The finding states (REVIEW-031). VERBATIM Spanish, and stored inside the findings JSON exactly
// as written here — the renderer reads them and the review renderer groups by them.
//
// Only the last two, plus a return to `abierto`, are reachable from a command: they are the human
// judgement "this is a false positive" or "I am not fixing this". The other two are the pipeline's.
const (
	StateOpen          = "abierto"
	StatePosted        = "posteado"
	StateResolved      = "resuelto"
	StateFalsePositive = "falso_positivo"
	StateIgnored       = "ignorado"
)

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

// ListRuns returns a workspace's saved runs, newest first.
//
// Scoped on the denormalised `workspace_id` rather than through projects, which is what lets a
// whole workspace be listed and purged without a join. Deleting a project still takes its runs —
// `project_id` carries ON DELETE CASCADE — so the two are not in tension.
//
// The join is a LEFT one, as 2.x wrote it, and COALESCE keeps `project_name` a string rather than
// null because the renderer's type declares it as one.
func (s *Store) ListRuns(ctx context.Context, workspaceID string) ([]RunSummary, error) {
	out := make([]RunSummary, 0, 16)
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `
			SELECT r.id, r.project_id, COALESCE(p.name, ''), r.pr_id,
			       COALESCE(json_extract(r.meta, '$.pr_title'), ''),
			       r.iter, r.level,
			       COALESCE(json_array_length(r.findings), 0),
			       r.created_at
			  FROM review_runs r
			  LEFT JOIN projects p ON p.id = r.project_id
			 WHERE r.workspace_id = ?
			 ORDER BY r.created_at DESC`, workspaceID)
		if err != nil {
			return fmt.Errorf("list review runs: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var run RunSummary
			if err := rows.Scan(&run.ID, &run.ProjectID, &run.ProjectName, &run.PRID, &run.PRTitle,
				&run.Iter, &run.Level, &run.FindingsCount, &run.CreatedAt); err != nil {
				return fmt.Errorf("scan review run: %w", err)
			}
			out = append(out, run)
		}
		return rows.Err()
	})
	return out, err
}

// GetRun returns one run in full, or ErrNotFound.
func (s *Store) GetRun(ctx context.Context, id string) (RunDetail, error) {
	var run RunDetail
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		err := db.QueryRowContext(ctx,
			`SELECT id, project_id, pr_id, iter, level, meta, review_md, diff, findings, created_at
			   FROM review_runs WHERE id = ?`, id).
			Scan(&run.ID, &run.ProjectID, &run.PRID, &run.Iter, &run.Level, &run.Meta,
				&run.ReviewMD, &run.Diff, &run.Findings, &run.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get review run: %w", err)
		}
		return nil
	})
	return run, err
}

// MarkFinding flips one finding's state inside a run's stored findings.
//
// The findings column is a JSON array, and this rewrites it in place. Decoding into a
// []map[string]any rather than a typed struct is deliberate: a finding carries fields this port
// has not modelled yet (`delta`, `thread_id`, `subtitulo`, the severity buckets), and decoding
// into a struct would drop every one of them on the way back out — silently, and only for runs
// somebody happened to mark.
//
// REVIEW-031 records that the specification could not establish where this write happens. It
// happens here: the renderer's own wrapper documents `estado` as "falso_positivo" | "ignorado" to
// mark, or "abierto" to clear.
func (s *Store) MarkFinding(ctx context.Context, runID, findingID, estado string, motivo *string) error {
	switch estado {
	case StateOpen, StateFalsePositive, StateIgnored:
	default:
		return fmt.Errorf("estado must be %q, %q or %q, got %q",
			StateFalsePositive, StateIgnored, StateOpen, estado)
	}

	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var encoded string
		err := tx.QueryRowContext(ctx, `SELECT findings FROM review_runs WHERE id = ?`, runID).Scan(&encoded)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read findings: %w", err)
		}

		var findings []map[string]any
		if err := json.Unmarshal([]byte(encoded), &findings); err != nil {
			return fmt.Errorf("the run's findings are not a JSON array: %w", err)
		}

		marked := false
		for _, finding := range findings {
			id, _ := finding["id"].(string)
			if id != findingID {
				continue
			}
			finding["estado"] = estado

			// Clearing the mark clears its reason with it, or a re-opened finding would keep
			// explaining why it was discarded.
			switch {
			case estado == StateOpen:
				finding["motivo_descarte"] = nil
			case motivo != nil && *motivo != "":
				finding["motivo_descarte"] = *motivo
			default:
				finding["motivo_descarte"] = nil
			}
			marked = true
			break
		}
		if !marked {
			return fmt.Errorf("finding %s is not part of run %s", findingID, runID)
		}

		// SetEscapeHTML off, as everywhere on this wire: a finding's text is full of angle
		// brackets and ampersands, and < in a stored blob is unreadable in a diff.
		var buf strings.Builder
		encoder := json.NewEncoder(&buf)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(findings); err != nil {
			return fmt.Errorf("re-encode findings: %w", err)
		}

		_, err = tx.ExecContext(ctx, `UPDATE review_runs SET findings = ? WHERE id = ?`,
			strings.TrimRight(buf.String(), "\n"), runID)
		if err != nil {
			return fmt.Errorf("write findings: %w", err)
		}
		return nil
	})
}

// NewRun is one finished review to file.
//
// The id is the job's own, reused: the run on screen and the same run reloaded from history after a
// restart are then one thing rather than two rows that happen to look alike.
type NewRun struct {
	ID          string
	ProjectID   string
	WorkspaceID string
	PRID        int64
	Iter        int64
	Level       string
	Meta        string
	ReviewMD    string
	Diff        string
	Findings    string
}

// AddRun files a finished review.
//
// `ON CONFLICT DO NOTHING`: a retry with the same job id is a silent no-op rather than a second row
// (`STORE-013`). Everything here is immutable once written except the findings, which a publish
// updates with the threads it opened.
func (s *Store) AddRun(ctx context.Context, run NewRun) error {
	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO review_runs (id, project_id, workspace_id, pr_id, iter, level, meta, review_md, diff, findings, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO NOTHING`,
			run.ID, run.ProjectID, run.WorkspaceID, run.PRID, run.Iter, run.Level,
			run.Meta, run.ReviewMD, run.Diff, run.Findings, s.clock.Now())
		if err != nil {
			return fmt.Errorf("add the review run: %w", err)
		}
		return nil
	})
}

// CountRuns is how many reviews a pull request already has. Zero means the first one, which skips
// reconciliation entirely.
func (s *Store) CountRuns(ctx context.Context, projectID string, prID int64) (int64, error) {
	var count int64
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM review_runs WHERE project_id = ? AND pr_id = ?`,
			projectID, prID).Scan(&count)
	})
	return count, err
}

// LatestRun is the newest review of a pull request, by when it was written.
//
// By `created_at` rather than by `iter`: the iteration is a counter this application maintains, and
// a row written by an older version, or one restored from an export, can carry a number that does
// not order with the rest.
func (s *Store) LatestRun(ctx context.Context, projectID string, prID int64) (RunDetail, error) {
	var run RunDetail
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		err := db.QueryRowContext(ctx,
			`SELECT id, project_id, pr_id, iter, level, meta, review_md, diff, findings, created_at
			   FROM review_runs WHERE project_id = ? AND pr_id = ?
			   ORDER BY created_at DESC LIMIT 1`, projectID, prID).
			Scan(&run.ID, &run.ProjectID, &run.PRID, &run.Iter, &run.Level, &run.Meta,
				&run.ReviewMD, &run.Diff, &run.Findings, &run.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read the latest review run: %w", err)
		}
		return nil
	})
	return run, err
}

// WriteFindings replaces a run's findings column after a publish.
//
// The **only** write that mutates a `review_runs` row after it is inserted (`STORE-013`): the
// markdown, the diff and the meta are immutable once written, and the findings are not because a
// publish records which thread each one now owns. A row that is gone is not an error — the user may
// have deleted the run from another window while the batch was going out, and the comments landed
// on the pull request either way.
func (s *Store) WriteFindings(ctx context.Context, runID, findings string) error {
	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE review_runs SET findings = ? WHERE id = ?`, findings, runID); err != nil {
			return fmt.Errorf("write findings: %w", err)
		}
		return nil
	})
}

// DeleteRun removes one saved run.
func (s *Store) DeleteRun(ctx context.Context, id string) error {
	return s.exec(ctx, `DELETE FROM review_runs WHERE id = ?`, id)
}

// DeleteRunsForPR removes every run of one pull request — the "start this PR's memory again"
// action, which is what makes the next review treat every finding as new.
func (s *Store) DeleteRunsForPR(ctx context.Context, projectID string, prID int64) error {
	return s.exec(ctx, `DELETE FROM review_runs WHERE project_id = ? AND pr_id = ?`, projectID, prID)
}

// PurgeWorkspace removes every run in a workspace.
func (s *Store) PurgeWorkspace(ctx context.Context, workspaceID string) error {
	return s.exec(ctx, `DELETE FROM review_runs WHERE workspace_id = ?`, workspaceID)
}

func (s *Store) exec(ctx context.Context, query string, args ...any) error {
	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("delete review runs: %w", err)
		}
		return nil
	})
}

// ExportRuns writes saved runs to disk and returns how many it wrote.
//
// One `PR-<n>_<timestamp>/` folder per run, holding the rendered review and the diff that produced
// it. A folder rather than a file because the pair is what makes an exported review readable
// months later: the review alone names line numbers in code the reader no longer has.
//
// id selects a single run; nil exports the whole workspace.
func (s *Store) ExportRuns(ctx context.Context, workspaceID string, id *string, destDir string) (int, error) {
	runs, err := s.ListRuns(ctx, workspaceID)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return 0, fmt.Errorf("create the destination directory: %w", err)
	}

	written := 0
	for _, summary := range runs {
		if id != nil && summary.ID != *id {
			continue
		}
		detail, err := s.GetRun(ctx, summary.ID)
		if err != nil {
			return written, err
		}

		folder := filepath.Join(destDir, fmt.Sprintf("PR-%d_%s", detail.PRID, safeStamp(detail.CreatedAt)))
		if err := os.MkdirAll(folder, 0o750); err != nil {
			return written, fmt.Errorf("create %s: %w", folder, err)
		}

		for name, content := range map[string]string{
			"REVIEW.md":    detail.ReviewMD,
			"changes.diff": detail.Diff,
		} {
			if content == "" {
				continue
			}
			// 0600: an exported review carries the full diff of the code it reviewed, which is
			// usually private, and it lands wherever the user pointed the picker.
			if err := os.WriteFile(filepath.Join(folder, name), []byte(content), 0o600); err != nil {
				return written, fmt.Errorf("write %s: %w", name, err)
			}
		}
		written++
	}
	return written, nil
}

// safeStamp turns a stored timestamp into something a filesystem accepts. Colons are illegal on
// Windows and awkward everywhere; the fractional part and the offset carry no information a human
// reading a folder name wants.
func safeStamp(timestamp string) string {
	stamp := timestamp
	if cut := strings.IndexByte(stamp, '.'); cut > 0 {
		stamp = stamp[:cut]
	}
	return strings.NewReplacer(":", "-", "T", "_").Replace(stamp)
}
