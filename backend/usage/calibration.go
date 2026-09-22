package usage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

/*
Learning the ceiling nobody publishes (USAGE-006).

The consumption is measurable and the plan is discoverable, but the number they should be divided
by is not: Anthropic does not publish the token limit of a Pro or Max window, the CLI does not store
it, and the limits move with demand. So the panel does not invent one.

It waits instead. The app **already** recognises a provider saying "you have hit your limit" —
`ai.QuotaSignal` matches it and the renderer gets `QUOTA_EXCEEDED::`. The moment that happens, what
the window held is, by definition, the ceiling for that plan and that window. It is recorded, and
from then on the percentage is a real division by a number the user actually reached.

The consequence is deliberate and stated in the panel: **before the first time you run out, there is
no percentage** — only consumption, rate and reset. A guess dressed as a measurement would be worse
than the gap.

A ceiling only ever moves up. A limit reached early in a window says nothing about the window's
size; a bigger number later says the earlier one was too small.
*/

// Ceiling is a limit this app watched the user reach.
type Ceiling struct {
	Provider string
	Plan     string
	Window   WindowKind
	Tokens   int64
	SeenAt   time.Time
}

// WindowKind names which of a subscription's two meters a number belongs to.
type WindowKind string

const (
	// SessionWindow is the rolling block (SessionLength).
	SessionWindow WindowKind = "session"
	// WeekWindow is the trailing seven days.
	WeekWindow WindowKind = "week"
)

// Store persists what calibration learns. Small on purpose: one row per provider, plan and window.
type Store struct{ db execer }

// execer is the slice of the app's database handle this package needs, declared here because that
// is where it is consumed.
type execer interface {
	Write(ctx context.Context, fn func(context.Context, *sql.Tx) error) error
	Read(ctx context.Context, fn func(context.Context, *sql.DB) error) error
}

func NewStore(db execer) *Store { return &Store{db: db} }

// Observe records a ceiling the user just hit, keeping whichever is larger.
//
// Larger wins because running out early proves only that the window held at least that much; a
// later, bigger number is the better estimate of the same limit.
func (s *Store) Observe(ctx context.Context, ceiling Ceiling) error {
	if s == nil || s.db == nil || ceiling.Tokens <= 0 {
		return nil
	}

	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO ai_usage_ceiling (provider, plan, window_kind, observed_tokens, observed_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (provider, plan, window_kind) DO UPDATE SET
			    observed_tokens = MAX(observed_tokens, excluded.observed_tokens),
			    observed_at     = excluded.observed_at`,
			ceiling.Provider, ceiling.Plan, string(ceiling.Window),
			ceiling.Tokens, ceiling.SeenAt.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("recording the observed ceiling: %w", err)
		}
		return nil
	})
}

// Lookup answers the ceiling learned for one provider, plan and window, if there is one.
func (s *Store) Lookup(ctx context.Context, provider, plan string, window WindowKind) (int64, bool) {
	if s == nil || s.db == nil {
		return 0, false
	}

	var tokens int64
	found := false

	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		row := db.QueryRowContext(ctx, `
			SELECT observed_tokens FROM ai_usage_ceiling
			WHERE provider = ? AND plan = ? AND window_kind = ?`,
			provider, plan, string(window))

		switch err := row.Scan(&tokens); {
		case errors.Is(err, sql.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("reading the observed ceiling: %w", err)
		default:
			found = true
			return nil
		}
	})
	// A ceiling that cannot be read is a percentage that is not shown, which is the same as not
	// having one yet. It is never an error the user is interrupted with.
	if err != nil {
		return 0, false
	}

	return tokens, found
}

// percentOf answers how much of a ceiling a window has consumed, or nil when there is no ceiling to
// divide by. A pointer because "not calibrated yet" and "zero percent" are different answers and
// the renderer draws them differently.
func percentOf(tokens int64, ceiling int64, calibrated bool) *float64 {
	if !calibrated || ceiling <= 0 {
		return nil
	}

	percent := float64(tokens) / float64(ceiling) * 100
	if percent > 100 {
		// Past a ceiling learned earlier: the limit moved, and the panel says "at the limit" rather
		// than an impossible number. The next observation will raise the ceiling.
		percent = 100
	}
	return &percent
}
