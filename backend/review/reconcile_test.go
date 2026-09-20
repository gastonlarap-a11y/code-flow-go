package review_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/review"
)

// The port of ReviewMemoryReconcileTests. Both mistakes this code can make are silent: matching too
// eagerly re-opens a finding somebody already answered, matching too little posts the same comment
// again on every run.

// finding builds a parsed finding — the shape reconciliation receives from the current run.
func finding(id, file, categoria string) review.MemoryFinding {
	f := review.MemoryFinding{
		ID: id, Categoria: categoria, Severity: "warning", Tipo: "Bug",
		Subtitulo: "algo", Estado: "abierto",
	}
	if file != "" {
		f.Archivo = text(file)
	}
	return f
}

// stored builds a finding as a previous run left it.
func stored(id, file, categoria, estado string, iter int64) review.MemoryFinding {
	f := finding(id, file, categoria)
	f.Estado = estado
	f.IntroducidoEnIter = iter
	return f
}

func idsOf(findings []review.MemoryFinding) []string {
	ids := make([]string, 0, len(findings))
	for _, f := range findings {
		ids = append(ids, f.ID)
	}
	return ids
}

func byID(t *testing.T, findings []review.MemoryFinding, id string) review.MemoryFinding {
	t.Helper()
	for _, f := range findings {
		if f.ID == id {
			return f
		}
	}
	t.Fatalf("no finding %s in %v", id, idsOf(findings))
	return review.MemoryFinding{}
}

// ---- pass 1 -----------------------------------------------------------------------------------

// A finding the previous run did not have is new, and gets an id that cannot collide with one the
// model itself wrote this run.
func TestANewFindingGetsTheNextFreeID(t *testing.T) {
	prev := []review.MemoryFinding{stored("F-004", "src/a.ts", "race", "abierto", 1)}
	current := []review.MemoryFinding{finding("F-001", "src/b.ts", "naming")}

	merged, delta := review.Reconcile(prev, current, 2, nil, "completo")

	fresh := byID(t, merged, "F-005")
	assert.Equal(t, int64(3), fresh.IntroducidoEnIter, "the iteration it was first seen in")
	require.NotNil(t, fresh.Delta)
	assert.Equal(t, "nuevo", *fresh.Delta)
	assert.Equal(t, "abierto", fresh.Estado)
	assert.Equal(t, int64(1), delta.Nuevos)
	assert.Equal(t, int64(3), delta.IterActual)
	assert.Equal(t, int64(2), delta.IterPrevia)
}

// The correlative comes from the higher of the two sides, so a model that numbered its own findings
// past the stored ones cannot produce a duplicate.
func TestTheNextIDClearsBothSides(t *testing.T) {
	prev := []review.MemoryFinding{stored("F-002", "src/a.ts", "race", "abierto", 1)}
	current := []review.MemoryFinding{finding("F-009", "src/b.ts", "naming")}

	merged, _ := review.Reconcile(prev, current, 1, nil, "completo")

	assert.Contains(t, idsOf(merged), "F-010")
}

func TestAFindingThatIsStillThereKeepsItsIdentityAndItsThread(t *testing.T) {
	threadID := int64(555)
	previous := stored("F-002", "src/a.ts", "race", "posteado", 1)
	previous.ThreadID = &threadID

	merged, delta := review.Reconcile(
		[]review.MemoryFinding{previous},
		[]review.MemoryFinding{finding("F-001", "src/a.ts", "race")},
		2, nil, "completo")

	require.Len(t, merged, 1)
	kept := merged[0]
	assert.Equal(t, "F-002", kept.ID, "the stored id wins over whatever the model numbered it")
	assert.Equal(t, "posteado", kept.Estado)
	require.NotNil(t, kept.ThreadID)
	assert.Equal(t, int64(555), *kept.ThreadID, "so the reply lands in the thread it belongs to")
	assert.Equal(t, int64(1), kept.IntroducidoEnIter)
	require.NotNil(t, kept.Delta)
	assert.Equal(t, "persiste", *kept.Delta)
	assert.Equal(t, int64(1), delta.Persisten)
}

