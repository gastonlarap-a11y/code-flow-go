package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Making two answers comparable without making them equal.
//
// The oracle's whole value depends on the line drawn here. Normalise too little and every run is a
// wall of timestamp noise nobody reads; normalise too much and a real difference is erased by the
// thing that was supposed to find it. So the rule is narrow: only what **cannot** be equal is
// replaced — a value minted per run (an id, a clock reading) or a location chosen per run (each
// side's own base directory) — and the replacement keeps the shape, so a field that is a string on
// one side and null on the other still differs.

// volatileKeys are the field names whose values are minted per run rather than derived from input.
//
// Matched by name, at any depth. A name is on this list because two correct implementations must
// disagree about it, never because it is inconvenient.
//
// **Identity is deliberately absent.** `id` is not here, although a workspace's id is minted per
// run, because a *commit's* id is not — it is derived from the repository both sides read, and the
// two must agree on it to the character. Minted ids are handled by shape instead: every one this
// application mints is a uuid, and `uuidPattern` below replaces those wherever they appear, under
// any field name. That is strictly sharper than a name list, and it fails in the safe direction —
// an id that is minted but not uuid-shaped reports a difference to investigate rather than hiding
// one. The same argument keeps `date` and `timestamp` off the list: a commit's date is data.
var volatileKeys = map[string]bool{
	// Clocks read at insert time.
	"created_at": true, "updated_at": true, "started_at": true, "finished_at": true,
	"last_used_at": true,
	// Measured durations.
	"response_time_ms": true, "duration_ms": true, "elapsed_ms": true,
}

var (
	// uuidPattern is the shape every id in this application has.
	uuidPattern = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	// timePattern is ISO-8601 with or without a zone, which is how every stored timestamp is
	// written. Dates that are *data* — a release date in fixture text — would also be caught, and
	// the script deliberately contains none.
	timePattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?([+-]\d{2}:?\d{2}|Z)?`)
)

// normaliser rewrites one side's answers into the shared vocabulary.
type normaliser struct {
	// base is this side's own data directory, which differs by construction: the two cores must
	// not share one.
	base string
	// repo is the fixture repository. Both sides read the *same* one, so its path is identical
	// already — it is replaced anyway so a report can be read without a temp directory in every
	// line, and so the two reports stay diffable if that ever changes.
	repo string
	// home is the old core's redirected home, of which base is a subdirectory. Replaced after base
	// so the longer match wins.
	home string
}

func (n normaliser) normalise(raw json.RawMessage) (string, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	// Numbers as strings, so a large integer is not reshaped into float64 notation by the round
	// trip. A difference in numeric formatting between the two sides is a real difference, and
	// float64 would hide it.
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}

	rendered, err := json.MarshalIndent(n.walk(value, ""), "", "  ")
	if err != nil {
		return "", fmt.Errorf("re-encode: %w", err)
	}
	return string(rendered), nil
}

// walk rewrites a decoded value. `key` is the field name this value arrived under, which is what
// makes the volatile-key rule possible at any depth.
func (n normaliser) walk(value any, key string) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for name, inner := range typed {
			out[name] = n.walk(inner, name)
		}
		return out

	case []any:
		out := make([]any, 0, len(typed))
		for _, inner := range typed {
			// A list element inherits the field name its list arrived under: `assets` holding
			// objects keeps working, and a bare list of ids under `ids` is still recognised.
			out = append(out, n.walk(inner, key))
		}
		return out

	case string:
		if volatileKeys[key] {
			// The shape is kept — a string stays a string — so a side that answers null where the
			// other answers a value still differs.
			return "<" + strings.ToUpper(key) + ">"
		}
		return n.text(typed)

	case json.Number:
		if volatileKeys[key] {
			return json.Number("0")
		}
		return typed

	default:
		// Booleans and null pass through. A volatile key holding null stays null on purpose: "this
		// run had no finished_at" is a fact about behaviour, not about the clock.
		return value
	}
}

// text rewrites the run-specific locations out of a string, longest first.
func (n normaliser) text(s string) string {
	for _, replacement := range []struct{ from, to string }{
		{n.base, "<BASE>"},
		{n.home, "<HOME>"},
		{n.repo, "<REPO>"},
	} {
		if replacement.from != "" {
			s = strings.ReplaceAll(s, replacement.from, replacement.to)
		}
	}
	s = uuidPattern.ReplaceAllString(s, "<UUID>")
	return timePattern.ReplaceAllString(s, "<TIME>")
}

// difference renders the first lines on which two normalised answers disagree.
//
// A whole-file dump of a hundred-line result with one wrong field is a report nobody reads to the
// end, so this prints the disagreeing lines with a little context and says how many more there are.
func difference(old, new string) string {
	oldLines, newLines := strings.Split(old, "\n"), strings.Split(new, "\n")

	var report strings.Builder
	shown, total := 0, 0
	for i := 0; i < len(oldLines) || i < len(newLines); i++ {
		oldLine, newLine := at(oldLines, i), at(newLines, i)
		if oldLine == newLine {
			continue
		}
		total++
		if shown >= 12 {
			continue
		}
		shown++
		fmt.Fprintf(&report, "      line %d\n        2.7.1: %s\n        3.0.0: %s\n", i+1, oldLine, newLine)
	}
	if total > shown {
		fmt.Fprintf(&report, "      … and %d more differing lines\n", total-shown)
	}
	return report.String()
}

func at(lines []string, i int) string {
	if i >= len(lines) {
		return "(absent)"
	}
	return lines[i]
}
