package codemap

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The map as text, for a model to read (CODEMAP-014, CODEMAP-015): every answer cites file:line,
// and every answer is clipped to a size an agent can act on rather than wade through.

// MaxTextChars bounds one text answer.
const MaxTextChars = 8000

// Clip shortens text to at most limit bytes on a line boundary, saying how much was left out.
func Clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := strings.LastIndexByte(text[:limit], '\n')
	if cut <= 0 {
		cut = limit
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
	}
	rest := strings.Count(text[cut:], "\n")
	return text[:cut] + "\n… " + strconv.Itoa(rest) + " more lines not shown\n"
}

func refLine(r SymbolRef) string {
	where := r.File
	if r.Line > 0 {
		where += ":" + strconv.Itoa(r.Line)
	}
	if where == "" {
		return fmt.Sprintf("%s (%s)", r.Name, r.Kind)
	}
	return fmt.Sprintf("%s (%s) %s", r.Name, r.Kind, where)
}

// RenderSearch lists search results, one per line.
func RenderSearch(results []SymbolRef) string {
	if len(results) == 0 {
		return "No symbol matches.\n"
	}
	var b strings.Builder
	for _, r := range results {
		b.WriteString("- " + refLine(r) + "\n")
	}
	return b.String()
}

// RenderLookup is a symbol's detail, or the candidates a name matched.
func RenderLookup(lookup SymbolLookup) string {
	if lookup.Detail != nil {
		return RenderDetail(*lookup.Detail)
	}
	if len(lookup.Candidates) == 0 {
		return "No symbol has that name. Try codemap_search with part of it.\n"
	}
	return "Several symbols have that name; ask again with one of these IDs:\n" + renderIDs(lookup.Candidates)
}

func renderIDs(refs []SymbolRef) string {
	var b strings.Builder
	for _, r := range refs {
		b.WriteString("- " + r.ID + "  — " + refLine(r) + "\n")
	}
	return b.String()
}

// RenderDetail describes one symbol and its neighbourhood.
func RenderDetail(d SymbolDetail) string {
	var b strings.Builder
	b.WriteString("# " + refLine(d.Symbol) + "\n")
	b.WriteString("id: " + d.Symbol.ID + "\n")
	if d.Symbol.Signature != "" {
		b.WriteString("signature: " + d.Symbol.Signature + "\n")
	}
	if d.Doc != "" {
		b.WriteString("doc: " + d.Doc + "\n")
	}
	if d.Parent != nil {
		b.WriteString("inside: " + refLine(*d.Parent) + "\n")
	}
	if d.Bound != "" {
		b.WriteString("bound to the frontend by Wails as " + d.Bound + "\n")
	}
	renderRelations(&b, "Called by", d.Callers)
	renderRelations(&b, "Calls", d.Callees)
	renderRelations(&b, "Implemented by", d.Implementations)
	renderRelations(&b, "Implements", d.Implements)
	if len(d.Tables) > 0 {
		b.WriteString("\n## Tables it reaches\n")
		for _, use := range d.Tables {
			verbs := make([]string, 0, 3)
			if use.Read {
				verbs = append(verbs, "reads")
			}
			if use.Write {
				verbs = append(verbs, "writes")
			}
			if use.Mention {
				verbs = append(verbs, "names")
			}
			line := "- " + use.Table.Name + " (" + strings.Join(verbs, ", ") + ")"
			if use.Via != nil {
				line += " via " + refLine(*use.Via)
			}
			b.WriteString(line + "\n")
		}
	}
	if len(d.Tests) > 0 {
		b.WriteString("\n## Tests that reach it\n")
		for _, test := range d.Tests {
			b.WriteString("- " + refLine(test) + "\n")
		}
	}
	if len(d.Unresolved) > 0 {
		b.WriteString("\n## Only known at run time (a table or callee held in a variable)\n")
		for _, at := range d.Unresolved {
			b.WriteString("- " + at.File + ":" + strconv.Itoa(at.Line) + "\n")
		}
	}
	return b.String()
}

func renderRelations(b *strings.Builder, title string, relations []Relation) {
	if len(relations) == 0 {
		return
	}
	b.WriteString("\n## " + title + "\n")
	for _, r := range relations {
		line := "- " + refLine(r.Symbol) + " [" + r.Kind
		if r.File != "" {
			line += " at " + r.File + ":" + strconv.Itoa(r.Line)
		}
		line += "]"
		if r.Via != nil {
			line += " via " + r.Via.Name
		}
		b.WriteString(line + "\n")
	}
}

