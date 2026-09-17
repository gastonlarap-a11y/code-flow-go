package files

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Caps, all four of them silent when reached (FILE-008). Only the caller-supplied result ceiling
// sets a flag; the rest are absent results with no explanation, which is 2.x's behaviour.
const (
	// maxLineChars is how much of a matching line is shown. Matching runs against the whole line —
	// only the displayed text is cut, so a hit past column 400 is still a hit.
	maxLineChars = 400
	// maxHitsPerFile stops one generated file from filling the whole result list.
	maxHitsPerFile = 20
)

// SearchOptions is the find box's toggles.
//
// **camelCase on the wire**, unlike almost everything else here: it is an object the renderer
// builds and sends, and it has been these names since 2.x.
type SearchOptions struct {
	CaseSensitive bool   `json:"caseSensitive"`
	WholeWord     bool   `json:"wholeWord"`
	Regex         bool   `json:"regex"`
	Include       string `json:"include"`
	Exclude       string `json:"exclude"`
}

// SearchHit is one matching line.
type SearchHit struct {
	Path   string `json:"path"`
	LineNo int64  `json:"line_no"`
	Line   string `json:"line"`
}

// SearchOutcome is what one search produced.
type SearchOutcome struct {
	Hits []SearchHit `json:"hits"`
	// Truncated is true whenever the result count reached the ceiling, including when the last
	// possible match happened to be the one that filled it. The find box says "showing the first
	// N" on the strength of it, and claiming certainty there would be worse than over-reporting.
	Truncated bool `json:"truncated"`
}

// ReplaceOutcome is what one replace did, and how to undo it.
type ReplaceOutcome struct {
	Replacements int64 `json:"replacements"`
	Files        int64 `json:"files"`
	// CheckpointID is null when the snapshot could not be taken. The replace still happened —
	// there is simply nothing to restore, which is the one case the caller has to notice.
	CheckpointID *string `json:"checkpoint_id"`
}

// buildMatcher composes the pattern in the one order that is correct (FILE-009).
//
// Escape-or-regex, then whole-word, then case-insensitivity. The first step has to come first:
// wrapping a literal in `\b(?:…)\b` before escaping it would let a query containing `(` or `+`
// corrupt the pattern. The last two commute, and are still written in this order because that is
// the order the specification pins.
func buildMatcher(query string, options SearchOptions) (*regexp.Regexp, error) {
	body := query
	if !options.Regex {
		body = regexp.QuoteMeta(query)
	}
	if options.WholeWord {
		body = `\b(?:` + body + `)\b`
	}
	if !options.CaseSensitive {
		body = "(?i)" + body
	}

	compiled, err := regexp.Compile(body)
	if err != nil {
		// Reduced to the last line and trimmed: the compiler's message is multi-line and prints the
		// offending pattern with a caret, which reads as noise in a one-line find box.
		message := err.Error()
		if lines := strings.Split(message, "\n"); len(lines) > 0 {
			message = strings.TrimSpace(lines[len(lines)-1])
		}
		// VERBATIM.
		return nil, fmt.Errorf("invalid regular expression: %s", message)
	}
	return compiled, nil
}

// SearchRepo finds a query across the repository's text files.
func SearchRepo(ctx context.Context, repo, query string, options SearchOptions, maxResults int64) (SearchOutcome, error) {
	outcome := SearchOutcome{Hits: make([]SearchHit, 0, 64)}

	matcher, err := buildMatcher(query, options)
	if err != nil {
		return outcome, err
	}
	base, err := canonical(repo)
	if err != nil {
		return outcome, err
	}

	paths, err := walk(ctx, gitIgnoreChecker{}, repo)
	if err != nil {
		return outcome, err
	}

	include, exclude := buildGlobs(options.Include), buildGlobs(options.Exclude)

	for _, rel := range paths {
		if int64(len(outcome.Hits)) >= maxResults {
			outcome.Truncated = true
			return outcome, nil
		}
		if !passesFilters(rel, include, exclude) {
			continue
		}
		text, ok := readTextFile(filepath.Join(base, filepath.FromSlash(rel)))
		if !ok {
			continue
		}

		inFile := 0
		for index, line := range strings.Split(text, "\n") {
			if inFile >= maxHitsPerFile || int64(len(outcome.Hits)) >= maxResults {
				break
			}
			if !matcher.MatchString(line) {
				continue
			}
			inFile++
			outcome.Hits = append(outcome.Hits, SearchHit{
				Path:   rel,
				LineNo: int64(index + 1),
				Line:   truncateLine(line),
			})
		}
	}

	// Recomputed after a clean walk as well: a result count sitting exactly on the ceiling is
	// reported as truncated whether or not anything was actually left out.
	outcome.Truncated = int64(len(outcome.Hits)) >= maxResults
	return outcome, nil
}