// The identity is the file and the category, so the same finding moved a few lines down is the same
// finding.
func TestAFindingIsMatchedByFileAndCategoryNotByLines(t *testing.T) {
	previous := stored("F-002", "/src/A.ts", "Race", "abierto", 1)
	current := finding("F-001", "src/a.ts", "race")
	current.Lineas = text("40-44")

	merged, delta := review.Reconcile([]review.MemoryFinding{previous}, []review.MemoryFinding{current}, 1, nil, "completo")

	require.Len(t, merged, 1)
	assert.Equal(t, "F-002", merged[0].ID, "case and a leading slash are normalised away")
	assert.Equal(t, int64(1), delta.Persisten)
}

// A row from before iterations were tracked gets a real one rather than keeping the sentinel.
func TestAPreTrackingRowGetsARealIteration(t *testing.T) {
	previous := stored("F-002", "src/a.ts", "race", "abierto", 0)

	merged, _ := review.Reconcile([]review.MemoryFinding{previous},
		[]review.MemoryFinding{finding("F-001", "src/a.ts", "race")}, 3, nil, "completo")

	require.Len(t, merged, 1)
	assert.Equal(t, int64(3), merged[0].IntroducidoEnIter)
}

// A finding somebody dismissed and that the model reported again persists, but is not counted: it
// does not persist for the person who dismissed it.
func TestADismissedFindingPersistsWithoutBeingCounted(t *testing.T) {
	for _, estado := range []string{"falso_positivo", "ignorado"} {
		t.Run(estado, func(t *testing.T) {
			previous := stored("F-002", "src/a.ts", "race", estado, 1)
			previous.MotivoDescarte = text("es intencional")

			merged, delta := review.Reconcile([]review.MemoryFinding{previous},
				[]review.MemoryFinding{finding("F-001", "src/a.ts", "race")}, 1, nil, "completo")

			require.Len(t, merged, 1)
			assert.Equal(t, estado, merged[0].Estado, "the dismissal stands")
			require.NotNil(t, merged[0].MotivoDescarte)
			assert.Equal(t, "es intencional", *merged[0].MotivoDescarte)
			assert.Zero(t, delta.Persisten)
		})
	}
}

// A resolved finding that comes back is a new finding: a fresh id, a fresh thread, and the old
// record kept beside it.
func TestAResolvedFindingThatComesBackIsBrandNew(t *testing.T) {
	oldThread := int64(99)
	previous := stored("F-002", "src/a.ts", "race", "resuelto", 1)
	previous.ThreadID = &oldThread
	previous.ResueltoEnIter = ptr(int64(2))

	merged, delta := review.Reconcile([]review.MemoryFinding{previous},
		[]review.MemoryFinding{finding("F-001", "src/a.ts", "race")}, 2, nil, "completo")

	require.Len(t, merged, 2, "the reappeared finding and the old record")
	fresh := byID(t, merged, "F-003")
	assert.Equal(t, "abierto", fresh.Estado)
	assert.Nil(t, fresh.ThreadID, "a new conversation, not a reopened one")
	assert.Equal(t, int64(3), fresh.IntroducidoEnIter)
	assert.Equal(t, int64(1), delta.Nuevos)

	history := byID(t, merged, "F-002")
	assert.Equal(t, "resuelto", history.Estado, "the old record keeps its traceability")
}

// ---- pass 2 -----------------------------------------------------------------------------------

func TestAFindingTheModelStoppedReportingIsResolved(t *testing.T) {
	previous := stored("F-002", "src/a.ts", "race", "posteado", 1)

	merged, delta := review.Reconcile([]review.MemoryFinding{previous}, nil, 2, nil, "completo")

	require.Len(t, merged, 1)
	assert.Equal(t, "resuelto", merged[0].Estado)
	require.NotNil(t, merged[0].ResueltoEnIter)
	assert.Equal(t, int64(3), *merged[0].ResueltoEnIter)
	require.NotNil(t, merged[0].Delta)
	assert.Equal(t, "resuelto", *merged[0].Delta)
	assert.Equal(t, int64(1), delta.Resueltos)
}

