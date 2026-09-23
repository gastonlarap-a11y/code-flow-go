package review

import (
	"strconv"
	"strings"
)

// Reconciliation (REVIEW-027, REVIEW-028): merging a fresh parse against the previous run.
//
// Run once per re-review, and only when the pull request already has a saved run — a first review
// skips all of this and every finding simply starts at iteration 1.
//
// What makes it delicate is that both mistakes are silent. Matching too eagerly re-opens a finding
// the reviewer already answered, under its old id; matching too little re-files the same finding as
// new every run, and a person watching the pull request sees the same comment posted again.

// ReviewDelta is what a re-review changed, and what the banner above the body reports.
type ReviewDelta struct {
	IterPrevia int64 `json:"iter_previa"`
	IterActual int64 `json:"iter_actual"`
	Nuevos     int64 `json:"nuevos"`
	Persisten  int64 `json:"persisten"`
	Resueltos  int64 `json:"resueltos"`
	// FueraDeAlcance counts findings this run could not have seen because it ran shallower than the
	// one that found them (`DIVERGENCE-REVIEW-b`). Counted apart from `Resueltos` precisely because
	// they are not the same claim: one says "fixed", the other says "not examined".
	FueraDeAlcance int64 `json:"fuera_de_alcance"`
}

// The three review depths, ordered. A run only resolves what it was deep enough to look for.
var reviewLevelRank = map[string]int{"basico": 1, "completo": 2, "ultra": 3}

// rankOf is 0 for a level nobody recorded — a row written before the level was stored, which then
// behaves exactly as it did before: unranked, so never treated as out of scope.
func rankOf(level *string) int {
	if level == nil {
		return 0
	}
	return reviewLevelRank[strings.ToLower(strings.TrimSpace(*level))]
}

// Reconcile merges this run's findings with the previous run's (REVIEW-027, REVIEW-028).
//
// `changedFiles` is nil for a full review and the diffed files for an efficient re-review; it is
// what keeps a finding in a file this run never looked at from being reported as fixed.
func Reconcile(prev, current []MemoryFinding, prevIter int64, changedFiles []string, level string) ([]MemoryFinding, ReviewDelta) {
	iterActual := prevIter + 1
	delta := ReviewDelta{IterPrevia: prevIter, IterActual: iterActual}

	// The highest correlative seen on **either** side, so a fresh id can never collide with one the
	// model itself happened to emit this run.
	nextID := maxIDNumber(prev)
	if fromCurrent := maxIDNumber(current); fromCurrent > nextID {
		nextID = fromCurrent
	}
	nextID++

	merged := make([]MemoryFinding, 0, len(prev)+len(current))
	matchedPrev := make(map[string]bool, len(prev))
	levelOfThisRun := level

	// ---- pass 1: every finding the model wrote this run -----------------------------------------
	for _, cur := range current {
		key := identityOf(cur)
		previous, found := findByIdentity(prev, key)

		// A finding that was resolved and has come back is **not** the old one returning: it gets a
		// fresh id, a fresh iteration and no thread, so posting it opens a new conversation rather
		// than reviving one somebody already closed.
		if !found || previous.Estado == StateResolved {
			cur.ID = "F-" + pad3(nextID)
			nextID++
			cur.Estado = StateOpen
			cur.IntroducidoEnIter = iterActual
			cur.Delta = new("nuevo")
			cur.Nivel = &levelOfThisRun
			delta.Nuevos++
			merged = append(merged, cur)
			continue
		}

		cur.ID = previous.ID
		cur.Estado = previous.Estado
		cur.ThreadID = previous.ThreadID
		cur.MotivoDescarte = previous.MotivoDescarte
		cur.IntroducidoEnIter = previous.IntroducidoEnIter
		if cur.IntroducidoEnIter == 0 {
			// A row from before iterations were tracked. Anything is better than 0, which reads as
			// "not assigned" everywhere else.
			cur.IntroducidoEnIter = max(prevIter, 1)
		}
		cur.Delta = new("persiste")
		cur.Nivel = &levelOfThisRun

		matchedPrev[key] = true
		// A persisting finding the reviewer already dismissed is not something that persists for
		// them: only the active ones are counted.
		if cur.IsActive() {
			delta.Persisten++
		}
		merged = append(merged, cur)
	}

	// ---- pass 2: every previous finding this run did not restate --------------------------------
	for _, p := range prev {
		key := identityOf(p)
		if matchedPrev[key] || containsID(merged, p.ID) {
			continue
		}

		if !p.IsActive() {
			// Resolved or discarded: carried forward untouched. Traceability, and nothing is ever
			// re-evaluated once a person has answered it.
			p.Delta = new("persiste")
			merged = append(merged, p)
			continue
		}

		if !fileWasExamined(p, changedFiles) {
			// The file was not in this run's diff, so the model was never shown it. Silence is not
			// evidence of a fix.
			p.Delta = new("persiste")
			delta.Persisten++
			merged = append(merged, p)
			continue
		}

		if outOfScope(p, levelOfThisRun) {
			// The file was diffed, but this run was shallower than the one that found this — a
			// `basico` pass does not look for what `ultra` found. Reporting it fixed would be a
			// claim nobody made (`DIVERGENCE-REVIEW-b`).
			p.Estado = StateOutOfScope
			p.Delta = new("persiste")
			delta.FueraDeAlcance++
			merged = append(merged, p)
			continue
		}

		p.Estado = StateResolved
		p.ResueltoEnIter = &iterActual
		p.Delta = new("resuelto")
		p.Nivel = &levelOfThisRun
		delta.Resueltos++
		merged = append(merged, p)
	}

	return merged, delta
}

