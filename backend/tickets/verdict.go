package tickets

import (
	"regexp"
	"strconv"
	"strings"
)

// The acceptance-criteria verdict block (XLANG-016).
//
// Two parsers match on these headers, one per language, and the prompt is what makes the model emit
// them. This half and `renderer/src/lib/parseTicketVerdict.ts` read the same fixture in
// `testdata/crosslang-verdict.md`, because two copies of a contract are exactly the thing that
// drifts.
//
// Both are **tolerant**: a missing or malformed section loses the verdict, never the answer, and the
// review then renders as an ordinary analysis.

// The four answers a criterion can get, `VERBATIM`. `no verificable` is a first-class one, not a
// failure to parse.
const (
	VerdictMet          = "cumple"
	VerdictNotMet       = "no cumple"
	VerdictPartial      = "parcial"
	VerdictUnverifiable = "no verificable"
)

// The three answers the branch as a whole can get, `VERBATIM`.
const (
	CoverageComplete     = "completa"
	CoverageIncomplete   = "incompleta"
	CoverageUnverifiable = "no verificable"
)

// The two headers the model is told to emit, and the criterion header under the first.
//
// The accent is optional on both `##` headers in both parsers, the same allowance `parseAnalysis`
// makes for `Ubicacion`: a dropped accent is the cheapest thing to tolerate and the most expensive
// to lose an answer to.
var (
	criteriaHeading  = regexp.MustCompile(`(?im)^[ \t]*##[ \t]*VERIFICACI[OÓ]N DE CRITERIOS DE ACEPTACI[OÓ]N[ \t]*$`)
	coverageHeading  = regexp.MustCompile(`(?im)^[ \t]*##[ \t]*VEREDICTO DE COBERTURA[ \t]*$`)
	criterionHeader  = regexp.MustCompile(`(?im)^[ \t]*###[ \t]*(AC-\d+)[ \t]*:?[ \t]*([^\n]*)$`)
	confidenceMarker = regexp.MustCompile(`🎯\s*Confianza:\s*(\d{1,3})`)
)

// fieldLabels end a field's continuation. `VERBATIM`, and the same list the renderer holds.
var fieldLabels = []string{
	"Veredicto:", "Evidencia:", "Relevancia:",
	"Cobertura:", "Faltante:", "Fuera de alcance:", "Resumen:",
}

// CriterionVerdict is one criterion's answer, in the shape the row stores and the renderer reads.
type CriterionVerdict struct {
	// ID is `AC-1` … `AC-N` — the criterion's own, so the table and the ticket agree.
	ID        string `json:"id"`
	Criterion string `json:"criterion"`
	Verdict   string `json:"verdict"`
	// Evidence is `path:lines — why`, or the sentence saying there is none. Kept as written.
	Evidence   string `json:"evidence"`
	Confidence *int64 `json:"confidence"`
}

// Coverage is the block that judges the branch as a whole.
type Coverage struct {
	Coverage   string `json:"coverage"`
	Missing    string `json:"missing"`
	OutOfScope string `json:"out_of_scope"`
	Summary    string `json:"summary"`
	// Relevant is whether the ticket describes this change at all (WI-026). **Read before the
	// criteria, because it can invalidate them**: a branch can be linked to the wrong work item, and
	// a criterion generic enough matches almost any code.
	//
	// An absent answer reads as relevant, in both parsers and in the stored form — a review written
	// before the question existed keeps its meaning, and a model that skipped the line does not have
	// its verdict discarded. Only an explicit `no corresponde` disowns a ticket.
	Relevant bool `json:"relevant"`
	// Relevance is why, in one line. Empty when the model did not answer it.
	Relevance string `json:"relevance"`
}

// ParsedVerdict is both halves, or as much of them as the answer carried.
type ParsedVerdict struct {
	Criteria []CriterionVerdict
	Coverage *Coverage
}

// SplitReview cuts a ticket review into the slice the finding parser reads and the slice this one
// does.
//
// The two are disjoint by construction rather than by convention. `### AC-1:` could never match the
// finding header anyway — no emoji, no bracketed severity, no `F-NNN` — but the cut also keeps the
// criteria table out of the summary fallback, which is where an answer with no findings at all would
// otherwise dump it.
func SplitReview(raw string) (findings, verdict string, hasVerdict bool) {
	location := criteriaHeading.FindStringIndex(raw)
	if location == nil {
		return raw, "", false
	}
	return strings.TrimRight(raw[:location[0]], " \t\r\n"), raw[location[0]:], true
}

