package testvectors_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/testvectors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The catalog's own integrity — the Go port of FixtureCatalogTests.
//
// These five guard the fixtures themselves rather than any behaviour, and they earn their place
// because a fixture file is data nobody compiles: a stale copy, a missing seed or a typo in a unit
// name produces a test that silently exercises nothing and reports success. That is worse than no
// test at all, because it is counted as coverage.

func TestEveryFixtureFileParses(t *testing.T) {
	names, err := testvectors.Files()
	require.NoError(t, err)
	require.Len(t, names, 24, "24 fixture files ship with the specification")

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			fixtures, err := testvectors.Load(name)
			require.NoError(t, err)
			assert.NotEmpty(t, fixtures, "a fixture file with no fixtures is a file nobody reads")
		})
	}
}

// A fixture written against a later schema must fail loudly rather than be half-read: the fields
// this loader does not know about would simply be dropped.
func TestEveryFixtureDeclaresTheKnownSchema(t *testing.T) {
	all, err := testvectors.LoadAll()
	require.NoError(t, err)

	for _, fixture := range all {
		assert.Equal(t, testvectors.SchemaVersion, fixture.Schema,
			"%s (unit %s)", fixture.File, fixture.Unit)
	}
}

// `extractedFrom` names must be unique per sourceFile: two fixtures claiming the same case group
// means one of them is a stale copy, and there is no way to tell which from the outside.
func TestExtractedCaseGroupsAreUniquePerSourceFile(t *testing.T) {
	all, err := testvectors.LoadAll()
	require.NoError(t, err)

	type origin struct{ sourceFile, group string }
	seen := make(map[origin]string, 256)

	for _, fixture := range all {
		for _, group := range fixture.ExtractedFrom {
			key := origin{fixture.SourceFile, group}
			if previous, duplicate := seen[key]; duplicate {
				t.Errorf("case group %q of %s is claimed by both %s and %s — one is a stale copy",
					group, fixture.SourceFile, previous, fixture.File)
				continue
			}
			seen[key] = fixture.File
		}
	}
}

// A scenario that names a seed artefact must have it on disk: without the seed the test would
// construct an empty environment and assert against it, which passes for the wrong reason.
//
// Only the ones that *name* a seed are checked. Most scenarios build a temporary git repository
// from their `steps` instead, and demanding a SQL file from those would be demanding the wrong
// thing — the README's own schema section is misleading here, showing setup at the fixture level
// when nine of the ten scenarios carry it per case.
func TestEveryNamedSeedArtefactExists(t *testing.T) {
	all, err := testvectors.LoadAll()
	require.NoError(t, err)

	seeds := 0
	for _, fixture := range all {
		if fixture.Kind != testvectors.KindScenario {
			continue
		}
		for _, c := range fixture.Cases {
			path := c.SeedPath(fixture)
			if path == "" {
				continue
			}
			seeds++
			t.Run(fixture.File+"/"+c.ID, func(t *testing.T) {
				assert.FileExists(t, path)

				content, err := c.Seed(fixture)
				require.NoError(t, err)
				assert.NotEmpty(t, content, "an empty seed seeds nothing")
			})
		}
	}
	assert.Positive(t, seeds, "the catalog is expected to reference seed artefacts")
}

// The other direction: a seed file nobody references is either dead weight or evidence that a
// fixture lost the reference to it, and the second is the one worth catching.
func TestEverySeedArtefactIsReferenced(t *testing.T) {
	all, err := testvectors.LoadAll()
	require.NoError(t, err)

	referenced := make(map[string]bool, 4)
	for _, fixture := range all {
		for _, c := range fixture.Cases {
			if path := c.SeedPath(fixture); path != "" {
				referenced[filepath.Base(path)] = true
			}
		}
		if path := fixture.SeedPath(); path != "" {
			referenced[filepath.Base(path)] = true
		}
	}

	seeds, err := filepath.Glob(filepath.Join(testvectors.Dir(), "sql", "*.sql"))
	require.NoError(t, err)
	require.NotEmpty(t, seeds)

	for _, seed := range seeds {
		assert.True(t, referenced[filepath.Base(seed)],
			"%s is not referenced by any fixture", filepath.Base(seed))
	}
}

// A scenario has to say what it does: without steps there is nothing to implement against.
func TestEveryScenarioCaseHasStepsAndAnExpectation(t *testing.T) {
	all, err := testvectors.LoadAll()
	require.NoError(t, err)

	for _, fixture := range all {
		if fixture.Kind != testvectors.KindScenario {
			continue
		}
		for _, c := range fixture.Cases {
			t.Run(fixture.File+"/"+c.ID, func(t *testing.T) {
				// Steps fall back to the fixture's, the same way the seed does: one file declares
				// them once for every case rather than repeating them.
				steps := c.Steps
				if len(steps) == 0 {
					steps = fixture.Steps
				}
				assert.NotEmpty(t, steps, "a scenario case needs steps")
				assert.NotEmpty(t, c.Expected, "a scenario case needs an expectation")
			})
		}
	}
}

