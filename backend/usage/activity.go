package usage

import (
	"context"
	"database/sql"
	"time"
)

/*
What the user has been doing (USAGE-009).

Nothing new is recorded for this. Conversations already land in `activity_log` and every review or
analysis already lands in `job_history`, both with a timestamp — so the activity section is two
counts over tables that were already being written, and turning the panel on collects nothing it
was not collecting before.

Counted over the trailing week, matching the AI section beside it, so the two read against the same
stretch of time rather than inviting a comparison that does not hold.
*/

// MeasureActivity counts the week's work.
//
// A count that cannot be read answers zero rather than failing the snapshot: the panel's other
// sections are still worth showing, and an activity figure is the least consequential thing on it.
func MeasureActivity(ctx context.Context, db execer, now time.Time) ActivityUsage {
	if db == nil {
		return ActivityUsage{}
	}

	since := now.Add(-WeekLength).UTC().Format(time.RFC3339Nano)
	activity := ActivityUsage{}

	_ = db.Read(ctx, func(ctx context.Context, sqlDB *sql.DB) error {
		// Both timestamps are stored as text and compared as text, which is the storage layer's
		// established contract (STORE query semantics) — so a string comparison is the right one.
		_ = sqlDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM activity_log WHERE created_at >= ?`, since).Scan(&activity.Conversations)
		_ = sqlDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM job_history WHERE created_at >= ?`, since).Scan(&activity.Jobs)
		return nil
	})

	return activity
}
