package git

import (
	"strconv"
	"strings"
)

// The unified-diff parser.
//
// libgit2 handed 2.x a structured patch; `git diff` hands this port text. So the structure has to
// be rebuilt, and the shapes below are the ones the renderer's `types/domain.ts` already declares
// — this is a translation back into what the UI was always given, not a new model.
//
// Every diff in the application is produced with `-U1000000`, the Go equivalent of the C#
// `FullFile()` options. One consequence is worth stating: a hunk is normally the *whole file*, and
// the renderer relies on that to show a diff with full context rather than islands.

// DiffLine is one line of a hunk. Origin is " ", "+" or "-"; the line numbers are null on the side
// where the line does not exist, which is what lets the renderer align two gutters.
type DiffLine struct {
	Origin    string `json:"origin"`
	Content   string `json:"content"`
	OldLineNo *int64 `json:"old_lineno"`
	NewLineNo *int64 `json:"new_lineno"`
}

// DiffHunk is a `@@` block.
type DiffHunk struct {
	Header string     `json:"header"`
	Lines  []DiffLine `json:"lines"`
}

// FileDiff is one file's changes. Both paths are nullable because an addition has no old path and
// a deletion has no new one — the renderer branches on exactly that to label the header.
type FileDiff struct {
	OldPath *string    `json:"old_path"`
	NewPath *string    `json:"new_path"`
	Status  string     `json:"status"`
	Hunks   []DiffHunk `json:"hunks"`
}

// parseUnifiedDiff turns `git diff` output into the renderer's shape.
//
// Written against git's output rather than the unified-diff format in the abstract, because the
// parts that matter here are git's own: the `diff --git` header, `rename from`/`rename to`, the
// `Binary files … differ` line that replaces hunks entirely, and `/dev/null` standing in for the
// missing side of an addition or a deletion.
func parseUnifiedDiff(output string) []FileDiff {
	files := make([]FileDiff, 0, 8)

	var current *FileDiff
	var hunk *DiffHunk
	var oldLine, newLine int64

	flushHunk := func() {
		if current != nil && hunk != nil {
			current.Hunks = append(current.Hunks, *hunk)
			hunk = nil
		}
	}
	flushFile := func() {
		flushHunk()
		if current != nil {
			files = append(files, *current)
			current = nil
		}
	}

	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			current = &FileDiff{Status: "modified", Hunks: make([]DiffHunk, 0, 1)}
			// The header's own paths are a fallback: `---`/`+++` below are authoritative, but a
			// binary file or a pure mode change has no such lines at all.
			if oldPath, newPath, ok := pathsFromHeader(line); ok {
				current.OldPath, current.NewPath = strPtr(oldPath), strPtr(newPath)
			}

		case current == nil:
			// Anything before the first `diff --git` is not ours to interpret.
			continue

		case strings.HasPrefix(line, "new file mode"):
			current.Status = "added"
			current.OldPath = nil
		case strings.HasPrefix(line, "deleted file mode"):
			current.Status = "deleted"
			current.NewPath = nil
		case strings.HasPrefix(line, "rename from "):
			current.Status = "renamed"
			current.OldPath = strPtr(strings.TrimPrefix(line, "rename from "))
		case strings.HasPrefix(line, "rename to "):
			current.Status = "renamed"
			current.NewPath = strPtr(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "copy from "):
			current.Status = "copied"
			current.OldPath = strPtr(strings.TrimPrefix(line, "copy from "))
		case strings.HasPrefix(line, "copy to "):
			current.Status = "copied"
			current.NewPath = strPtr(strings.TrimPrefix(line, "copy to "))

		case strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch"):
			// A binary file has no hunks, and the renderer shows "binary file" instead of a
			// diff. Leaving Hunks as an empty array rather than nil is what keeps it from
			// crashing on the map it does first.
			flushHunk()
			current.Status = "binary"

		case strings.HasPrefix(line, "--- "):
			if path, ok := devNullOrPath(strings.TrimPrefix(line, "--- ")); ok {
				current.OldPath = strPtr(path)
			} else {
				current.OldPath = nil
			}
		case strings.HasPrefix(line, "+++ "):
			if path, ok := devNullOrPath(strings.TrimPrefix(line, "+++ ")); ok {
				current.NewPath = strPtr(path)
			} else {
				current.NewPath = nil
			}

		case strings.HasPrefix(line, "@@"):
			flushHunk()
			oldStart, newStart := parseHunkHeader(line)
			oldLine, newLine = oldStart, newStart
			hunk = &DiffHunk{Header: line, Lines: make([]DiffLine, 0, 32)}

		case hunk == nil:
			// Index lines, mode changes, and anything else between the header and the first hunk.
			continue

		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file" belongs to the previous line, not to a new one.
			continue

		case strings.HasPrefix(line, "+"):
			hunk.Lines = append(hunk.Lines, DiffLine{
				Origin: "+", Content: line[1:], NewLineNo: int64Ptr(newLine),
			})
			newLine++
		case strings.HasPrefix(line, "-"):
			hunk.Lines = append(hunk.Lines, DiffLine{
				Origin: "-", Content: line[1:], OldLineNo: int64Ptr(oldLine),
			})
			oldLine++
		case strings.HasPrefix(line, " "):
			hunk.Lines = append(hunk.Lines, DiffLine{
				Origin: " ", Content: line[1:],
				OldLineNo: int64Ptr(oldLine), NewLineNo: int64Ptr(newLine),
			})
			oldLine++
			newLine++
		case line == "":
			// A context line that is genuinely empty arrives as a single space, which the case
			// above catches. A truly empty line here is the trailing newline of the output.
			continue
		}
	}
	flushFile()

	return files
}

