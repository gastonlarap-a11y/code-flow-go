package review

import (
	"fmt"
	"strings"
)

// What a re-review adds to the body it shows (REVIEW-030, REVIEW-040) and the renumbering that
// keeps the ids in the text agreeing with the ids in the row (`DIVERGENCE-REVIEW-a`).
//
// Every Spanish literal here is `VERBATIM`: it is what a stored review reads like, and a reader
// opening a run from months ago has to see the same words.

// DeltaBanner is the line prepended to a re-review's body (REVIEW-030).
//
// A blank line follows it, because it is prepended directly onto the review markdown.
//
// The out-of-scope segment is only appended when there is one, so a run with none renders byte for
// byte what every stored review already renders (`DIVERGENCE-REVIEW-b`).
func DeltaBanner(delta ReviewDelta) string {
	banner := fmt.Sprintf("🔁 Re-revisión (iter %d → %d): %d nuevos · %d persisten · %d resueltos",
		delta.IterPrevia, delta.IterActual, delta.Nuevos, delta.Persisten, delta.Resueltos)
	if delta.FueraDeAlcance > 0 {
		banner += fmt.Sprintf(" · %d fuera de alcance", delta.FueraDeAlcance)
	}
	return banner + "\n\n"
}

// ResolvedHistorySection is the cumulative traceability a pull request builds up (REVIEW-030).
//
// Empty when there is nothing resolved and nothing discarded, so a first review stays clean. It has
// no cap and no pruning by design: the point is that a finding, once found, is never lost — which
// does mean a long-lived pull request grows this section and the stored body without bound.
func ResolvedHistorySection(findings []MemoryFinding) string {
	resolved := make([]MemoryFinding, 0, len(findings))
	discarded := make([]MemoryFinding, 0, len(findings))
	for _, f := range findings {
		switch f.Estado {
		case StateResolved:
			resolved = append(resolved, f)
		case StateFalsePositive, StateIgnored:
			discarded = append(discarded, f)
		}
	}
	if len(resolved) == 0 && len(discarded) == 0 {
		return ""
	}

	section := &strings.Builder{}
	section.WriteString("\n\n---\n\n### 🕘 Historial de hallazgos resueltos (trazabilidad)\n\n")
	for _, f := range resolved {
		resolvedIter := int64(0)
		if f.ResueltoEnIter != nil {
			resolvedIter = *f.ResueltoEnIter
		}
		fmt.Fprintf(section, "- `%s` · %s — introducido iter %d · resuelto iter %d\n",
			f.Categoria, fileOrDash(f.Archivo), f.IntroducidoEnIter, resolvedIter)
	}

	if len(discarded) > 0 {
		section.WriteString("\n### 🗂️ Hallazgos descartados\n\n")
		for _, f := range discarded {
			reason := "falso positivo"
			if f.Estado == StateIgnored {
				reason = "ignorado"
			}
			if f.MotivoDescarte != nil && strings.TrimSpace(*f.MotivoDescarte) != "" {
				reason += ": " + *f.MotivoDescarte
			}
			fmt.Fprintf(section, "- `%s` · %s — %s\n", f.Categoria, fileOrDash(f.Archivo), reason)
		}
	}
	return section.String()
}

// PersistingSection names the findings that are still open and that this run never restated
// (REVIEW-040).
//
// It exists because the two correct behaviours around it left a hole: a re-review keeps a finding
// open when its file did not change, and stops sending that file at all — so the banner said
// "2 persisten" and nothing anywhere said which two.
//
// Empty when the body already covers every open finding, so a first review stays clean.
func PersistingSection(findings, restated []MemoryFinding) string {
	// Matched by identity rather than by position: `Reconcile` happens to emit the restated ones
	// first, and a section that depended on that would break silently the day it stopped.
	written := make(map[string]bool, len(restated))
	for _, f := range restated {
		written[identityOf(f)] = true
	}

	silent := make([]MemoryFinding, 0, len(findings))
	for _, f := range findings {
		if f.IsActive() && !written[identityOf(f)] {
			silent = append(silent, f)
		}
	}
	if len(silent) == 0 {
		return ""
	}

	section := &strings.Builder{}
	section.WriteString("\n\n### 📌 Siguen abiertos de revisiones anteriores\n\n")
	for _, f := range silent {
		fmt.Fprintf(section, "- `%s` · %s — %s, introducido iter %d; sin cambios en ese archivo desde entonces\n",
			f.Categoria, fileOrDash(f.Archivo), f.ID, f.IntroducidoEnIter)
	}
	return section.String()
}

// fileOrDash is what a finding with no location shows: an em dash, not an empty gap.
func fileOrDash(archivo *string) string {
	if archivo == nil || strings.TrimSpace(*archivo) == "" {
		return "—"
	}
	return *archivo
}

// RenumberHeaders rewrites the `F-NNN` in the markdown to the ids reconciliation assigned
// (`DIVERGENCE-REVIEW-a`).
//
// The model writes its own correlatives, one per run, starting from F-001 every time.
// Reconciliation assigns the ids that actually identify a finding across runs, and without this the
// body a person reads and the row the app stores disagree about which finding is which — the
// drift that made a finding's id useless for talking about it.
//
// The pairing is **checked, not assumed**: the headers and the reconciled findings have to line up
// one-to-one, in order and by identity. If they do not, the text is returned untouched — a wrong
// renumbering is worse than none, because it looks deliberate.
func RenumberHeaders(markdown string, findings []MemoryFinding) string {
	headers := headerPattern.FindAllStringSubmatchIndex(markdown, -1)
	if len(headers) == 0 || len(headers) != len(findings) {
		return markdown
	}

	parsed := ParseFindings(markdown)
	if len(parsed) != len(findings) {
		return markdown
	}
	for i := range parsed {
		if identityOf(parsed[i]) != identityOf(findings[i]) {
			return markdown
		}
	}

	rewritten := &strings.Builder{}
	previous := 0
	for i, header := range headers {
		// Group 5 is the `F-NNN` token itself, and only that token is replaced: the rest of the
		// header line — emoji, severity word, type, category — is the model's own text.
		start, stop := header[2*5], header[2*5+1]
		rewritten.WriteString(markdown[previous:start])
		rewritten.WriteString(findings[i].ID)
		previous = stop
	}
	rewritten.WriteString(markdown[previous:])
	return rewritten.String()
}