// Silence about a file this run never diffed is not evidence of a fix.
func TestAFindingInAFileThisRunDidNotDiffStaysOpen(t *testing.T) {
	previous := stored("F-002", "src/untouched.ts", "race", "abierto", 1)

	merged, delta := review.Reconcile([]review.MemoryFinding{previous}, nil, 1,
		[]string{"src/other.ts"}, "completo")

	require.Len(t, merged, 1)
	assert.Equal(t, "abierto", merged[0].Estado)
	assert.Equal(t, int64(1), delta.Persisten)
	assert.Zero(t, delta.Resueltos)
}

// A full review diffs everything, so silence there does mean fixed.
func TestAFullReviewResolvesWhateverItNoLongerReports(t *testing.T) {
	previous := stored("F-002", "src/untouched.ts", "race", "abierto", 1)

	merged, delta := review.Reconcile([]review.MemoryFinding{previous}, nil, 1, nil, "completo")

	require.Len(t, merged, 1)
	assert.Equal(t, "resuelto", merged[0].Estado)
	assert.Equal(t, int64(1), delta.Resueltos)
}

// A finding with no location cannot be tied to a file that might not have changed, so it is
// treated as examined.
func TestAFindingWithNoFileIsAlwaysTreatedAsExamined(t *testing.T) {
	previous := stored("F-002", "", "race", "abierto", 1)

	merged, _ := review.Reconcile([]review.MemoryFinding{previous}, nil, 1, []string{"src/other.ts"}, "completo")

	require.Len(t, merged, 1)
	assert.Equal(t, "resuelto", merged[0].Estado)
}

func TestTheChangedFileMatchIsSuffixTolerant(t *testing.T) {
	tests := []struct {
		name    string
		finding string
		changed string
		match   bool
	}{
		{name: "identical", finding: "src/app.ts", changed: "src/app.ts", match: true},
		{name: "a leading slash on one side", finding: "/src/app.ts", changed: "src/app.ts", match: true},
		{name: "git's path is longer", finding: "app.ts", changed: "backend/src/app.ts", match: true},
		{name: "the review's path is longer", finding: "backend/src/app.ts", changed: "src/app.ts", match: true},
		{name: "different case", finding: "SRC/App.ts", changed: "src/app.ts", match: true},
		{name: "a different file", finding: "src/app.ts", changed: "src/other.ts", match: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.match, review.FileInChanged(test.finding, []string{test.changed}))
		})
	}
}

// Resolved and discarded findings are carried forward untouched: nothing re-evaluates what somebody
// already answered.
func TestClosedFindingsAreCarriedForwardVerbatim(t *testing.T) {
	prev := []review.MemoryFinding{
		stored("F-001", "src/a.ts", "race", "resuelto", 1),
		stored("F-002", "src/b.ts", "naming", "falso_positivo", 1),
	}

	merged, delta := review.Reconcile(prev, nil, 2, nil, "completo")

	require.Len(t, merged, 2)
	assert.Equal(t, "resuelto", byID(t, merged, "F-001").Estado)
	assert.Equal(t, "falso_positivo", byID(t, merged, "F-002").Estado)
	assert.Zero(t, delta.Resueltos, "neither was resolved by this run")
	assert.Zero(t, delta.Persisten)
}

// ---- the level a finding was found at (DIVERGENCE-REVIEW-b) -----------------------------------

// A shallower run did not look for what a deeper one found, so it cannot claim it is fixed.
func TestAShallowerRunMarksWhatItCouldNotHaveSeenOutOfScope(t *testing.T) {
	previous := stored("F-002", "src/a.ts", "race", "abierto", 1)
	previous.Nivel = text("ultra")

	merged, delta := review.Reconcile([]review.MemoryFinding{previous}, nil, 1, nil, "basico")

	require.Len(t, merged, 1)
	assert.Equal(t, "fuera_de_alcance", merged[0].Estado)
	assert.Equal(t, int64(1), delta.FueraDeAlcance)
	assert.Zero(t, delta.Resueltos, "counted apart: 'not examined' is not 'fixed'")
}