// pathsFromHeader reads `diff --git a/<old> b/<new>`.
//
// Deliberately naive about paths containing " b/": git quotes such a header, and the `---`/`+++`
// lines that follow are authoritative anyway. This only has to be right for the binary and
// mode-change cases, where those lines are absent and the paths are ordinary.
func pathsFromHeader(line string) (string, string, bool) {
	rest := strings.TrimPrefix(line, "diff --git ")
	cut := strings.Index(rest, " b/")
	if cut < 0 || !strings.HasPrefix(rest, "a/") {
		return "", "", false
	}
	return rest[len("a/"):cut], rest[cut+len(" b/"):], true
}

// devNullOrPath strips the a/ or b/ prefix, reporting false for /dev/null — which is how git says
// "this side does not exist".
func devNullOrPath(field string) (string, bool) {
	// A tab separates the path from a timestamp when one is present.
	if tab := strings.IndexByte(field, '\t'); tab >= 0 {
		field = field[:tab]
	}
	if field == "/dev/null" {
		return "", false
	}
	return strings.TrimPrefix(strings.TrimPrefix(field, "a/"), "b/"), true
}

// parseHunkHeader reads the starting line numbers out of `@@ -a,b +c,d @@`.
//
// A count of 1 may be omitted — `@@ -1 +1 @@` is legal — so the comma is optional, and a file with
// no lines on one side starts at 0.
func parseHunkHeader(header string) (int64, int64) {
	fields := strings.Fields(header)
	var oldStart, newStart int64 = 1, 1

	for _, field := range fields {
		switch {
		case strings.HasPrefix(field, "-"):
			oldStart = leadingNumber(field[1:])
		case strings.HasPrefix(field, "+") && field != "+":
			newStart = leadingNumber(field[1:])
		}
	}
	return oldStart, newStart
}

func leadingNumber(field string) int64 {
	if comma := strings.IndexByte(field, ','); comma >= 0 {
		field = field[:comma]
	}
	value, err := strconv.ParseInt(field, 10, 64)
	if err != nil {
		return 1
	}
	return value
}

func strPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func int64Ptr(value int64) *int64 { return &value }
