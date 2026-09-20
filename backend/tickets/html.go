package tickets

import (
	"fmt"
	"html"
	"strings"
)

// Turning Azure's rich text into something a person and a model both read.
//
// A work item's description and acceptance criteria are HTML — that is what the board's editor
// produces — and handing that to a model wastes a third of the block on markup while making the
// requirements harder to read, not easier. This converts the subset that editor actually emits and
// strips the rest, which is the honest trade: a converter that tried to be general would still meet
// something it did not know, and the failure of *that* is a requirement rendered as an empty box.

// substanceFloor is how much tag-stripped text a field needs before it counts as a requirement
// (WI-007).
//
// 25 characters, and it is measured after the markup is gone for a reason worth naming:
// `<div><b>-</b> </div>` is twenty characters of markup and one of content, and counting the former
// calls an empty box a requirement. One real organisation declared the acceptance-criteria field on
// eight of its thirty-three work-item types and had filled it on none of them — every one held
// exactly that string.
const substanceFloor = 25

// SubstanceLength is how many characters of real content an HTML fragment carries.
//
// Whitespace is collapsed as well as stripped: a field padded with `&nbsp;` and newlines is still
// empty, and counting those would clear the floor with nothing in it.
func SubstanceLength(fragment string) int {
	return len([]rune(strings.Join(strings.Fields(StripTags(fragment)), " ")))
}

// HasSubstance reports whether a field carries enough to be a requirement.
func HasSubstance(fragment string) bool { return SubstanceLength(fragment) >= substanceFloor }

// StripTags removes every tag and resolves entities, leaving the text a reader would see.
//
// Used for measuring and for comparing two fields (`WI-008`), never for what reaches the model —
// that goes through `ToMarkdown`, which keeps the structure.
func StripTags(fragment string) string {
	out := &strings.Builder{}
	out.Grow(len(fragment))

	depth := 0
	for i := 0; i < len(fragment); i++ {
		switch fragment[i] {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				out.WriteByte(fragment[i])
			}
		}
	}
	return html.UnescapeString(out.String())
}

// ToMarkdown converts a work item's HTML into Markdown.
//
// Hand-written rather than pulled in as a dependency, and the reason is the same one that keeps the
// fold table in `paths.go`: what arrives here is not arbitrary HTML, it is what one editor emits,
// and the whole of it is headings, emphasis, lists, links, images, code and tables. A general
// converter would bring a transitive tree to this process for a job whose entire surface fits on one
// screen — and would still have to be told what to do with the `<div>`-per-line shape that editor
// actually produces.
//
// Text arriving as plain text rather than HTML passes through: an organisation whose process stores
// the description as plain text is not a failure to convert.
func ToMarkdown(fragment string) string {
	if strings.TrimSpace(fragment) == "" {
		return ""
	}

	tokens := tokenize(fragment)
	rendered := renderTokens(tokens)

	// At most one blank line between blocks: the editor emits `<div><br></div>` for a pressed Enter,
	// and a description written with generous spacing turns into a page of blank lines otherwise.
	return strings.TrimSpace(collapseBlankLines(rendered))
}

// token is one piece of the fragment: a tag, or the text between two.
type token struct {
	tag     string
	closing bool
	attrs   map[string]string
	text    string
}

// tokenize splits HTML into tags and text without building a tree.
//
// A flat scan is enough because nothing here needs to know an element's children — every rule below
// is "what does this tag start or end", and list nesting is tracked with a counter. Building a tree
// would be more code for a decision this converter never makes.
func tokenize(fragment string) []token {
	tokens := make([]token, 0, 64)
	text := &strings.Builder{}

	flush := func() {
		if text.Len() > 0 {
			tokens = append(tokens, token{text: html.UnescapeString(text.String())})
			text.Reset()
		}
	}

	for i := 0; i < len(fragment); {
		length, isTag := tagAt(fragment, i)
		if !isTag {
			text.WriteByte(fragment[i])
			i++
			continue
		}

		flush()
		tokens = append(tokens, parseTag(fragment[i+1:i+length-1]))
		i += length
	}

	flush()
	return tokens
}

