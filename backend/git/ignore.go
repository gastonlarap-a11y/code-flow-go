package git

import (
	"context"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

// CheckIgnore answers, for a batch of repo-relative paths, which ones git ignores.
//
// A batch rather than one call per path, and it is not a micro-optimisation: the file walk asks
// about every entry of every directory it visits, so one process per path would spawn thousands of
// them for a single palette open — on Windows, where process creation is expensive, that is the
// difference between instant and unusable.
//
// A probe ending in `/` is how the caller says "this one is a directory". Directory-only ignore
// rules (`build/`) match only a path git also sees as a directory, so dropping the slash makes the
// walk descend into exactly what the user excluded.
func CheckIgnore(ctx context.Context, repo string, probes []string) (map[string]bool, error) {
	ignored := make(map[string]bool, len(probes))
	if len(probes) == 0 {
		return ignored, nil
	}

	// `--stdin` with NUL separators on both sides: a filename may contain anything except NUL, and
	// a newline in one would otherwise be read as two paths.
	cmd := proc.Command(ctx, "git", "-C", repo, "check-ignore", "-z", "--stdin")
	cmd.Dir = repo
	cmd.Env = append(proc.Environment(nil), "LC_ALL=C")
	cmd.Stdin = strings.NewReader(strings.Join(probes, "\x00") + "\x00")

	out, err := cmd.Output()
	if err != nil {
		// Exit 1 means "none of them are ignored", which is data rather than a failure, and
		// Output() reports it as an ExitError with empty stdout. Anything worse — no git, not a
		// repository — also lands here, and answering "nothing is ignored" is the safe reading:
		// the walk then shows too much rather than hiding the user's files.
		return ignored, nil //nolint:nilerr // deliberate, see above
	}

	for _, path := range strings.Split(string(out), "\x00") {
		if path != "" {
			ignored[path] = true
		}
	}
	return ignored, nil
}
