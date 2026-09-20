package review_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/review"
)

// The port of ReviewMemoryRenderTests and ReviewMemoryRenumberTests. Every string here is
// `VERBATIM` Spanish: it is what a stored review reads like, and a run opened months later has to
// read the same.

func TestTheDeltaBannerReadsExactlyAsItAlwaysHas(t *testing.T) {
	banner := review.DeltaBanner(review.ReviewDelta{
		IterPrevia: 2, IterActual: 3, Nuevos: 1, Persisten: 4, Resueltos: 2,
	})

	assert.Equal(t, "🔁 Re-revisión (iter 2 → 3): 1 nuevos · 4 persisten · 2 resueltos\n\n", banner,
		"the blank line is part of it — the banner is prepended straight onto the body")
}

// The out-of-scope count only appears when there is one, so every run that has none renders byte
// for byte what 2.x rendered.
func TestTheBannerOnlyMentionsOutOfScopeWhenThereIsSome(t *testing.T) {
	without := review.DeltaBanner(review.ReviewDelta{IterPrevia: 1, IterActual: 2})
	assert.NotContains(t, without, "fuera de alcance")

	with := review.DeltaBanner(review.ReviewDelta{IterPrevia: 1, IterActual: 2, FueraDeAlcance: 2})
	assert.Equal(t, "🔁 Re-revisión (iter 1 → 2): 0 nuevos · 0 persisten · 0 resueltos · 2 fuera de alcance\n\n", with)
}

func TestAReviewWithNothingClosedHasNoHistorySection(t *testing.T) {
	history := review.ResolvedHistorySection([]review.MemoryFinding{
		stored("F-001", "src/a.ts", "race", "abierto", 1),
		stored("F-002", "src/b.ts", "naming", "posteado", 1),
	})

	assert.Empty(t, history, "a first review stays clean")
}

func TestTheHistorySectionNamesWhatWasResolvedAndWhatWasDiscarded(t *testing.T) {
	resolved := stored("F-001", "src/a.ts", "race-condition", "resuelto", 2)
	resolved.ResueltoEnIter = ptr(int64(5))
	discarded := stored("F-002", "src/b.ts", "naming", "falso_positivo", 1)
	discarded.MotivoDescarte = text("es intencional")
	ignored := stored("F-003", "", "typo", "ignorado", 1)

	history := review.ResolvedHistorySection([]review.MemoryFinding{resolved, discarded, ignored})

	assert.Equal(t, strings.Join([]string{
		"",
		"",
		"---",
		"",
		"### 🕘 Historial de hallazgos resueltos (trazabilidad)",
		"",
		"- `race-condition` · src/a.ts — introducido iter 2 · resuelto iter 5",
		"",
		"### 🗂️ Hallazgos descartados",
		"",
		"- `naming` · src/b.ts — falso positivo: es intencional",
		"- `typo` · — — ignorado",
		"",
	}, "\n"), history)
}

func TestAResolvedFindingWithNoRecordedIterationStillRenders(t *testing.T) {
	resolved := stored("F-001", "src/a.ts", "race", "resuelto", 2)
	require.Nil(t, resolved.ResueltoEnIter)

	history := review.ResolvedHistorySection([]review.MemoryFinding{resolved})

	assert.Contains(t, history, "introducido iter 2 · resuelto iter 0")
}

// The section that names the still-open findings the model did not restate. Without it the banner
// said "2 persisten" and nothing anywhere said which two.
func TestThePersistingSectionNamesWhatTheBodyDoesNot(t *testing.T) {
	restated := finding("F-001", "src/a.ts", "race")
	silent := stored("F-002", "src/b.ts", "naming", "posteado", 3)
	closed := stored("F-003", "src/c.ts", "typo", "resuelto", 1)

	section := review.PersistingSection(
		[]review.MemoryFinding{restated, silent, closed},
		[]review.MemoryFinding{restated},
	)

	assert.Equal(t, strings.Join([]string{
		"",
		"",
		"### 📌 Siguen abiertos de revisiones anteriores",
		"",
		"- `naming` · src/b.ts — F-002, introducido iter 3; sin cambios en ese archivo desde entonces",
		"",
	}, "\n"), section)
}

