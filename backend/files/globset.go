package files

import (
	"regexp"
	"strings"
)

// globSet is a compiled include or exclude filter.
//
// Translated to a regular expression here rather than handed to a glob library: the translation
// itself is what the specification pins, down to which wildcards cross a `/` and which do not, and
// a library's own dialect would differ in exactly the cases nobody checks by hand.
type globSet struct {
	patterns []*regexp.Regexp
}

// buildGlobs parses a comma-separated pattern list, or nil when there is nothing to filter by.
//
// A pattern without a `/` is rewritten to `**/{pattern}`, so `*.ts` means "any .ts file at any
// depth" — which is what a user typing it into the find box means. One containing a `/` is used
// as-is against the whole repo-relative path.
func buildGlobs(list string) *globSet {
	patterns := make([]*regexp.Regexp, 0, 4)

	for _, raw := range strings.Split(list, ",") {
		pattern := strings.TrimSpace(raw)
		if pattern == "" {
			continue
		}
		if !strings.Contains(pattern, "/") {
			pattern = "**/" + pattern
		}
		if compiled, err := compileGlob(pattern); err == nil {
			// A pattern that will not compile is dropped rather than raised: the find box filters
			// as the user types, and half a glob is a normal intermediate state, not a mistake to
			// interrupt them with.
			patterns = append(patterns, compiled)
		}
	}

	if len(patterns) == 0 {
		return nil
	}
	return &globSet{patterns: patterns}
}

// matches reports whether any pattern in the set matches the path.
func (g *globSet) matches(path string) bool {
	if g == nil {
		return false
	}
	for _, pattern := range g.patterns {
		if pattern.MatchString(path) {
			return true
		}
	}
	return false
}

// compileGlob translates one glob into an anchored regular expression.
//
// The three wildcards differ in what they may cross, and the difference is the whole point:
//
//	**/   any number of leading directories, including none — so `**/x.ts` matches `x.ts` too
//	**    any characters, separators included
//	*     any characters **except** a separator, so `src/*.ts` does not reach `src/deep/x.ts`
//	?     one character, separator excluded
func compileGlob(pattern string) (*regexp.Regexp, error) {
	var out strings.Builder
	out.WriteString("^")

	for i := 0; i < len(pattern); i++ {
		switch {
		case strings.HasPrefix(pattern[i:], "**/"):
			// Matches nothing at all as well as any run of directories, which is what makes
			// `**/*.ts` find a file at the repository root.
			out.WriteString("(?:.*/)?")
			i += 2

		case strings.HasPrefix(pattern[i:], "**"):
			out.WriteString(".*")
			i++

		case pattern[i] == '*':
			out.WriteString("[^/]*")

		case pattern[i] == '?':
			out.WriteString("[^/]")

		default:
			// Byte by byte, escaping only what is special to a regular expression. Writing the
			// raw byte keeps a multi-byte character intact — converting it to a rune one byte at
			// a time would turn every accented filename into mojibake.
			if strings.IndexByte(`\.+()|[]{}^$`, pattern[i]) >= 0 {
				out.WriteByte('\\')
			}
			out.WriteByte(pattern[i])
		}
	}

	out.WriteString("$")
	return regexp.Compile(out.String())
}

// passesFilters applies the two stages in their fixed order (FILE-010).
//
// Include first, then exclude — **independently**, and exclude always wins. There is no way for an
// include pattern to re-admit something exclude removed, because they are two stages rather than
// one merged predicate. A user narrowing to `*.ts` and then excluding `*.test.ts` depends on it.
func passesFilters(path string, include, exclude *globSet) bool {
	if include != nil && !include.matches(path) {
		return false
	}
	return !exclude.matches(path)
}
