package git

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Reshaping a diff before a model reads it (GIT-031).
//
// The Changes tab wants whole-file context; a prompt wants the opposite. This is the single funnel
// for the three prompt paths — pre-commit analysis, pull-request review, pull-request description —
// and what it replaced was a flatten: whole unchanged files concatenated and then cut at a fixed
// character count with no marker, so the model received the first files entire and nothing from the
// rest, with no way to tell. Measured on this repository's own commit `f4d0792`: 460 217 characters
// flattened, 120 000 of which survived. The same commit renders as 68 269 here.
//
// Three properties make that work, and each is load-bearing:
//
//   - only the lines around a change travel, with what was dropped **declared** rather than dropped
//     silently;
//   - a file with no reviewable signal is excluded and **named**;
//   - the budget is shared between files, cheapest first, so one enormous file cannot starve
//     twenty small ones.

const (
	// PromptContextLines is how many unchanged lines either side of a change survive.
	PromptContextLines = 3

	// PromptBudgetChars is what a prompt diff gets by default.
	PromptBudgetChars = 250_000

	// minimumFileShare is the smallest slice of the budget worth spending on a file. Below it the
	// file is named in the note instead: half a file is a review that cites lines nobody can read.
	minimumFileShare = 2_000

	// noteReserve is what one named file costs in the `NOTE:` block, charged only against the files
	// that will actually be named.
	noteReserve = 120
)

// DiffCoverage is what a rendered prompt diff left in and left out — the stats line's raw material.
type DiffCoverage struct {
	// Touched is every file in the diff, whatever happened to it.
	Touched int
	// Shown is how many were rendered, whole or cut.
	Shown int
	// Truncated is how many of those were cut on a line boundary.
	Truncated int
	// Excluded is the paths dropped for having no reviewable signal, with the reason.
	Excluded []string
	// Omitted is the paths that had signal but no room.
	Omitted []string
	// Carried is the paths the caller said were reviewed already and unchanged since.
	Carried []string
}

// RenderForPrompt reshapes a parsed diff for a model (GIT-031).
func RenderForPrompt(files []FileDiff, budgetChars int) string {
	rendered, _ := ShapeForPrompt(files, budgetChars, nil)
	return rendered
}

// ShapeForPrompt is RenderForPrompt plus what it left out.
//
// `carried` are paths a previous review already covered and that have not changed since: they are
// named in the note rather than rendered, because sending them again spends budget on a review that
// has already happened.
func ShapeForPrompt(files []FileDiff, budgetChars int, carried []string) (string, DiffCoverage) {
	coverage := DiffCoverage{Touched: len(files), Carried: carried}
	if len(files) == 0 {
		return "", coverage
	}
	if budgetChars <= 0 {
		budgetChars = PromptBudgetChars
	}

	carriedSet := make(map[string]bool, len(carried))
	for _, path := range carried {
		carriedSet[path] = true
	}

	// Render each candidate once, at full length, so the budget can be shared by what each one
	// actually costs rather than by a guess.
	type candidate struct {
		path string
		body string
	}
	candidates := make([]candidate, 0, len(files))

	for _, file := range files {
		display := displayPath(file)
		if reason, skip := SkipReason(display); skip {
			coverage.Excluded = append(coverage.Excluded, fmt.Sprintf("%s (%s)", display, reason))
			continue
		}
		if carriedSet[display] {
			continue
		}
		candidates = append(candidates, candidate{path: display, body: renderFileForPrompt(file)})
	}

	// The note's own room is reserved against the files that will be named, which needs knowing
	// which those are — so the share is computed twice: once to find them, once for real.
	reserve := len(coverage.Excluded) * noteReserve
	if len(carried) > 0 {
		reserve += len(carried) * noteReserve
	}
	costs := make([]int, len(candidates))
	for i, c := range candidates {
		costs[i] = len(c.body)
	}
	shares := shareBudget(costs, budgetChars-reserve)
	for i, share := range shares {
		if costs[i] > share && share < minimumFileShare {
			// This file will be named rather than shown, so its line in the note needs room.
			reserve += noteReserve
		}
	}
	shares = shareBudget(costs, budgetChars-reserve)

	body := &strings.Builder{}
	for i, c := range candidates {
		share := shares[i]
		switch {
		case share < minimumFileShare && len(c.body) > share:
			coverage.Omitted = append(coverage.Omitted, c.path)
		case len(c.body) <= share:
			coverage.Shown++
			body.WriteString(c.body)
		default:
			coverage.Shown++
			coverage.Truncated++
			body.WriteString(cutOnALineBoundary(c.body, share))
		}
	}

	note := renderNote(coverage)
	return note + body.String(), coverage
}

