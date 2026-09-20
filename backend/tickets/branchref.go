package tickets

import (
	"regexp"
	"strings"
)

// The ticket a branch name looks like it is work for (WI-006).
//
// **A suggestion, never a link.** `TicketStore.ForBranch` answers only from the explicit row, so no
// review is ever judged against a work item nobody chose. This exists so the link dialog opens with
// the right number already in the field.

// Suggestion is a work item a branch name appears to be about.
type Suggestion struct {
	Provider   string `json:"provider"`
	ExternalID string `json:"external_id"`
}

// The three shapes recognised, in the order they are tried.
var (
	// azureRef is the `AB#1234` Azure's own commit integration uses.
	azureRef = regexp.MustCompile(`AB#(\d+)`)
	// jiraKey is upper case **only**, and that is the rule rather than an oversight: accepting lower
	// case matches `utf-8` in `feature/utf-8-encoding` and suggests a ticket called `UTF-8`.
	jiraKey = regexp.MustCompile(`\b([A-Z][A-Z0-9]+-\d+)\b`)
	// leadingNumber is a number opening the branch's own last segment.
	leadingNumber = regexp.MustCompile(`^(\d+)`)
)

// SuggestForBranch reads a branch name for the work item it is probably about.
//
// The number is looked for on the **last segment only**: `feature/1234-thing` is about 1234, and a
// repository whose branches live under `2025/` would otherwise suggest the year on every one.
//
// A date-led branch such as `release/2025-cleanup` still resolves to work item 2025. That is an
// accepted false positive, pinned by a test: nothing in the name separates a year from a work-item
// number, and rejecting four-digit ids would reject the common real case.
func SuggestForBranch(branch string) *Suggestion {
	name := strings.TrimSpace(branch)
	if name == "" {
		return nil
	}

	if match := azureRef.FindStringSubmatch(name); match != nil {
		return &Suggestion{Provider: "azure", ExternalID: match[1]}
	}
	if match := jiraKey.FindStringSubmatch(name); match != nil {
		return &Suggestion{Provider: "jira", ExternalID: match[1]}
	}

	segments := strings.Split(name, "/")
	last := segments[len(segments)-1]
	if match := leadingNumber.FindStringSubmatch(last); match != nil {
		return &Suggestion{Provider: "azure", ExternalID: match[1]}
	}
	return nil
}