func TestThePersistingSectionIsEmptyWhenTheBodyCoversEverything(t *testing.T) {
	restated := finding("F-001", "src/a.ts", "race")

	section := review.PersistingSection([]review.MemoryFinding{restated}, []review.MemoryFinding{restated})

	assert.Empty(t, section, "a first review stays clean")
}

// Matched by identity, not by position: reconciliation happens to emit the restated ones first, and
// a section that relied on that would break silently the day it stopped.
func TestThePersistingSectionMatchesByIdentityNotByPosition(t *testing.T) {
	silent := stored("F-002", "src/b.ts", "naming", "abierto", 3)
	restated := finding("F-001", "src/a.ts", "race")

	section := review.PersistingSection(
		[]review.MemoryFinding{silent, restated}, // the restated one second, on purpose
		[]review.MemoryFinding{restated},
	)

	assert.Contains(t, section, "`naming`")
	assert.NotContains(t, section, "`race`")
}

// ---- renumbering (DIVERGENCE-REVIEW-a) --------------------------------------------------------

const twoHeaders = "### 🔴 [Blocker · Bug] race · F-001\n" +
	"El contador se lee sin lock\n" +
	"📍 Ubicación: src/a.ts:12\n" +
	"\n" +
	"### 🟡 [Menor · Code Smell] naming · F-002\n" +
	"El nombre no dice qué hace\n" +
	"📍 Ubicación: src/b.ts:4\n"

// The model numbers its findings from F-001 on every run; reconciliation assigns the ids that
// actually identify them across runs. Without this the body and the stored row disagree about which
// finding is which.
func TestTheHeadersAreRewrittenToTheReconciledIds(t *testing.T) {
	reconciled := []review.MemoryFinding{
		finding("F-007", "src/a.ts", "race"),
		finding("F-008", "src/b.ts", "naming"),
	}

	rewritten := review.RenumberHeaders(twoHeaders, reconciled)

	assert.Contains(t, rewritten, "### 🔴 [Blocker · Bug] race · F-007")
	assert.Contains(t, rewritten, "### 🟡 [Menor · Code Smell] naming · F-008")
	assert.Contains(t, rewritten, "El contador se lee sin lock", "only the id token is touched")
	assert.Contains(t, rewritten, "📍 Ubicación: src/b.ts:4")
}

// The pairing is checked rather than assumed: a wrong renumbering is worse than none, because it
// looks deliberate.
func TestTheTextIsLeftAloneWhenThePairingDoesNotHold(t *testing.T) {
	tests := map[string][]review.MemoryFinding{
		"fewer findings than headers": {finding("F-007", "src/a.ts", "race")},
		"a different order": {
			finding("F-007", "src/b.ts", "naming"),
			finding("F-008", "src/a.ts", "race"),
		},
		"a finding the text does not carry": {
			finding("F-007", "src/a.ts", "race"),
			finding("F-008", "src/other.ts", "secret"),
		},
		"nothing at all": {},
	}

	for name, reconciled := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, twoHeaders, review.RenumberHeaders(twoHeaders, reconciled))
		})
	}
}

func TestRenumberingTextWithNoFindingsIsANoOp(t *testing.T) {
	body := "El cambio se ve bien.\n"

	assert.Equal(t, body, review.RenumberHeaders(body, nil))
}

// Reconciliation and renumbering, in the order the pipeline runs them.
func TestAReReviewRendersItsBannerHistoryAndIds(t *testing.T) {
	prev := []review.MemoryFinding{
		stored("F-005", "src/a.ts", "race", "posteado", 2),
		stored("F-006", "src/gone.ts", "leak", "abierto", 2),
	}
	current := review.ParseFindings(twoHeaders)

	merged, delta := review.Reconcile(prev, current, 2, nil, "completo")
	body := review.DeltaBanner(delta) + review.RenumberHeaders(twoHeaders, merged[:len(current)]) +
		review.PersistingSection(merged, current) + review.ResolvedHistorySection(merged)

	assert.Contains(t, body, "🔁 Re-revisión (iter 2 → 3): 1 nuevos · 1 persisten · 1 resueltos")
	assert.Contains(t, body, "· F-005", "the persisting finding keeps its stored id")
	assert.Contains(t, body, "· F-007", "the new one gets the next free id")
	assert.Contains(t, body, "### 🕘 Historial de hallazgos resueltos (trazabilidad)")
	assert.Contains(t, body, "- `leak` · src/gone.ts — introducido iter 2 · resuelto iter 3")
}
