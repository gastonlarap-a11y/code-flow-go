package tickets

import (
	"fmt"
	"strings"
)

// What the ticket asks for, and where that was found (WI-007, WI-008).

// The two fields the reader walks when nothing else is configured, in this order.
//
// Measured against a live organisation before being written down: over three work items of one
// active sprint, `Microsoft.VSTS.Common.AcceptanceCriteria` held two characters
// (`<div><b>-</b></div>`) on all three, the sixteen `Custom.*` fields were byte-identical across
// them — the refinement form, not its answers — and `System.Description` was the only field that
// differed and the only one carrying requirements.
const (
	FieldAcceptanceCriteria = "Microsoft.VSTS.Common.AcceptanceCriteria"
	FieldDescription        = "System.Description"
)

// The three shapes requirements arrive in.
const (
	// ModeList: an enumerable list, already numbered `AC-1 … AC-N` in Items.
	ModeList = "list"
	// ModeProse: narrative. The model enumerates it, because splitting prose by regex cuts rules in
	// half and the model then reports failures that belong to the splitting.
	ModeProse = "prose"
	// ModeNone: no field carried enough to be a requirement, which the review says out loud.
	ModeNone = "none"
)

// Criteria is what a ticket asks for, in the shape the renderer types it.
type Criteria struct {
	Mode string `json:"mode"`
	// Field is the Azure reference name the content came from, null when nothing was usable — the
	// picker shows it, so a team can see which field their requirements would be read from before
	// anything is linked.
	Field    *string  `json:"field"`
	Markdown string   `json:"markdown"`
	Items    []string `json:"items"`
}

// NoCriteria is the answer for a work item that carries no requirement.
func NoCriteria() Criteria {
	return Criteria{Mode: ModeNone, Items: []string{}}
}

// CriteriaFieldOrder is the field order for one board, from the
// `ticket_criteria_fields:{org}:{project}` setting, falling back to the two defaults.
//
// Comma-separated, blanks dropped; an entirely blank setting is the same as an unset one, because
// clearing the field in Settings writes an empty string.
func CriteriaFieldOrder(setting *string) []string {
	if setting != nil {
		fields := make([]string, 0, 4)
		for _, field := range strings.Split(*setting, ",") {
			if trimmed := strings.TrimSpace(field); trimmed != "" {
				fields = append(fields, trimmed)
			}
		}
		if len(fields) > 0 {
			return fields
		}
	}
	return []string{FieldAcceptanceCriteria, FieldDescription}
}

// ReadCriteria walks the field order and takes the first field that clears the substance floor and
// is not a template (WI-007).
//
// `others` is the same field on other cached work items of the same board and type, which is what
// `IsTemplate` compares against. Empty means "nothing to compare with", and then nothing is
// excluded: guessing without a corpus would drop a real requirement the first time a board is used,
// which is exactly when nobody would suspect the extraction.
func ReadCriteria(item RawFields, order []string, others map[string][]string) Criteria {
	for _, field := range order {
		fragment := item.Field(field)
		if !HasSubstance(fragment) {
			continue
		}
		if IsTemplate(fragment, others[field]) {
			continue
		}

		name := field
		markdown := ToMarkdown(fragment)
		if items := listItems(fragment); len(items) > 0 {
			return Criteria{Mode: ModeList, Field: &name, Markdown: markdown, Items: items}
		}
		return Criteria{Mode: ModeProse, Field: &name, Markdown: markdown, Items: []string{}}
	}
	return NoCriteria()
}

// RawFields is the one thing the criteria reader needs of a work item: its fields by reference name.
//
// Declared here rather than taking `providers.RawWorkItem`, so extraction can be exercised against a
// literal map — which is what every interesting case is.
type RawFields interface {
	Field(name string) string
}

// FieldMap is the trivial implementation, and what a cached payload decodes into.
type FieldMap map[string]string

// Field answers one field's raw HTML.
func (f FieldMap) Field(name string) string { return f[name] }

// IsTemplate reports whether a field is the refinement form rather than its answers (WI-008).
//
// A candidate whose tag-stripped text matches the same field on another cached work item of the same
// board and type is the form: a requirement written for one work item does not reappear verbatim on
// another. Compared against at most twenty others, which is enough to be sure and cheap enough to do
// on every read.
func IsTemplate(fragment string, others []string) bool {
	candidate := normalisedText(fragment)
	if candidate == "" {
		return false
	}

	for i, other := range others {
		if i >= 20 {
			break
		}
		if normalisedText(other) == candidate {
			return true
		}
	}
	return false
}

// normalisedText is the comparable form: markup gone, whitespace collapsed, case folded. Case is
// folded because two work items created from the same template can differ in it and still be the
// same form.
func normalisedText(fragment string) string {
	return strings.ToLower(strings.Join(strings.Fields(StripTags(fragment)), " "))
}

// listItems numbers an HTML list's items `AC-1 … AC-N`, or answers nothing when the field carries no
// list.
//
// **A nested bullet extends the criterion above it** rather than becoming one of its own: a sub-case
// qualifies the rule it sits under, and promoting it produces a criterion that reads as a fragment
// and gets judged as one.
func listItems(fragment string) []string {
	tokens := tokenize(fragment)

	items := make([]string, 0, 8)
	current := &strings.Builder{}
	depth, listDepth := 0, 0
	open := false

	flush := func() {
		if !open {
			return
		}
		if text := strings.Join(strings.Fields(current.String()), " "); text != "" {
			items = append(items, fmt.Sprintf("AC-%d: %s", len(items)+1, text))
		}
		current.Reset()
		open = false
	}

	for _, t := range tokens {
		switch {
		case t.tag == "" && open:
			current.WriteString(" " + t.text)

		case t.tag == "ul" || t.tag == "ol":
			if t.closing {
				listDepth--
			} else {
				listDepth++
			}

		case t.tag == "li" && !t.closing:
			// Only a top-level item starts a criterion. A nested one keeps writing into the criterion
			// it qualifies, separated so the two do not run together as one word.
			if listDepth <= 1 {
				flush()
				open = true
				depth = listDepth
			} else if open {
				current.WriteString(" — ")
			}

		case t.tag == "li" && t.closing:
			if listDepth <= depth {
				flush()
			}

		case t.tag == "br" && open:
			current.WriteString(" ")
		}
	}
	flush()

	return items
}