// fileWasExamined reports whether this run actually looked at the finding's file.
//
// A full review (no file list) and a finding with no recorded location both count as examined: the
// first saw everything, and the second cannot be tied to a file that might not have changed.
func fileWasExamined(f MemoryFinding, changedFiles []string) bool {
	if changedFiles == nil || f.Archivo == nil {
		return true
	}
	return FileInChanged(*f.Archivo, changedFiles)
}

// outOfScope reports whether this run was shallower than the one that found the finding.
//
// Unranked on either side means "do not decide": a row from before levels were recorded behaves as
// it always did, which is the one thing a new field must not change.
func outOfScope(f MemoryFinding, level string) bool {
	found := rankOf(f.Nivel)
	now := rankOf(&level)
	return found > 0 && now > 0 && now < found
}

// FileInChanged matches a finding's file against the files a run diffed (REVIEW-029).
//
// Suffix-tolerant in both directions, because the two sides come from different places: the model
// writes the path it read in the review markdown, git writes the repository-relative one, and they
// differ by a leading slash or by how fully qualified they are.
func FileInChanged(findingFile string, changed []string) bool {
	a := normalisePath(findingFile)
	if a == "" {
		return false
	}
	for _, candidate := range changed {
		c := normalisePath(candidate)
		if c == "" {
			continue
		}
		if c == a || strings.HasSuffix(c, a) || strings.HasSuffix(a, c) {
			return true
		}
	}
	return false
}

func normalisePath(path string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(path), "/"))
}

func findByIdentity(findings []MemoryFinding, key string) (MemoryFinding, bool) {
	for _, f := range findings {
		if identityOf(f) == key {
			return f, true
		}
	}
	return MemoryFinding{}, false
}

func containsID(findings []MemoryFinding, id string) bool {
	for _, f := range findings {
		if f.ID == id {
			return true
		}
	}
	return false
}

// maxIDNumber is the highest `F-NNN` correlative in a list, ignoring anything that is not one.
func maxIDNumber(findings []MemoryFinding) int64 {
	highest := int64(0)
	for _, f := range findings {
		digits, found := strings.CutPrefix(f.ID, "F-")
		if !found {
			continue
		}
		value, err := strconv.ParseInt(digits, 10, 64)
		if err == nil && value > highest {
			highest = value
		}
	}
	return highest
}

// pad3 renders an id's correlative the way the model writes it: three digits, more when it needs
// them.
func pad3(n int64) string {
	digits := strconv.FormatInt(n, 10)
	for len(digits) < 3 {
		digits = "0" + digits
	}
	return digits
}
