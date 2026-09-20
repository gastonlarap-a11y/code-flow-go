package review

import (
	"regexp"
	"strconv"
	"strings"
)

// Review memory: the parse and the reconciliation a re-review runs.
//
// This is the flagship feature's memory. Everything here is pure — no network, no database, no
// clock — because it is the part that has to be right: a review that reconciles wrongly either
// loses a finding a person is waiting on, or re-opens one they already answered.
//
// The header format is a three-way contract (`XLANG-001`): the prompt tells the model to write it,
// this parses it for reconciliation, and `frontend/src/lib/parseAnalysis.ts` parses it for display.
// The regexes below are character-identical to the renderer's, and the two change together.

// MemoryFinding is the slim, comparable projection of a finding that `review_runs.findings` stores.
//
// The field names are the stored ones and mostly Spanish (`VERBATIM`): they are what 2.x wrote into
// every existing row and what the renderer reads back. `severity` being the one English name among
// them is 2.x's own inconsistency, preserved for the same reason.
type MemoryFinding struct {
	ID        string `json:"id"`
	Severity  string `json:"severity"`
	Tipo      string `json:"tipo"`
	Categoria string `json:"categoria"`
	Subtitulo string `json:"subtitulo"`

	Archivo   *string `json:"archivo"`
	Lineas    *string `json:"lineas"`
	Confianza *int64  `json:"confianza"`

	Estado   string `json:"estado"`
	ThreadID *int64 `json:"thread_id"`

	IntroducidoEnIter int64   `json:"introducido_en_iter"`
	ResueltoEnIter    *int64  `json:"resuelto_en_iter"`
	MotivoDescarte    *string `json:"motivo_descarte"`
	Delta             *string `json:"delta"`

	// Nivel is the review depth that last saw this finding (`DIVERGENCE-REVIEW-b`). Absent on every
	// row written before the field existed, which then behaves exactly as it did before.
	Nivel *string `json:"nivel,omitempty"`
}

// The `estado` a finding can be in. The first two are active; the rest are carried for traceability
// and never re-evaluated. `StateOutOfScope` is the port's own (`DIVERGENCE-REVIEW-b`).
const StateOutOfScope = "fuera_de_alcance"

// IsActive reports whether a finding counts toward the severity buckets and the Quality Gate.
//
// Nothing ever deletes a finding: a resolved or discarded one stays in the row so the pull request
// keeps its cumulative history, and this is what keeps it out of the current view.
func (f MemoryFinding) IsActive() bool {
	return f.Estado == StateOpen || f.Estado == StatePosted
}

// The five regexes of `XLANG-001`, character for character the renderer's.
var (
	// headerPattern finds every finding header line.
	headerPattern = regexp.MustCompile(`(?m)^###\s*(🔴|🚨|🟠|🟡|🔵|⚠️|ℹ️)\s*\[([^·\]]+)·([^\]]+)\]\s*([^·]+)·\s*(F-\d+)\s*$`)

	// locationPattern and confidencePattern are matched **within one finding's block**, never over
	// the whole document, so a field belonging to the next finding cannot bleed into this one.
	locationPattern   = regexp.MustCompile(`📍\s*Ubicaci[oó]n:\s*([^\n]+)`)
	confidencePattern = regexp.MustCompile(`🎯\s*Confianza:\s*(\d+)`)

	// afterwordPattern caps the last finding's block. Without it, a digit in a
	// `Lo que está bien` bullet is read as that finding's confidence.
	afterwordPattern = regexp.MustCompile(`(?im)^[ \t]*##[ \t]*(?:👍[ \t]*Lo que est[aá] bien|🗒️?[ \t]*Notas)[ \t]*$`)

	// markdownWrapping is the backticks, asterisks and underscores the model puts around a path.
	markdownWrapping = regexp.MustCompile("[`*_]+")
)

