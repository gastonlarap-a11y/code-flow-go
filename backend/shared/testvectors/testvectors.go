// Package testvectors loads the 133 extracted test cases that live as data under
// docs/business-rules/test-vectors.
//
// They are the reason a rewrite of this size is tractable. AWS SigV4 against Amazon's published
// vectors, HTTP Digest against the RFC's, Socket.IO wire bytes, the secret-scanner rules, every AI
// engine's output interpretation: the parts where "mostly right" is indistinguishable from right
// until it reaches a user. Being data rather than code, they were consumed unchanged by the xUnit
// suite and are consumed unchanged here — which is what makes a passing Go test evidence about the
// *behaviour* rather than about the port's own idea of it.
//
// A vector that has been "tidied up" is worthless: the whole point is that its bytes were
// validated against a published specification. Nothing in this package rewrites one.
package testvectors

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Kind distinguishes a pure data-in/data-out vector from one that needs a seeded environment.
type Kind string

const (
	// KindVector is data in, data out: no filesystem, no network, no process, no keychain.
	KindVector Kind = "vector"
	// KindScenario is data plus a seed artefact — a temporary git repository, a seeded legacy
	// schema — that the test builds first.
	KindScenario Kind = "scenario"
)

// Fixture is one unit's cases.
//
// A file holds either a single fixture or an array of them. The array form exists because one
// implementation file often covers several distinct units, and forcing one unit per file would
// either scatter a module across files or push unrelated cases under one label.
type Fixture struct {
	Schema        string   `json:"$schema"`
	SourceFile    string   `json:"sourceFile"`
	SourceLines   string   `json:"sourceLines"`
	ExtractedFrom []string `json:"extractedFrom"`
	Kind          Kind     `json:"kind"`
	Unit          string   `json:"unit"`
	Setup         *Setup   `json:"setup"`
	Steps         []string `json:"steps"`
	Cases         []Case   `json:"cases"`

	// Expected is a scenario's whole-run expectation, distinct from a case's own.
	Expected json.RawMessage `json:"expected"`

	// File is the fixture's own filename, filled in by the loader. Not part of the JSON.
	File string `json:"-"`
}

// Setup names the seed artefact a scenario needs.
type Setup struct {
	SeedSQL string `json:"seedSql"`
}

// Case is one input/expected pair, or one scenario run.
//
// Input and Expected stay json.RawMessage: the shapes differ per unit, and decoding them here
// would mean this package knowing every feature's types — the exact coupling that keeps a shared
// helper from being shared. Each test decodes into its own structs with Decode below.
//
// Setup and Steps appear here rather than only on the fixture. The README's schema section shows
// them at the fixture level and one file (grpc) does put them there, but every other scenario
// carries them per case — which is the shape that actually makes sense, since one fixture's cases
// seed different databases. Both places are read; SeedSQL() resolves the precedence.
type Case struct {
	ID       string          `json:"id"`
	Input    json.RawMessage `json:"input"`
	Expected json.RawMessage `json:"expected"`
	Notes    string          `json:"notes"`
	Setup    *Setup          `json:"setup"`
	Steps    []string        `json:"steps"`
}

// SchemaVersion is the only schema this loader understands. A fixture written against a later one
// fails loudly rather than being half-read.
const SchemaVersion = "codeflow-fixture-v1"

// Dir is where the fixtures live, resolved from this file's own location so a test works whatever
// directory `go test` was run from.
func Dir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	// backend/shared/testvectors → repository root → docs/business-rules/test-vectors
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	return filepath.Clean(filepath.Join(root, "docs", "business-rules", "test-vectors"))
}

// Load reads one fixture file and returns every fixture in it.
//
// name is the bare filename, "secret_scan.vectors.json". Callers pass a literal, so a typo is a
// failing test with the path in the message rather than a silently empty case list — which would
// otherwise read as "all vectors passed".
func Load(name string) ([]Fixture, error) {
	path := filepath.Join(Dir(), name)
	data, err := os.ReadFile(path) //nolint:gosec // a checked-in fixture, named by a literal
	if err != nil {
		return nil, fmt.Errorf("read fixture %s: %w", name, err)
	}

	fixtures, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse fixture %s: %w", name, err)
	}
	for i := range fixtures {
		fixtures[i].File = name
	}
	return fixtures, nil
}