// shareBudget divides a budget between costs, cheapest first, each taking what it needs and
// returning the rest.
//
// Water-filling rather than an equal split: an equal split gives a two-line file the same room as a
// two-thousand-line one, so the small files are all shown whole and the large one is cut — which is
// the outcome anyway, but reached by starving twenty files to half-show one.
func shareBudget(costs []int, budget int) []int {
	shares := make([]int, len(costs))
	if len(costs) == 0 {
		return shares
	}
	if budget < 0 {
		budget = 0
	}

	order := make([]int, len(costs))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return costs[order[a]] < costs[order[b]] })

	remaining := budget
	left := len(costs)
	for _, index := range order {
		fair := remaining / left
		if costs[index] <= fair {
			shares[index] = costs[index]
			remaining -= costs[index]
		} else {
			shares[index] = fair
			remaining -= fair
		}
		left--
	}
	return shares
}

// renderNote is the `NOTE:` block. A complete diff carries none.
func renderNote(coverage DiffCoverage) string {
	if len(coverage.Excluded) == 0 && len(coverage.Omitted) == 0 && len(coverage.Carried) == 0 {
		return ""
	}

	note := &strings.Builder{}
	note.WriteString("NOTE: this diff is not complete.\n")
	for _, excluded := range coverage.Excluded {
		fmt.Fprintf(note, "- excluded, no reviewable signal: %s\n", excluded)
	}
	for _, omitted := range coverage.Omitted {
		fmt.Fprintf(note, "- omitted for space: %s\n", omitted)
	}
	for _, carried := range coverage.Carried {
		fmt.Fprintf(note, "- unchanged since the previous review, already reviewed: %s\n", carried)
	}
	note.WriteString("\n")
	return note.String()
}

// cutOnALineBoundary trims a file's rendering to fit, and says that it did.
func cutOnALineBoundary(body string, budget int) string {
	const marker = "~ cut for space, the rest of this file is not shown\n"
	if budget <= len(marker) {
		return marker
	}

	kept := body[:budget-len(marker)]
	if at := strings.LastIndex(kept, "\n"); at >= 0 {
		kept = kept[:at+1]
	}
	return kept + marker
}

// renderFileForPrompt renders one file: a banner, then the runs that survived trimming.
func renderFileForPrompt(file FileDiff) string {
	out := &strings.Builder{}
	fmt.Fprintf(out, "--- %s (%s)\n", displayPath(file), file.Status)

	// A binary file has no hunks at all, and its banner is still the signal that it changed.
	for _, hunk := range file.Hunks {
		writeTrimmedHunk(out, hunk)
	}
	out.WriteString("\n")
	return out.String()
}

// writeTrimmedHunk keeps three lines either side of each change and declares what it dropped.
func writeTrimmedHunk(out *strings.Builder, hunk DiffHunk) {
	keep := linesToKeep(hunk.Lines)

	index := 0
	for index < len(hunk.Lines) {
		if !keep[index] {
			start := index
			for index < len(hunk.Lines) && !keep[index] {
				index++
			}
			fmt.Fprintf(out, "~ %d unchanged lines omitted\n", index-start)
			continue
		}

		// Each surviving run carries its own anchor, built from its real line numbers: with
		// whole-file context the source hunk header describes the file rather than the change, and
		// a finding that cites a line needs a line that exists.
		runStart := index
		for index < len(hunk.Lines) && keep[index] {
			index++
		}
		run := hunk.Lines[runStart:index]
		fmt.Fprintf(out, "%s\n", runAnchor(run))
		for _, line := range run {
			fmt.Fprintf(out, "%s%s\n", line.Origin, line.Content)
		}
	}
}

