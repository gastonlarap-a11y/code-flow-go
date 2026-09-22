package usage

import (
	"context"
	"io/fs"
	"path/filepath"
)

/*
What the app's own data occupies (USAGE-008).

Everything CodeFlow keeps lives under one directory — the database, the logs, the cloned
repositories — so the answer is that tree's size. Reported as bytes rather than a percentage of the
volume: a disk's free space is the operating system's business and changes for reasons that have
nothing to do with this app, whereas "the app is holding 3.4 GB" is a fact about the app and the
only one that tells somebody whether to clean up.

The walk is bounded and it never fails the panel. A directory that cannot be read is skipped, and a
tree that is enormous stops being counted rather than stalling a poll behind it — the figure is
there to be glanced at, not audited.
*/

// diskEntryLimit bounds one sweep. Reached only by a repositories folder with an extraordinary
// number of files in it, where the exact byte count matters far less than answering at all.
const diskEntryLimit = 200_000

// MeasureDisk totals the size of a directory tree.
func MeasureDisk(ctx context.Context, root string) DataUsage {
	usage := DataUsage{Complete: true}
	entries := 0

	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable directory is skipped, not fatal
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			return nil
		}

		entries++
		if entries > diskEntryLimit {
			usage.Complete = false
			return fs.SkipAll
		}

		info, statErr := entry.Info()
		if statErr != nil {
			// A file that vanished mid-walk is ordinary in a tree the app is writing to.
			return nil //nolint:nilerr
		}
		usage.Bytes += info.Size()
		return nil
	})

	return usage
}
