package tickets

import (
	"context"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
)

// Publishing a verdict onto the board (WI-022).
//
// **The only write in this feature**, and it happens because somebody pressed a button. A review is
// run many times while work is in progress, and a board that collects every one of those attempts is
// worse than a board with nothing on it — so the verdict is rendered first and the button sits
// underneath it.
//
// State transitions are **not** built and their absence is asserted: a comment is additive and
// anyone can delete it, while a transition moves a card other people are looking at, and its legal
// states belong to the project's process — `New`/`Active`/`Resolved` under Agile, `Committed`/`Done`
// under Scrum — which is not something this app can name from the outside.

// The three refusals, each by name. A publish that silently does nothing is the failure this
// feature already made once with linking.
var (
	// ErrNotAzureTicket refuses a provider with no comments endpoint here.
	ErrNotAzureTicket = errors.New("Only Azure DevOps work items can be commented on") //nolint:staticcheck // ST1005: VERBATIM
	// ErrEmptyComment refuses a blank body: an empty comment on a board is noise nobody can undo
	// except by deleting it.
	ErrEmptyComment = errors.New("There is nothing to publish") //nolint:staticcheck // ST1005: VERBATIM
)

// Comment publishes a body onto the linked work item and answers its URL (WI-022).
//
// The **body travels from the renderer**, not a run id: the button publishes the text the user just
// read, and letting the backend rebuild it from the stored row is how what was approved and what was
// posted come apart.
func (d Deps) Comment(ctx context.Context, ticketID, body string) (string, error) {
	if strings.TrimSpace(body) == "" {
		return "", ErrEmptyComment
	}

	ticket, err := d.Store.Get(ctx, ticketID)
	if err != nil {
		return "", err
	}
	if ticket.Provider != "azure" {
		return "", ErrNotAzureTicket
	}

	id, err := numericID(ticket.ExternalID)
	if err != nil {
		return "", err
	}

	client, err := d.clientFor(ticket.Org)
	if err != nil {
		return "", err
	}
	if _, err := client.AddComment(ctx, ticket.Project, id, ToHTML(body)); err != nil {
		return "", err
	}
	return ticket.WebURL, nil
}

// The markdown this converter recognises. Anything else reaches the board as the text it is.
var (
	headingLine  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	bulletLine   = regexp.MustCompile(`^\s*[-*]\s+(.*)$`)
	numberedLine = regexp.MustCompile(`^\s*\d+[.)]\s+(.*)$`)
	boldSpan     = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	italicSpan   = regexp.MustCompile(`(^|[^*])\*([^*]+)\*`)
	codeSpan     = regexp.MustCompile("`([^`]+)`")
)

// ToHTML converts a verdict's markdown into the HTML an Azure comment renders.
//
// Azure comments are rich text, so markdown arrives as its own punctuation — `## VERIFICACIÓN` and
// `**cumple**`, literally, on a page other people read.
//
// **Escaping happens before any tag is added**, and that order is the rule: a verdict quoting a diff
// carries `<`, `>` and `&`, and escaping afterwards would either mangle the tags this function just
// wrote or let the quoted diff close one.
func ToHTML(markdown string) string {
	out := &strings.Builder{}
	inList := false
	inCodeBlock := false

	closeList := func() {
		if inList {
			out.WriteString("</ul>")
			inList = false
		}
	}

	for _, raw := range strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n") {
		line := strings.TrimRight(raw, " \t")

		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			closeList()
			if inCodeBlock {
				out.WriteString("</pre>")
			} else {
				out.WriteString("<pre>")
			}
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			// Inside a fence, everything is content: escaped, and with no inline conversion at all.
			out.WriteString(html.EscapeString(line) + "\n")
			continue
		}

		switch {
		case strings.TrimSpace(line) == "":
			closeList()

		case headingLine.MatchString(line):
			closeList()
			match := headingLine.FindStringSubmatch(line)
			level := min(len(match[1])+1, 6) // one level down: a comment sits inside somebody's page
			fmt.Fprintf(out, "<h%d>%s</h%d>", level, inlineHTML(match[2]), level)

		case bulletLine.MatchString(line), numberedLine.MatchString(line):
			if !inList {
				out.WriteString("<ul>")
				inList = true
			}
			fmt.Fprintf(out, "<li>%s</li>", inlineHTML(listItemText(line)))

		default:
			closeList()
			fmt.Fprintf(out, "<div>%s</div>", inlineHTML(line))
		}
	}

	closeList()
	if inCodeBlock {
		// An unclosed fence is the model's typo, not a reason to publish a broken element.
		out.WriteString("</pre>")
	}
	return out.String()
}

func listItemText(line string) string {
	if match := bulletLine.FindStringSubmatch(line); match != nil {
		return match[1]
	}
	return numberedLine.FindStringSubmatch(line)[1]
}

// inlineHTML escapes the text and *then* adds the three inline elements, in that order.
func inlineHTML(text string) string {
	escaped := html.EscapeString(text)
	escaped = codeSpan.ReplaceAllString(escaped, "<code>$1</code>")
	escaped = boldSpan.ReplaceAllString(escaped, "<b>$1</b>")
	escaped = italicSpan.ReplaceAllString(escaped, "$1<i>$2</i>")
	return escaped
}
