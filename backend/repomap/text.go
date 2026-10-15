package repomap

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The map as text a model reads (REPOMAP-010): an overview to orient itself by, a file's outline,
// declarations found by name, and the uses of one. Every line names a file, and a line when there
// is one, so what a model cites can be checked.

// Overview is the repository at a glance, within budget characters: its size and languages, its
// folders three levels deep with their files and declarations, and the declarations used most —
// the last section cut where the budget runs out.
func (s *Snapshot) Overview(budget int) string {
	var b strings.Builder
	languages := s.languages()
	names := make([]string, 0, len(languages))
	for _, l := range languages {
		names = append(names, l.Language+" "+strconv.Itoa(l.Files))
	}
	fmt.Fprintf(&b, "Repository map: %d code files (%s), %d declarations.\n", len(s.files), strings.Join(names, ", "), s.declarationCount())
	if s.truncated > 0 {
		fmt.Fprintf(&b, "(%d more code files past the map's bound are not in it.)\n", s.truncated)
	}

	b.WriteString("\nFolders (files · declarations):\n")
	for i, folder := range s.folderSummary(3) {
		if i == maxOverviewFolders {
			b.WriteString("  …\n")
			break
		}
		fmt.Fprintf(&b, "%s%s/ (%d · %d)\n", strings.Repeat("  ", folder.depth), folder.path, folder.files, folder.decls)
	}

	b.WriteString("\nMost used declarations (where · files using it):\n")
	for _, hit := range s.MostUsed(200) {
		line := fmt.Sprintf("  %s — %s:%d · %d\n", shownName(hit), hit.Path, hit.Line, hit.UsedBy)
		if b.Len()+len(line) > budget {
			break
		}
		b.WriteString(line)
	}
	return clipText(b.String(), budget)
}

// maxOverviewFolders bounds the folder tree an overview lists.
const maxOverviewFolders = 80

type folderLine struct {
	path         string
	depth, files int
	decls        int
}

// folderSummary counts files and declarations per folder down to depth levels, the biggest first
// within each parent.
func (s *Snapshot) folderSummary(depth int) []folderLine {
	counts := make(map[string]*folderLine)
	for _, f := range s.files {
		parts := strings.Split(dirOf(f.Path), "/")
		if parts[0] == "" {
			continue
		}
		for d := 1; d <= min(depth, len(parts)); d++ {
			key := strings.Join(parts[:d], "/")
			line := counts[key]
			if line == nil {
				line = &folderLine{path: key, depth: d - 1}
				counts[key] = line
			}
			line.files++
			line.decls += len(f.Facts.Decls)
		}
	}
	lines := make([]folderLine, 0, len(counts))
	for _, line := range counts {
		lines = append(lines, *line)
	}
	// Each folder right after its parent, the biggest children first.
	slices.SortFunc(lines, func(a, b folderLine) int { return compareTree(a, b, counts) })
	return lines
}

func compareTree(a, b folderLine, counts map[string]*folderLine) int {
	pa, pb := strings.Split(a.path, "/"), strings.Split(b.path, "/")
	for i := 0; i < min(len(pa), len(pb)); i++ {
		if pa[i] == pb[i] {
			continue
		}
		ka, kb := strings.Join(pa[:i+1], "/"), strings.Join(pb[:i+1], "/")
		return cmp.Or(cmp.Compare(counts[kb].files, counts[ka].files), cmp.Compare(ka, kb))
	}
	return cmp.Compare(len(pa), len(pb))
}

func shownName(hit DeclHit) string {
	name := hit.Name
	if hit.Parent != "" {
		name = hit.Parent + "." + name
	}
	switch hit.Kind {
	case KindFunction, KindMethod:
		return name + "()"
	case KindConst, KindVar:
		return name
	}
	return string(hit.Kind) + " " + name
}

// OutlineText is one file's declarations and connections, or why there are none.
func (s *Snapshot) OutlineText(path string) string {
	detail := s.File(path)
	if detail == nil {
		return fmt.Sprintf("%s is not in the map: not a code file, ignored, generated, or past its bound.", path)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s, %d lines\n", detail.Path, detail.Language, detail.Lines)
	if !detail.Parsed {
		fmt.Fprintf(&b, "(%s is mapped as a file only: its declarations and imports are not read.)\n", detail.Language)
	}
	for _, row := range detail.Declarations {
		fmt.Fprintf(&b, "%s%d-%d  %s %s  — %s\n", strings.Repeat("  ", row.Depth+1), row.Line, row.EndLine, row.Kind, row.Name, row.Signature)
	}
	pathList := func(title string, paths []string) {
		if len(paths) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s:\n", title)
		for i, p := range paths {
			if i == 20 {
				fmt.Fprintf(&b, "  … and %d more\n", len(paths)-20)
				break
			}
			fmt.Fprintf(&b, "  %s\n", p)
		}
	}
	pathList("Imports", detail.Imports)
	pathList("Imported by", detail.ImportedBy)
	if len(detail.External) > 0 {
		fmt.Fprintf(&b, "Packages: %s\n", strings.Join(detail.External, ", "))
	}
	return b.String()
}

// DeclarationsText is the declarations a name finds, the closest and most used first.
func (s *Snapshot) DeclarationsText(query string) string {
	hits := s.Search(query, SearchLimit)
	if len(hits) == 0 {
		return fmt.Sprintf("Nothing is declared as %q. Try a shorter part of the name.", query)
	}
	var b strings.Builder
	for _, hit := range hits {
		fmt.Fprintf(&b, "%s:%d-%d  %s  — %s  (used from %d files)\n", hit.Path, hit.Line, hit.EndLine, shownName(hit), hit.Signature, hit.UsedBy)
	}
	return b.String()
}

// UsagesText is where a name is declared and used: confirmed uses first, then mentions by name
// only, and how many files meant a different declaration of it.
func (s *Snapshot) UsagesText(name, path string) string {
	report := s.Usages(name, path, UsagesLimit)
	if len(report.Declarations) == 0 {
		return fmt.Sprintf("Nothing is declared as %q.", name)
	}
	var b strings.Builder
	b.WriteString("Declared at:\n")
	for _, hit := range report.Declarations {
		fmt.Fprintf(&b, "  %s:%d  %s\n", hit.Path, hit.Line, shownName(hit))
	}
	if len(report.Usages) == 0 {
		b.WriteString("No other file uses it.\n")
	} else {
		b.WriteString("Used in:\n")
		for _, u := range report.Usages {
			fmt.Fprintf(&b, "  %s:%d", u.Path, u.Line)
			if u.Within != "" {
				fmt.Fprintf(&b, "  (in %s)", u.Within)
			}
			if !u.Confirmed {
				b.WriteString("  [name only: imports nothing that declares it]")
			}
			b.WriteString("\n")
		}
	}
	if report.Excluded > 0 {
		fmt.Fprintf(&b, "Not uses of this one: %d more files mention a different %s.\n", report.Excluded, name)
	}
	return b.String()
}

// clipText cuts text to at most n bytes, on a line boundary, saying so.
func clipText(text string, n int) string {
	if len(text) <= n {
		return text
	}
	cut := strings.LastIndexByte(text[:n], '\n')
	if cut < 0 {
		cut = n
	}
	return text[:cut] + "\n…\n"
}