// tagAt reports whether a tag starts at `i`, and how long it is.
//
// The three tests are what a browser does, and each of them is a real work item's description:
//
//   - `<` must be followed by a letter or by `/` and a letter. A ticket writing `a < b` has a `<`
//     that opens nothing.
//   - there has to be a closing `>`. A description ending mid-comparison has none.
//   - no second `<` may appear before that `>`. Without this test, `si a < b entonces</p>` reads as
//     one tag from the `<` to the paragraph's own `>` — swallowing the sentence and emitting a bold
//     marker from the `b`, which is exactly what it did.
func tagAt(fragment string, i int) (length int, isTag bool) {
	if fragment[i] != '<' || i+1 >= len(fragment) {
		return 0, false
	}

	after := i + 1
	if fragment[after] == '/' {
		after++
	}
	if after >= len(fragment) || !isTagNameStart(fragment[after]) {
		return 0, false
	}

	for j := after; j < len(fragment); j++ {
		switch fragment[j] {
		case '>':
			return j - i + 1, true
		case '<':
			return 0, false
		}
	}
	return 0, false
}

func isTagNameStart(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// parseTag reads one tag's name and the two attributes this converter uses.
func parseTag(inside string) token {
	inside = strings.TrimSuffix(strings.TrimSpace(inside), "/")

	closing := strings.HasPrefix(inside, "/")
	inside = strings.TrimPrefix(inside, "/")

	name, rest, _ := strings.Cut(inside, " ")
	return token{
		tag:     strings.ToLower(strings.TrimSpace(name)),
		closing: closing,
		attrs:   parseAttributes(rest),
	}
}

// parseAttributes reads `key="value"` pairs, tolerating single quotes and an unquoted value.
func parseAttributes(rest string) map[string]string {
	attrs := map[string]string{}

	for len(rest) > 0 {
		rest = strings.TrimLeft(rest, " \t\r\n")
		key, after, found := strings.Cut(rest, "=")
		if !found {
			break
		}
		key = strings.ToLower(strings.TrimSpace(key))
		after = strings.TrimLeft(after, " \t\r\n")
		if after == "" {
			break
		}

		var value string
		switch after[0] {
		case '"', '\'':
			quote := after[0]
			closing := strings.IndexByte(after[1:], quote)
			if closing < 0 {
				return attrs
			}
			value, rest = after[1:1+closing], after[closing+2:]
		default:
			value, rest, _ = strings.Cut(after, " ")
		}
		attrs[key] = html.UnescapeString(value)
	}
	return attrs
}

// markdownWriter accumulates the converted text and the state one conversion carries.
type markdownWriter struct {
	out *strings.Builder
	// listDepth is how many lists deep we are, so a nested bullet indents rather than restarting.
	listDepth int
	// ordered tracks whether each open list is numbered, and counters its next item's number.
	ordered  []bool
	counters []int
	// inCode suppresses emphasis inside `<pre>`: a `*` there is a character, not a marker.
	inCode bool
	// linkHref is the pending `](…)` of an anchor whose text is still being read.
	linkHref string
	// cells collects one table row until `</tr>` decides how to lay it out.
	cells       []string
	inCell      bool
	cell        *strings.Builder
	headerRow   bool
	rowsInTable int
}

func renderTokens(tokens []token) string {
	w := &markdownWriter{out: &strings.Builder{}, cell: &strings.Builder{}}
	for _, t := range tokens {
		if t.tag == "" {
			w.writeText(t.text)
			continue
		}
		w.writeTag(t)
	}
	return w.out.String()
}

// write appends to whichever sink is open: a table cell, or the document.
func (w *markdownWriter) write(value string) {
	if w.inCell {
		w.cell.WriteString(value)
		return
	}
	w.out.WriteString(value)
}

// writeText appends content, collapsing the whitespace HTML treats as insignificant. Inside `<pre>`
// it is written exactly as it arrived, which is the whole point of the element.
func (w *markdownWriter) writeText(text string) {
	if w.inCode {
		w.write(text)
		return
	}
	collapsed := strings.Join(strings.Fields(text), " ")
	if collapsed == "" {
		// A run of pure whitespace between two inline elements is still a word break.
		if strings.ContainsAny(text, " \t\n\r") && w.endsWithWord() {
			w.write(" ")
		}
		return
	}
	if strings.HasPrefix(text, " ") && w.endsWithWord() {
		w.write(" ")
	}
	w.write(collapsed)
	if strings.HasSuffix(text, " ") {
		w.write(" ")
	}
}

// endsWithWord reports whether the last thing written could take a space after it, so a collapsed
// run of whitespace does not open a line or double a space.
func (w *markdownWriter) endsWithWord() bool {
	current := w.out.String()
	if w.inCell {
		current = w.cell.String()
	}
	if current == "" {
		return false
	}
	last := current[len(current)-1]
	return last != ' ' && last != '\n'
}

func (w *markdownWriter) writeTag(t token) {
	switch t.tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		w.heading(t)
	case "p", "div":
		w.block(t)
	case "br":
		w.write("\n")
	case "strong", "b":
		w.emphasis("**")
	case "em", "i":
		w.emphasis("*")
	case "code":
		w.emphasis("`")
	case "pre":
		w.preformatted(t)
	case "ul", "ol":
		w.list(t)
	case "li":
		w.item(t)
	case "a":
		w.anchor(t)
	case "img":
		w.image(t)
	case "table":
		w.table(t)
	case "tr":
		w.row(t)
	case "td", "th":
		w.cellTag(t)
	case "hr":
		w.write("\n\n---\n\n")
	}
}