// linesToKeep marks every changed line, the context around it, and any single-line gap left
// between two kept runs.
//
// The gap is absorbed **here**, before anything is written, which is what keeps the runs either
// side of it from being emitted as two: declaring a one-line gap costs about thirty characters plus
// a second `@@` anchor of twenty more, and a line of code is about forty. One line is cheaper
// shown; two are not.
func linesToKeep(lines []DiffLine) []bool {
	keep := make([]bool, len(lines))
	for i, line := range lines {
		if line.Origin == " " {
			continue
		}
		for offset := -PromptContextLines; offset <= PromptContextLines; offset++ {
			if at := i + offset; at >= 0 && at < len(lines) {
				keep[at] = true
			}
		}
	}

	for i := 1; i < len(lines)-1; i++ {
		if !keep[i] && keep[i-1] && keep[i+1] {
			keep[i] = true
		}
	}
	return keep
}

// runAnchor builds the `@@ -old +new @@` for one run from the lines it actually holds.
func runAnchor(run []DiffLine) string {
	oldStart, oldCount := int64(0), 0
	newStart, newCount := int64(0), 0

	for _, line := range run {
		if line.OldLineNo != nil {
			if oldStart == 0 {
				oldStart = *line.OldLineNo
			}
			oldCount++
		}
		if line.NewLineNo != nil {
			if newStart == 0 {
				newStart = *line.NewLineNo
			}
			newCount++
		}
	}
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart, oldCount, newStart, newCount)
}

// displayPath is the path a note or a banner names: the new one, or the old one for a deletion.
func displayPath(file FileDiff) string {
	if file.NewPath != nil && *file.NewPath != "" {
		return *file.NewPath
	}
	if file.OldPath != nil {
		return *file.OldPath
	}
	return ""
}

// generatedSuffixes are the file endings that mean "a tool wrote this".
var generatedSuffixes = []string{
	".min.js", ".min.css", ".map", ".g.cs", ".designer.cs", ".g.dart", ".freezed.dart",
	".pb.go", "_pb2.py",
}

// lockFiles are dependency lock files: a real change with nothing to review in it.
var lockFiles = []string{
	"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "go.sum", "cargo.lock",
	"composer.lock", "gemfile.lock", "poetry.lock", "packages.lock.json",
}

// SkipReason says whether a path has any reviewable signal, and why not when it does not
// (GIT-031).
//
// Deliberately **not** `dist` / `build` / `out`: git ignores those where they are generated, and
// where it does not they hold real source — this repository stages into `shell/build/` itself.
func SkipReason(filePath string) (string, bool) {
	lower := strings.ToLower(filePath)
	base := path.Base(lower)

	for _, lock := range lockFiles {
		if base == lock {
			return "lock file", true
		}
	}
	for _, suffix := range generatedSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return "generated", true
		}
	}
	if strings.Contains(base, ".generated.") {
		return "generated", true
	}
	for _, segment := range strings.Split(lower, "/") {
		if segment == "node_modules" || segment == "vendor" {
			return "vendored", true
		}
	}
	return "", false
}

// RenderTextForPrompt reshapes a diff that arrived as **text** — a provider's own, for a review
// reached by a pasted link, where there is no clone to diff.
//
// A shape the parser does not recognise is truncated and said, never passed through: an unfamiliar
// format has to degrade to less content, not to no limit. That route once had no budget at all and
// the provider's diff went into the prompt whole; this application found it reviewing its own change.
func RenderTextForPrompt(diffText string, budgetChars int) string {
	if strings.TrimSpace(diffText) == "" {
		return ""
	}
	if budgetChars <= 0 {
		budgetChars = PromptBudgetChars
	}

	files := parseUnifiedDiff(diffText)
	if len(files) == 0 {
		if len(diffText) <= budgetChars {
			return diffText
		}
		return cutOnALineBoundary(diffText, budgetChars)
	}
	return RenderForPrompt(files, budgetChars)
}