// ParseFindings reads a review's markdown into the rows that get stored (REVIEW-024, REVIEW-025).
//
// Blocks, not lines: each finding owns the text from its own header to the next one — or, for the
// last, to the first trailing section. Every field is extracted from inside that block.
func ParseFindings(markdown string) []MemoryFinding {
	headers := headerPattern.FindAllStringSubmatchIndex(markdown, -1)
	findings := make([]MemoryFinding, 0, len(headers))
	if len(headers) == 0 {
		return findings
	}

	// Where the last block has to stop: the first trailing section, if there is one.
	end := len(markdown)
	if afterword := afterwordPattern.FindStringIndex(markdown[headers[len(headers)-1][1]:]); afterword != nil {
		end = headers[len(headers)-1][1] + afterword[0]
	}

	for i, header := range headers {
		blockEnd := end
		if i+1 < len(headers) {
			blockEnd = headers[i+1][0]
		}
		block := markdown[header[1]:blockEnd]

		emoji := group(markdown, header, 1)
		severityWord := strings.TrimSpace(group(markdown, header, 2))

		finding := MemoryFinding{
			ID:        strings.TrimSpace(group(markdown, header, 5)),
			Severity:  SeverityOf(severityWord, emoji),
			Tipo:      strings.TrimSpace(group(markdown, header, 3)),
			Categoria: strings.TrimSpace(group(markdown, header, 4)),
			Subtitulo: subtitleOf(block),
			Confianza: confidenceOf(block),
			// Every field the block cannot carry gets its first-parse default. An
			// `introducido_en_iter` of 0 is a sentinel for "not assigned yet": reconciliation fills
			// in a real iteration, and a first run forces it to 1 before storing.
			Estado:            StateOpen,
			IntroducidoEnIter: 0,
		}
		finding.Archivo, finding.Lineas = parseLocation(block)
		findings = append(findings, finding)
	}
	return findings
}

func group(text string, match []int, n int) string {
	start, stop := match[2*n], match[2*n+1]
	if start < 0 {
		return ""
	}
	return text[start:stop]
}

// SeverityOf buckets a finding into the three levels every decision uses (`XLANG-001`).
//
// **The word wins over the emoji.** The header carries the severity twice — one of five words, and
// one of five emoji the prompt asks be derived from it — and reading only the emoji is what once
// rendered two `Mayor` findings as critical and turned the Quality Gate red for them. The emoji
// stays as the fallback so an unrecognised word still parses, and so every row written under
// 1.7.2's drifted vocabulary keeps parsing exactly as it did.
func SeverityOf(severityWord, emoji string) string {
	switch strings.ToLower(strings.TrimSpace(severityWord)) {
	case "blocker", "crítico", "critico":
		return "critical"
	case "mayor":
		return "warning"
	case "menor", "info":
		return "info"
	}

	switch emoji {
	case "🔴", "🚨":
		return "critical"
	case "🟠", "⚠️":
		return "warning"
	default:
		return "info"
	}
}

// subtitleOf is the first line of the block that says something (REVIEW-024).
//
// Positional, because the format gives it no marker: the first non-empty line after the header that
// is not the location or the "why".
func subtitleOf(block string) string {
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "📍") || strings.HasPrefix(trimmed, "💭") {
			continue
		}
		return trimmed
	}
	return ""
}

// parseLocation splits `📍 Ubicación: src/app.ts:12-14` into its file and its lines (REVIEW-025).
//
// Split on the **last** colon, because a Windows path carries one of its own. The tail is only
// accepted as line numbers when it holds a digit, so `C:` stays part of a file-only location rather
// than becoming a line range — and a bare file path, which the model writes often, stays a file.
func parseLocation(block string) (file, lines *string) {
	match := locationPattern.FindStringSubmatch(block)
	if match == nil {
		return nil, nil
	}

	// The model markdown-formats paths everywhere else in its output and is not told not to here,
	// so the wrapping is stripped before anything is matched.
	cleaned := strings.TrimSpace(markdownWrapping.ReplaceAllString(strings.TrimSpace(match[1]), ""))
	if cleaned == "" {
		return nil, nil
	}

	if colon := strings.LastIndex(cleaned, ":"); colon >= 0 {
		left := strings.TrimSpace(cleaned[:colon])
		right := cleaned[colon+1:]
		if left != "" && strings.ContainsFunc(right, isASCIIDigit) {
			trimmedRight := strings.TrimSpace(right)
			return &left, &trimmedRight
		}
	}
	return &cleaned, nil
}

func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

// confidenceOf is the first confidence in the block, or nothing when the field is missing.
func confidenceOf(block string) *int64 {
	match := confidencePattern.FindStringSubmatch(block)
	if match == nil {
		return nil
	}
	value, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return nil
	}
	return &value
}

// FindingIdentity is the key two runs are matched on (REVIEW-026).
//
// File plus category, both normalised. It is exported because posting uses the **same** key to find
// a selected item's stored finding: two different keys would mean a posted comment that never gets
// its thread recorded.
func FindingIdentity(archivo *string, categoria string) string {
	file := ""
	if archivo != nil {
		file = strings.ToLower(strings.TrimPrefix(*archivo, "/"))
	}
	return file + "|" + strings.ToLower(categoria)
}

// identityOf falls back to the subtitle when a finding carries neither a file nor a category —
// otherwise every such finding would share the key `"|"` and reconcile as the same one.
func identityOf(f MemoryFinding) string {
	key := FindingIdentity(f.Archivo, f.Categoria)
	if key == "|" {
		return strings.ToLower(f.Subtitulo)
	}
	return key
}