// truncateLine cuts a displayed line to maxLineChars **characters**, not bytes.
//
// Bytes would split a multi-byte character in half and send invalid UTF-8 to the renderer, which
// fails the whole response rather than showing one odd line.
func truncateLine(line string) string {
	runes := []rune(strings.TrimSuffix(line, "\r"))
	if len(runes) <= maxLineChars {
		return string(runes)
	}
	return string(runes[:maxLineChars]) + "…"
}

// Checkpointer takes the snapshot a replace can be undone from.
//
// One method, declared at the consumer: the replace needs "protect this repository now" and knows
// nothing about refs, trees or how undo works.
type Checkpointer interface {
	CreateCheckpoint(ctx context.Context, repo, kind string) (string, error)
}

// replaceCheckpointKind is the stable action key the checkpoint is labelled with. The UI translates
// it; this string never changes.
const replaceCheckpointKind = "replace-all"

// ReplaceInRepo rewrites every match across the repository, or within one file (FILE-011).
//
// **Every edit is planned before anything is written.** A file that fails to read halfway through
// the walk therefore cannot leave the tree half-replaced — the alternative, writing as it goes,
// fails after the fourth of nine files and leaves the user to work out which four.
//
// The checkpoint is taken after planning and before the first write, so undo restores the state the
// user actually had. Its failure is swallowed: a replace with no undo is worse than a replace, but
// refusing to do what the user asked because the snapshot failed is worse still. They find out
// through a null checkpoint id.
func ReplaceInRepo(
	ctx context.Context,
	checkpointer Checkpointer,
	repo, query, replacement string,
	options SearchOptions,
	onlyPath *string,
) (ReplaceOutcome, error) {
	if strings.TrimSpace(query) == "" {
		// Not an error: an empty find box is the state it starts in.
		return ReplaceOutcome{}, nil
	}

	matcher, err := buildMatcher(query, options)
	if err != nil {
		return ReplaceOutcome{}, err
	}
	base, err := canonical(repo)
	if err != nil {
		return ReplaceOutcome{}, err
	}

	paths, err := walk(ctx, gitIgnoreChecker{}, repo)
	if err != nil {
		return ReplaceOutcome{}, err
	}

	include, exclude := buildGlobs(options.Include), buildGlobs(options.Exclude)

	type edit struct {
		rel     string
		text    string
		matches int64
	}
	planned := make([]edit, 0, 16)
	var replacements int64

	for _, rel := range paths {
		if onlyPath != nil && *onlyPath != "" && rel != *onlyPath {
			continue
		}
		if !passesFilters(rel, include, exclude) {
			continue
		}
		text, ok := readTextFile(filepath.Join(base, filepath.FromSlash(rel)))
		if !ok {
			continue
		}

		matches := int64(len(matcher.FindAllStringIndex(text, -1)))
		if matches == 0 {
			continue
		}

		// Expand is the regex-mode behaviour: `$1` in the replacement refers to a capture group.
		// In literal mode the pattern has no groups, so a `$1` in the replacement expands to
		// nothing — which is what 2.x did, and changing it would break anyone's saved replacement.
		replaced := matcher.ReplaceAllString(text, replacement)
		if replaced == text {
			continue
		}
		planned = append(planned, edit{rel: rel, text: replaced, matches: matches})
		replacements += matches
	}

	if len(planned) == 0 {
		return ReplaceOutcome{}, nil
	}

	outcome := ReplaceOutcome{Replacements: replacements, Files: int64(len(planned))}
	if checkpointer != nil {
		if id, err := checkpointer.CreateCheckpoint(ctx, repo, replaceCheckpointKind); err == nil {
			outcome.CheckpointID = &id
		}
	}

	for _, planned := range planned {
		full := filepath.Join(base, filepath.FromSlash(planned.rel))
		if err := writeUserFile(full, []byte(planned.text)); err != nil {
			// Aborts on the first failure and names the file. Nothing is rolled back here: the
			// checkpoint taken moments ago is exactly what rollback is for, and undoing by hand
			// would be a second, less reliable mechanism doing the same job.
			return outcome, fmt.Errorf("%s: %w", planned.rel, err)
		}
	}
	return outcome, nil
}