// Every case needs an id, and ids must be unique within their fixture: a subtest name collision
// hides one of the two, and a missing id makes a failure unattributable.
func TestEveryCaseHasAUniqueIDWithinItsFixture(t *testing.T) {
	all, err := testvectors.LoadAll()
	require.NoError(t, err)

	total := 0
	for _, fixture := range all {
		seen := make(map[string]bool, len(fixture.Cases))
		for _, c := range fixture.Cases {
			total++
			assert.NotEmpty(t, c.ID, "%s (unit %s) has a case with no id", fixture.File, fixture.Unit)
			assert.False(t, seen[c.ID], "%s (unit %s) has two cases called %q", fixture.File, fixture.Unit, c.ID)
			seen[c.ID] = true
		}
	}

	// Two different numbers, and the difference is worth stating because the README conflates
	// them: 130 `extractedFrom` entries are the *source test methods* these fixtures were lifted
	// from, and they expand to 170 individual data cases. The README's "133 extracted test cases"
	// counts the former, so anyone asserting 133 against the files finds neither number.
	//
	// Both are pinned so a fixture quietly losing cases shows up here rather than as a suite that
	// runs fewer assertions and still reports success.
	assert.Equal(t, 170, total, "individual data cases across all fixtures")

	all, err = testvectors.LoadAll()
	require.NoError(t, err)
	groups := 0
	for _, fixture := range all {
		groups += len(fixture.ExtractedFrom)
	}
	assert.Equal(t, 130, groups, "source test methods the fixtures were extracted from")
}

// ---- the loader itself ------------------------------------------------------------------------

func TestLoadUnitFindsAFixtureAndNamesTheAlternativesWhenItCannot(t *testing.T) {
	fixture, err := testvectors.LoadUnit("secret_scan.vectors.json", "scan_diff")
	require.NoError(t, err)
	assert.Equal(t, "scan_diff", fixture.Unit)
	assert.NotEmpty(t, fixture.Cases)
	assert.Equal(t, "secret_scan.vectors.json", fixture.File)

	// A renamed unit must not silently produce zero cases, which would read as "all passed".
	_, err = testvectors.LoadUnit("secret_scan.vectors.json", "renamed_away")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no unit \"renamed_away\"")
	assert.Contains(t, err.Error(), "scan_diff", "the message lists what is actually there")
}

func TestLoadNamesTheFileItCouldNotRead(t *testing.T) {
	_, err := testvectors.Load("does_not_exist.vectors.json")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does_not_exist.vectors.json")
}

// The loader accepts both shapes the README documents: one fixture object, or an array of them.
func TestBothFileShapesLoad(t *testing.T) {
	names, err := testvectors.Files()
	require.NoError(t, err)

	single, multiple := 0, 0
	for _, name := range names {
		data, err := os.ReadFile(testvectors.Dir() + "/" + name)
		require.NoError(t, err)
		fixtures, err := testvectors.Load(name)
		require.NoError(t, err)

		var probe any
		require.NoError(t, json.Unmarshal(data, &probe))
		if _, isArray := probe.([]any); isArray {
			multiple++
			assert.NotEmpty(t, fixtures)
			continue
		}
		single++
		assert.Len(t, fixtures, 1)
	}
	assert.Positive(t, single, "the catalog uses the single-object shape")
	assert.Positive(t, multiple, "and the array shape")
}

// Decode names the case when a fixture's shape has drifted from the struct it feeds — the
// difference between a two-minute fix and a search.
func TestDecodeNamesTheCaseOnAShapeMismatch(t *testing.T) {
	c := testvectors.Case{ID: "sigv4-get-vanilla", Input: json.RawMessage(`{"method":"GET"}`)}

	var wrong struct {
		Method int `json:"method"`
	}
	err := testvectors.Decode(c, c.Input, &wrong)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "case sigv4-get-vanilla")
}

func TestDecodeReadsACaseInput(t *testing.T) {
	fixture, err := testvectors.LoadUnit("secret_scan.vectors.json", "scan_diff")
	require.NoError(t, err)
	require.NotEmpty(t, fixture.Cases)

	var input struct {
		Files []struct {
			NewPath *string `json:"new_path"`
		} `json:"files"`
	}
	require.NoError(t, testvectors.Decode(fixture.Cases[0], fixture.Cases[0].Input, &input))
	assert.NotEmpty(t, input.Files)
}