func TestASameOrDeeperRunResolvesNormally(t *testing.T) {
	for _, level := range []string{"completo", "ultra"} {
		t.Run(level, func(t *testing.T) {
			previous := stored("F-002", "src/a.ts", "race", "abierto", 1)
			previous.Nivel = text("completo")

			merged, delta := review.Reconcile([]review.MemoryFinding{previous}, nil, 1, nil, level)

			require.Len(t, merged, 1)
			assert.Equal(t, "resuelto", merged[0].Estado)
			assert.Equal(t, int64(1), delta.Resueltos)
		})
	}
}

// A row written before the level was recorded behaves exactly as it always did — the one thing a
// new field must not change.
func TestAFindingWithNoRecordedLevelBehavesAsBefore(t *testing.T) {
	previous := stored("F-002", "src/a.ts", "race", "abierto", 1)
	require.Nil(t, previous.Nivel)

	merged, delta := review.Reconcile([]review.MemoryFinding{previous}, nil, 1, nil, "basico")

	require.Len(t, merged, 1)
	assert.Equal(t, "resuelto", merged[0].Estado)
	assert.Zero(t, delta.FueraDeAlcance)
}

// The level travels with every finding this run saw, so the next run can make the same judgement.
func TestThisRunsLevelIsRecordedOnWhatItSaw(t *testing.T) {
	merged, _ := review.Reconcile(nil, []review.MemoryFinding{finding("F-001", "src/a.ts", "race")}, 1, nil, "ultra")

	require.Len(t, merged, 1)
	require.NotNil(t, merged[0].Nivel)
	assert.Equal(t, "ultra", *merged[0].Nivel)
}

// ---- the whole machine ------------------------------------------------------------------------

// The delta the banner reports, over one run that does all four things at once.
func TestOneRunThatAddsKeepsResolvesAndDiscards(t *testing.T) {
	prev := []review.MemoryFinding{
		stored("F-001", "src/a.ts", "race", "posteado", 1),       // still reported → persists
		stored("F-002", "src/b.ts", "naming", "abierto", 1),      // not reported → resolved
		stored("F-003", "src/c.ts", "typo", "falso_positivo", 1), // dismissed → carried
	}
	current := []review.MemoryFinding{
		finding("F-001", "src/a.ts", "race"),
		finding("F-002", "src/d.ts", "secret"), // never seen before → new
	}

	merged, delta := review.Reconcile(prev, current, 4, nil, "completo")

	assert.Equal(t, review.ReviewDelta{
		IterPrevia: 4, IterActual: 5, Nuevos: 1, Persisten: 1, Resueltos: 1,
	}, delta)
	require.Len(t, merged, 4, "nothing is ever dropped")
	assert.Equal(t, "resuelto", byID(t, merged, "F-002").Estado)
	assert.Equal(t, "falso_positivo", byID(t, merged, "F-003").Estado)
	assert.Equal(t, "abierto", byID(t, merged, "F-004").Estado, "the new one")
}

// The findings the model restated come first, which the section that names the silent ones must not
// depend on — but the order itself is what a reader sees, so it is pinned.
func TestTheRestatedFindingsComeFirst(t *testing.T) {
	prev := []review.MemoryFinding{stored("F-001", "src/gone.ts", "race", "abierto", 1)}
	current := []review.MemoryFinding{finding("F-002", "src/a.ts", "naming")}

	merged, _ := review.Reconcile(prev, current, 1, nil, "completo")

	require.Len(t, merged, 2)
	assert.Equal(t, "src/a.ts", *merged[0].Archivo)
	assert.Equal(t, "src/gone.ts", *merged[1].Archivo)
}

func TestAFirstRunNeedsNoReconciliation(t *testing.T) {
	current := review.ParseFindings(strings.ReplaceAll(twoFindings, "🔁", "#"))

	merged, delta := review.Reconcile(nil, current, 0, nil, "completo")

	require.Len(t, merged, 2)
	assert.Equal(t, int64(2), delta.Nuevos)
	assert.Equal(t, int64(1), delta.IterActual)
	for _, f := range merged {
		assert.Equal(t, int64(1), f.IntroducidoEnIter)
	}
}

func ptr[T any](value T) *T { return &value }