func (w *markdownWriter) heading(t token) {
	if t.closing {
		w.write("\n\n")
		return
	}
	level := int(t.tag[1] - '0')
	w.write("\n\n" + strings.Repeat("#", level) + " ")
}

// block is `<p>` and `<div>`, which that editor uses interchangeably — one line per `<div>` is its
// normal shape, so a closing one is a paragraph break.
func (w *markdownWriter) block(t token) {
	if t.closing {
		w.write("\n\n")
	}
}

func (w *markdownWriter) emphasis(marker string) {
	if w.inCode {
		return
	}
	w.write(marker)
}

func (w *markdownWriter) preformatted(t token) {
	w.inCode = !t.closing
	if t.closing {
		w.write("\n```\n\n")
		return
	}
	w.write("\n\n```\n")
}

func (w *markdownWriter) list(t token) {
	if t.closing {
		if w.listDepth > 0 {
			w.listDepth--
			w.ordered = w.ordered[:len(w.ordered)-1]
			w.counters = w.counters[:len(w.counters)-1]
		}
		if w.listDepth == 0 {
			w.write("\n")
		}
		return
	}
	w.listDepth++
	w.ordered = append(w.ordered, t.tag == "ol")
	w.counters = append(w.counters, 0)
	w.write("\n")
}

func (w *markdownWriter) item(t token) {
	if t.closing {
		w.write("\n")
		return
	}
	if w.listDepth == 0 {
		// An `<li>` with no list around it: the editor produces this when a bullet is pasted. It is
		// still an item, and rendering it as one beats dropping the line.
		w.write("- ")
		return
	}

	indent := strings.Repeat("  ", w.listDepth-1)
	if w.ordered[w.listDepth-1] {
		w.counters[w.listDepth-1]++
		w.write(fmt.Sprintf("%s%d. ", indent, w.counters[w.listDepth-1]))
		return
	}
	w.write(indent + "- ")
}

func (w *markdownWriter) anchor(t token) {
	if t.closing {
		if w.linkHref != "" {
			w.write("](" + w.linkHref + ")")
			w.linkHref = ""
		}
		return
	}
	href := strings.TrimSpace(t.attrs["href"])
	if href == "" {
		return
	}
	w.linkHref = href
	w.write("[")
}

// image keeps the source as it stands. A mirrored attachment's `src` is rewritten later, by
// `Relink`, once the file it points at exists on disk (`WI-004`).
func (w *markdownWriter) image(t token) {
	src := strings.TrimSpace(t.attrs["src"])
	if src == "" {
		return
	}
	alt := strings.TrimSpace(t.attrs["alt"])
	w.write(fmt.Sprintf("![%s](%s)", alt, src))
}

func (w *markdownWriter) table(t token) {
	if t.closing {
		w.write("\n")
		return
	}
	w.rowsInTable = 0
	w.write("\n\n")
}

func (w *markdownWriter) row(t token) {
	if !t.closing {
		w.cells = w.cells[:0]
		w.headerRow = false
		return
	}
	if len(w.cells) == 0 {
		return
	}

	w.write("| " + strings.Join(w.cells, " | ") + " |\n")
	w.rowsInTable++

	// Markdown needs a separator after the first row, header or not: without one the table renders
	// as a wall of pipes. A board's tables frequently have no `<th>` at all.
	if w.rowsInTable == 1 {
		w.write("|" + strings.Repeat(" --- |", len(w.cells)) + "\n")
	}
	w.cells = w.cells[:0]
}

func (w *markdownWriter) cellTag(t token) {
	if !t.closing {
		w.inCell = true
		w.cell.Reset()
		w.headerRow = w.headerRow || t.tag == "th"
		return
	}
	w.inCell = false
	// A newline inside a cell would break the row; a pipe would open a column that is not there.
	value := strings.TrimSpace(w.cell.String())
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", `\|`)
	w.cells = append(w.cells, strings.Join(strings.Fields(value), " "))
}

// collapseBlankLines leaves at most one blank line between blocks.
func collapseBlankLines(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))

	blanks := 0
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			blanks++
			if blanks > 1 {
				continue
			}
		} else {
			blanks = 0
		}
		out = append(out, trimmed)
	}
	return strings.Join(out, "\n")
}