// ParseVerdict reads the criteria table and the coverage block out of a review, answering nil when
// the review carries neither — which is every review this feature did not produce, and also a ticket
// review the model answered without its closing sections.
func ParseVerdict(raw string) *ParsedVerdict {
	_, verdict, found := SplitReview(raw)
	if !found {
		return nil
	}

	criteriaText := verdict
	if location := coverageHeading.FindStringIndex(verdict); location != nil {
		criteriaText = verdict[:location[0]]
	}

	headers := criterionHeader.FindAllStringSubmatchIndex(criteriaText, -1)
	criteria := make([]CriterionVerdict, 0, len(headers))

	for i, header := range headers {
		start := header[1]
		end := len(criteriaText)
		if i+1 < len(headers) {
			end = headers[i+1][0]
		}
		block := criteriaText[start:end]

		criteria = append(criteria, CriterionVerdict{
			ID:         strings.ToUpper(criteriaText[header[2]:header[3]]),
			Criterion:  clean(criteriaText[header[4]:header[5]]),
			Verdict:    normaliseVerdict(field(block, "Veredicto:")),
			Evidence:   field(block, "Evidencia:"),
			Confidence: confidenceOf(block),
		})
	}

	parsed := &ParsedVerdict{Criteria: criteria}

	if location := coverageHeading.FindStringIndex(verdict); location != nil {
		block := verdict[location[0]:]
		relevance := field(block, "Relevancia:")
		parsed.Coverage = &Coverage{
			Coverage:   normaliseCoverage(field(block, "Cobertura:")),
			Missing:    field(block, "Faltante:"),
			OutOfScope: field(block, "Fuera de alcance:"),
			Summary:    field(block, "Resumen:"),
			Relevant:   !strings.HasPrefix(strings.ToLower(relevance), "no corresponde"),
			Relevance:  relevance,
		}
	}
	return parsed
}

// field reads one labelled value: the rest of its line plus any wrapped continuation, up to the next
// label, heading, confidence line or blank line.
func field(block, label string) string {
	collected := make([]string, 0, 4)
	inside := false

	for raw := range strings.SplitSeq(block, "\n") {
		line := strings.TrimLeft(strings.TrimSuffix(raw, "\r"), " \t")

		if !inside {
			if !strings.HasPrefix(strings.ToLower(line), strings.ToLower(label)) {
				continue
			}
			inside = true
			collected = append(collected, strings.TrimSpace(line[len(label):]))
			continue
		}

		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "🎯") || isLabel(line) {
			break
		}
		collected = append(collected, strings.TrimSpace(line))
	}

	kept := make([]string, 0, len(collected))
	for _, line := range collected {
		if line != "" {
			kept = append(kept, line)
		}
	}
	return clean(strings.Join(kept, " "))
}

func isLabel(line string) bool {
	lower := strings.ToLower(line)
	for _, label := range fieldLabels {
		if strings.HasPrefix(lower, strings.ToLower(label)) {
			return true
		}
	}
	return false
}

// clean strips the emphasis the model wraps values in despite being told not to.
func clean(value string) string {
	return strings.Trim(strings.TrimSpace(value), "*`_: \t\r\n")
}

func confidenceOf(block string) *int64 {
	match := confidenceMarker.FindStringSubmatch(block)
	if match == nil {
		return nil
	}
	value, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return nil
	}
	return &value
}

// normaliseVerdict maps what the model wrote onto the four literals, defaulting to `no verificable`.
//
// The default is the conservative one deliberately: the prompt's standing order is to prefer a false
// alarm to approving incomplete work, and a verdict this parser could not read is not evidence that
// a criterion was met. `parseTicketVerdict.ts` holds the same table.
func normaliseVerdict(raw string) string {
	value := strings.ToLower(clean(raw))
	switch {
	// The same three tests in the same order as the renderer's. They do not overlap — `no cumple`
	// does not begin with `cumple` — so the order is mirrored for reading rather than for behaviour.
	case strings.HasPrefix(value, VerdictMet):
		return VerdictMet
	case strings.HasPrefix(value, VerdictNotMet):
		return VerdictNotMet
	case strings.HasPrefix(value, VerdictPartial):
		return VerdictPartial
	default:
		return VerdictUnverifiable
	}
}

func normaliseCoverage(raw string) string {
	value := strings.ToLower(clean(raw))
	switch {
	case strings.HasPrefix(value, CoverageComplete):
		return CoverageComplete
	case strings.HasPrefix(value, CoverageIncomplete):
		return CoverageIncomplete
	default:
		return CoverageUnverifiable
	}
}
