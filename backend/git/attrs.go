package git

import (
	"context"
	"fmt"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

// CheckAttrs answers, for a batch of repo-relative paths, the paths for which git sets any of the
// named attributes — `.gitattributes` lines such as `dist/** linguist-generated`.
//
// One process for the whole batch, for the reason CheckIgnore gives. An attribute counts as set when
// git reports it `set` or any value other than `false`; `unset` (`-attr`) and `unspecified` do not.
func CheckAttrs(ctx context.Context, repo string, attrs, paths []string) (map[string]bool, error) {
	flagged := make(map[string]bool)
	if len(paths) == 0 || len(attrs) == 0 {
		return flagged, nil
	}

	// `-z --stdin` on both sides, for the reason CheckIgnore gives: a path may contain a newline.
	args := append([]string{"-C", repo, "-c", "core.quotepath=off", "check-attr", "-z", "--stdin"}, attrs...)
	cmd := proc.Command(ctx, "git", args...)
	cmd.Dir = repo
	cmd.Env = append(proc.Environment(nil), "LC_ALL=C")
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")

	// Unlike check-ignore, check-attr answers every path and exits 0; a failure means no git or no
	// repository, which the caller decides how to read.
	out, err := cmd.Output()
	if err != nil {
		return flagged, fmt.Errorf("git check-attr in %s: %w", repo, err)
	}

	// The answer is `path NUL attribute NUL value NUL`, once per path and attribute.
	fields := strings.Split(string(out), "\x00")
	for i := 0; i+2 < len(fields); i += 3 {
		path, value := fields[i], fields[i+2]
		switch value {
		case "unspecified", "unset", "false":
		default:
			flagged[path] = true
		}
	}
	return flagged, nil
}
