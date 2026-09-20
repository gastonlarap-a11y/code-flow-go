package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
)

// Reading the C# test inventory: 1 232 behaviours, 121 classes, 18 folders.
//
// `docs/verbatim/test-inventory.md` is the port's to-do list, generated from `tests/CodeFlow.Tests`
// at v2.7.1. Each entry is one xUnit method, and the method names are sentences — which is the only
// reason this audit is possible at all: a name like `A_prerelease_is_older_than_the_release_it_
// precedes` says what it pins, so it can be looked for on the Go side even after the test that
// pins it was rewritten.

// csharpTest is one entry of the inventory.
type csharpTest struct {
	folder string
	class  string
	name   string
	// theory marks a `[Theory]`: data-driven, usually fed by `docs/business-rules/test-vectors`.
	// Worth carrying because a theory that lost its vectors is a different kind of gap from a fact
	// that was never ported.
	theory bool
}

var (
	folderHeading = regexp.MustCompile(`^## (.+)$`)
	classHeading  = regexp.MustCompile(`^### (.+)$`)
	testEntry     = regexp.MustCompile("^- (\\[T\\] )?`([A-Za-z0-9_]+)`\\s*$")
)

func readInventory(path string) ([]csharpTest, error) {
	// gosec G304: the path is this tool's own `-inventory` flag, defaulting to a file in this
	// repository. A developer tool reading the file a developer named is the whole interface.
	file, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	var (
		tests  []csharpTest
		folder string
		class  string
	)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		if match := folderHeading.FindStringSubmatch(line); match != nil {
			folder, class = strings.TrimSpace(match[1]), ""
			continue
		}
		if match := classHeading.FindStringSubmatch(line); match != nil {
			class = strings.TrimSpace(match[1])
			continue
		}
		if match := testEntry.FindStringSubmatch(line); match != nil && class != "" {
			tests = append(tests, csharpTest{
				folder: folder,
				class:  class,
				name:   match[2],
				theory: match[1] != "",
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return tests, nil
}

// words reduces a name to the lowercase words it is made of, whichever convention wrote it.
//
// `A_higher_version_is_newer`, `TestAHigherVersionIsNewer` and the subtest string
// `"a higher version is newer"` all become the same slice. That equivalence is the whole matcher:
// the port kept the *sentences* while changing the casing convention, so comparing words compares
// what was meant rather than how it was spelled.
func words(name string) []string {
	var (
		out     []string
		current strings.Builder
	)
	flush := func() {
		if current.Len() > 0 {
			out = append(out, strings.ToLower(current.String()))
			current.Reset()
		}
	}

	runes := []rune(name)
	for i, r := range runes {
		switch {
		case r == '_' || r == ' ' || r == '-' || r == '.' || r == ',' || r == '\'':
			flush()
		case r >= 'A' && r <= 'Z':
			// A capital starts a word, except inside a run of them: `PRLink` is two words, not
			// four, and `HTTPSend` is `http` + `send`.
			previousIsLower := i > 0 && runes[i-1] >= 'a' && runes[i-1] <= 'z'
			nextIsLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
			if previousIsLower || nextIsLower {
				flush()
			}
			current.WriteRune(r)
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return out
}

// filler words carry no evidence about which behaviour a name describes.
//
// Deliberately short. Dropping too much makes two unrelated names look alike — and the words that
// *invert* a meaning, `no`, `not`, `never`, `without`, `rather`, are kept for exactly that reason:
// "a refusal is recorded" and "a refusal is never recorded" must not score as the same behaviour.
var filler = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true, "were": true,
	"be": true, "been": true, "it": true, "its": true, "of": true, "to": true, "for": true,
	"and": true, "or": true, "that": true, "this": true, "with": true, "by": true, "in": true,
	"on": true, "at": true, "as": true, "from": true, "into": true, "test": true, "tests": true,
	"when": true, "then": true, "does": true, "do": true, "did": true, "has": true, "have": true,
	"one": true, "still": true, "even": true,
}

// signature is the evidence-bearing words of a name, in no particular order.
func signature(name string) map[string]bool {
	out := map[string]bool{}
	for _, word := range words(name) {
		if !filler[word] {
			out[word] = true
		}
	}
	return out
}

// weights says how much evidence each word carries, by how rare it is across both suites.
//
// Counting shared words flatly does not work, and the first run of this tool is what showed it:
// these names are English sentences about one program, so almost every one of them contains `file`,
// `path`, `run` or `commit`. Two unrelated tests share four such words and score the same as two
// tests that both say `prerelease`. Rarity is the correction — a word that appears in six names out
// of three thousand is nearly proof, one that appears in four hundred is nearly noise — and it is
// the standard one, an inverse document frequency.
type weights map[string]float64

func newWeights(corpus []map[string]bool) weights {
	frequency := map[string]int{}
	for _, document := range corpus {
		for word := range document {
			frequency[word]++
		}
	}

	total := float64(len(corpus))
	out := make(weights, len(frequency))
	for word, count := range frequency {
		// +1 inside the log so a word present in every document weighs a little rather than
		// nothing: a sentence made entirely of common words is weak evidence, not no evidence.
		out[word] = math.Log(1 + total/float64(count))
	}
	return out
}

// weight of an unseen word is the weight of the rarest seen one: a Go test using a word no C# name
// ever used tells us nothing about a C# name that does not use it either.
func (w weights) of(word string) float64 {
	if value, seen := w[word]; seen {
		return value
	}
	return 0
}

// overlap is the share of a C# name's *evidence* that a Go name also carries.
//
// Asymmetric on purpose: the question is "is this behaviour pinned somewhere on the Go side", not
// "are these two names the same". A Go test that pins the C# behaviour and more scores 1.0, and a
// Go name that happens to mention `file` and `path` scores close to nothing.
func (w weights) overlap(want, have map[string]bool) float64 {
	var wanted, found float64
	for word := range want {
		weight := w.of(word)
		wanted += weight
		if have[word] {
			found += weight
		}
	}
	if wanted == 0 {
		return 0
	}
	return found / wanted
}