// RenderFlow draws a flow as an indented tree; each step says how it was reached.
func RenderFlow(f Flow) string {
	var b strings.Builder
	b.WriteString("Flow from " + refLine(f.Root.Symbol) + "\n")
	var walk func(n FlowNode, depth int)
	walk = func(n FlowNode, depth int) {
		for _, child := range n.Children {
			line := strings.Repeat("  ", depth) + "- [" + child.Via + "] " + refLine(child.Symbol)
			if child.Repeat {
				line += " (shown above)"
			}
			if child.Truncated {
				line += " …"
			}
			b.WriteString(line + "\n")
			walk(child, depth+1)
		}
	}
	walk(f.Root, 0)
	if len(f.Root.Children) == 0 {
		b.WriteString("(it leads nowhere the map can see)\n")
	}
	if f.Truncated {
		b.WriteString("(cut at " + strconv.Itoa(f.Steps) + " steps)\n")
	}
	return b.String()
}

// RenderImpact lists what may break and the tests to run.
func RenderImpact(im Impact) string {
	var b strings.Builder
	b.WriteString("Impact of changing " + refLine(im.Root) + "\n")
	if len(im.EntryPoints) > 0 {
		b.WriteString("\n## Where it surfaces\n")
		for _, r := range im.EntryPoints {
			b.WriteString("- " + refLine(r) + "\n")
		}
	}
	for i, level := range im.Levels {
		b.WriteString("\n## " + strconv.Itoa(i+1) + " step(s) upstream\n")
		for _, r := range level {
			b.WriteString("- " + refLine(r) + "\n")
		}
	}
	if len(im.GoTests) > 0 || len(im.TSTests) > 0 {
		b.WriteString("\n## Tests to run\n")
		for _, group := range im.GoTests {
			command := group.Command
			if group.WorkingDir != "" {
				command = "(cd " + group.WorkingDir + " && " + command + ")"
			}
			b.WriteString("- " + command + "\n")
		}
		for _, group := range im.TSTests {
			if group.Command == "" {
				b.WriteString("- " + strings.Join(group.Files, " ") + " (no runner found)\n")
				continue
			}
			command := group.Command
			if group.PackageDir != "" {
				command = "(cd " + group.PackageDir + " && " + command + ")"
			}
			b.WriteString("- " + command + "\n")
		}
	} else {
		b.WriteString("\nNo test reaches it.\n")
	}
	if im.Truncated {
		b.WriteString("(there is more upstream than is listed)\n")
	}
	return b.String()
}

// RenderOverview summarises the map.
func RenderOverview(o Overview) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Code map: %d symbols, %d relations.\n", o.Status.Nodes, o.Status.Edges)
	for _, language := range o.Status.Languages {
		fmt.Fprintf(&b, "- %s: %s, %d files, %d symbols", language.Lang, language.State, language.Files, language.Symbols)
		if language.Detail != "" {
			b.WriteString(" — " + language.Detail)
		}
		b.WriteString("\n")
	}
	for _, note := range o.Status.Notes {
		b.WriteString("  note: " + note + "\n")
	}
	if len(o.Areas) > 0 {
		b.WriteString("\n## Where the code is (folder, language, symbols)\n")
		for _, area := range o.Areas {
			fmt.Fprintf(&b, "- %s (%s) %d\n", area.Path, area.Lang, area.Symbols)
		}
	}
	if len(o.Services) > 0 {
		b.WriteString("\n## Go services Wails binds to the frontend\n")
		for _, s := range o.Services {
			fmt.Fprintf(&b, "- %s (%s): %d methods, called from %d frontend symbols\n", s.Name, s.File, s.Methods, s.CalledBy)
		}
	}
	if o.Endpoints > 0 {
		fmt.Fprintf(&b, "\n%d endpoints join the frontend to Go handlers by a shared key.\n", o.Endpoints)
	}
	if len(o.Tables) > 0 {
		b.WriteString("\n## Tables (readers / writers)\n")
		for _, t := range o.Tables {
			fmt.Fprintf(&b, "- %s %d / %d\n", t.Table.Name, t.Readers, t.Writers)
		}
	}
	if len(o.Hotspots) > 0 {
		b.WriteString("\n## Most depended on\n")
		for _, h := range o.Hotspots {
			fmt.Fprintf(&b, "- %s — %d callers\n", refLine(h.Symbol), h.Callers)
		}
	}
	return b.String()
}
