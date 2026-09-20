// Package tickets links a branch to the work item it is work for, caches that work item, and
// writes a readable copy of it to disk so the developer and the model read the same thing.
//
// Everything here reads, with one exception: `comment_ticket` publishes a review verdict onto the
// board when somebody presses a button. A review itself writes nothing to Azure — it reads the work
// item, writes its own row and stops.
package tickets

import (
	"path/filepath"
	"strings"
)

// Where a ticket's files live (WI-002), and how a title becomes a directory name.

// maxSegment is how long one path segment may be. Cut with no trailing separator, so a truncated
// title never ends in a dangling `-`.
const maxSegment = 60

// ID is a ticket's primary key (WI-001).
//
// Composed here rather than by callers, so the primary key and `idx_tickets_identity` cannot
// disagree about what "the same ticket" means. `externalID` is text and not a number: Azure numbers
// work items and Jira names them.
func ID(provider, org, project, externalID string) string {
	return provider + ":" + org + ":" + project + ":" + externalID
}

// Root is where the mirrors live: the `tickets_root_dir` setting, or `{base}/tickets`.
//
// Blank counts as unset — clearing the field in Settings writes an empty string and the user means
// "use the default" by it.
func Root(setting *string, fallback string) string {
	if setting != nil && strings.TrimSpace(*setting) != "" {
		return strings.TrimSpace(*setting)
	}
	return fallback
}

// MirrorPath is `{root}/{org}/{project}/{id}-{slug}`.
//
// The id leads the directory name so directories sort and complete by the number a person quotes,
// and so a retitled work item keeps its prefix.
func MirrorPath(root, org, project, externalID, title string) string {
	name := externalID
	if slug := Slug(title); slug != "" {
		name += "-" + slug
	}
	return filepath.Join(root, Slug(org), Slug(project), name)
}

// MirrorFor answers where a ticket's mirror belongs, and **a mirror never moves** (WI-022).
//
// A ticket already mirrored keeps the directory recorded in its row; a fresh name is computed only
// the first time it is seen. Blank counts as never mirrored.
//
// It is a function of its own rather than a condition inside the sync because of what it prevents:
// the directory used to be recomputed from the current title on every sync, so renaming the work
// item on the board silently relocated the mirror — and left the user's `notes/`, the one thing in
// there nobody else owns, stranded in a directory the app would never open again. Nothing moved them
// and nothing said so. Named here, the rule can be tested without a network or a keychain, which
// `TicketSync` needs both of.
//
// It is also what makes `Slug`'s case change safe: an existing directory is never recomputed, so
// nothing already on disk is renamed.
func MirrorFor(existing, root, org, project, externalID, title string) string {
	if strings.TrimSpace(existing) != "" {
		return existing
	}
	return MirrorPath(root, org, project, externalID, title)
}

// Slug turns a title into one path segment.
//
// **Case is preserved.** It was lower-cased first, and a user who opened the folder for *CF-E2E
// Ajuste de tabla (criterios en prosa)* found `3-cf-e2e-ajuste-de-tabla-criterios-en-prosa` and
// reported it as wrong. It was not wrong, but nothing was gained by it: what makes a path awkward is
// spaces and punctuation, not capitals, and both are still gone.
//
// Accented letters fold through the explicit table below and **not** through Unicode normalisation.
// The original was built with `InvariantGlobalization`, under which normalising was a no-op and
// *Facturación* would have named its directory `facturaci-n`. Invariant mode is off now, and the
// table stays anyway: a directory name must not depend on which ICU build a machine happens to ship.
func Slug(title string) string {
	folded := &strings.Builder{}
	folded.Grow(len(title))

	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			folded.WriteRune(r)
		case r == '-', r == '_':
			folded.WriteRune(r)
		default:
			if replacement, found := foldTable[r]; found {
				folded.WriteString(replacement)
				continue
			}
			folded.WriteByte('-')
		}
	}

	return trimSegment(collapseDashes(folded.String()))
}

// collapseDashes turns any run of separators into one, so `Ajuste de tabla (criterios)` does not
// become `Ajuste-de-tabla--criterios-`.
func collapseDashes(value string) string {
	out := &strings.Builder{}
	out.Grow(len(value))

	previousDash := false
	for i := 0; i < len(value); i++ {
		if value[i] == '-' {
			if !previousDash {
				out.WriteByte('-')
			}
			previousDash = true
			continue
		}
		previousDash = false
		out.WriteByte(value[i])
	}
	return out.String()
}

// trimSegment cuts to `maxSegment` characters and leaves no trailing separator, in that order: a
// cut that lands on one would otherwise produce a directory ending in `-`.
func trimSegment(value string) string {
	runes := []rune(value)
	if len(runes) > maxSegment {
		runes = runes[:maxSegment]
	}
	return strings.Trim(string(runes), "-_")
}

// foldTable is the explicit accent fold, and each replacement is spelled **in the case it was
// found** rather than upper-cased afterwards — the same bet against invariant globalisation, in a
// different place.
//
// Deliberately only what a work-item title in this application's languages carries: Spanish,
// Portuguese, Catalan, French and German. A letter that is not here becomes a separator, which is
// the same answer any other punctuation gets and is never wrong in a way that loses a directory.
var foldTable = map[rune]string{
	'á': "a", 'à': "a", 'ä': "a", 'â': "a", 'ã': "a", 'å': "a",
	'Á': "A", 'À': "A", 'Ä': "A", 'Â': "A", 'Ã': "A", 'Å': "A",
	'é': "e", 'è': "e", 'ë': "e", 'ê': "e",
	'É': "E", 'È': "E", 'Ë': "E", 'Ê': "E",
	'í': "i", 'ì': "i", 'ï': "i", 'î': "i",
	'Í': "I", 'Ì': "I", 'Ï': "I", 'Î': "I",
	'ó': "o", 'ò': "o", 'ö': "o", 'ô': "o", 'õ': "o", 'ø': "o",
	'Ó': "O", 'Ò': "O", 'Ö': "O", 'Ô': "O", 'Õ': "O", 'Ø': "O",
	'ú': "u", 'ù': "u", 'ü': "u", 'û': "u",
	'Ú': "U", 'Ù': "U", 'Ü': "U", 'Û': "U",
	'ñ': "n", 'Ñ': "N",
	'ç': "c", 'Ç': "C",
	'ß': "ss",
}