// LoadUnit returns the single fixture in a file that covers a named unit.
//
// The common case: a test knows which unit it is exercising and wants its cases. Failing when the
// unit is absent is deliberate — a renamed unit must not silently produce zero cases.
func LoadUnit(name, unit string) (Fixture, error) {
	fixtures, err := Load(name)
	if err != nil {
		return Fixture{}, err
	}
	for _, fixture := range fixtures {
		if fixture.Unit == unit {
			return fixture, nil
		}
	}

	units := make([]string, 0, len(fixtures))
	for _, fixture := range fixtures {
		units = append(units, fixture.Unit)
	}
	return Fixture{}, fmt.Errorf("fixture %s has no unit %q; it has %s",
		name, unit, strings.Join(units, ", "))
}

// parse accepts either shape: a single fixture object or an array of them.
func parse(data []byte) ([]Fixture, error) {
	trimmed := strings.TrimLeft(string(data), " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		var fixtures []Fixture
		if err := json.Unmarshal(data, &fixtures); err != nil {
			return nil, err
		}
		return fixtures, nil
	}

	var single Fixture
	if err := json.Unmarshal(data, &single); err != nil {
		return nil, err
	}
	return []Fixture{single}, nil
}

// Files lists every fixture file, sorted. Used by the catalog tests, which check all of them.
func Files() ([]string, error) {
	entries, err := os.ReadDir(Dir())
	if err != nil {
		return nil, fmt.Errorf("read the fixture directory: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".vectors.json") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

// LoadAll returns every fixture in every file.
func LoadAll() ([]Fixture, error) {
	names, err := Files()
	if err != nil {
		return nil, err
	}

	all := make([]Fixture, 0, len(names))
	for _, name := range names {
		fixtures, err := Load(name)
		if err != nil {
			return nil, err
		}
		all = append(all, fixtures...)
	}
	return all, nil
}

// Decode unmarshals a case's input or expected value into a caller-supplied type.
//
// A helper rather than leaving callers to call json.Unmarshal, so the error names the case: a
// fixture whose shape drifted from the struct it feeds produces "case sigv4-get-vanilla: …"
// instead of "cannot unmarshal string into field X", which is the difference between a two-minute
// fix and a search.
func Decode[T any](c Case, raw json.RawMessage, target *T) error {
	if len(raw) == 0 {
		return fmt.Errorf("case %s has no value to decode", c.ID)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("case %s: %w", c.ID, err)
	}
	return nil
}

// SeedPath resolves a fixture-level seed artefact to an absolute path, or "" when there is none.
func (f Fixture) SeedPath() string { return seedPath(f.Setup) }

// SeedPath resolves a case's own seed artefact, falling back to its fixture's.
//
// The case wins: a fixture whose cases seed different databases is the normal shape, and a
// fixture-level seed is the exception one file happens to use.
func (c Case) SeedPath(fixture Fixture) string {
	if path := seedPath(c.Setup); path != "" {
		return path
	}
	return fixture.SeedPath()
}

func seedPath(setup *Setup) string {
	if setup == nil || setup.SeedSQL == "" {
		return ""
	}
	return filepath.Join(Dir(), filepath.FromSlash(setup.SeedSQL))
}

// Seed reads a case's seed SQL, ready to execute against a fresh database.
func (c Case) Seed(fixture Fixture) (string, error) {
	path := c.SeedPath(fixture)
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // a checked-in seed named by the fixture
	if err != nil {
		return "", fmt.Errorf("read the seed for case %s: %w", c.ID, err)
	}
	return string(data), nil
}

// CaseIDs lists a fixture's case ids, for a subtest name or an assertion.
func (f Fixture) CaseIDs() []string {
	ids := make([]string, 0, len(f.Cases))
	for _, c := range f.Cases {
		ids = append(ids, c.ID)
	}
	return ids
}
