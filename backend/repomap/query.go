package repomap

import (
	"cmp"
	"slices"
	"strings"
)

// Questions about declarations (REPOMAP-006): what a file declares, where a name is declared, who
// uses it, and what is used most.

// Limits on what one answer lists.
const (
	SearchLimit   = 30
	UsagesLimit   = 200
	MostUsedLimit = 40
)

// File is one file's outline and connections, or nil when the map does not hold it.
func (s *Snapshot) File(path string) *FileDetail {
	i, ok := s.lookup(path)
	if !ok {
		return nil
	}
	f := s.files[i]
	detail := &FileDetail{
		Path:         f.Path,
		Language:     f.Facts.Lang.Name(),
		Lines:        f.Facts.Lines,
		Parsed:       f.Facts.Parsed,
		Declarations: make([]OutlineRow, 0, len(f.Facts.Decls)),
		Imports:      s.paths(s.imports[i]),
		ImportedBy:   s.paths(s.importedBy[i]),
		External:     append(make([]string, 0, len(s.external[i])), s.external[i]...),
	}
	for _, d := range f.Facts.Decls {
		depth := 0
		for parent := d.Parent; parent >= 0 && depth < len(f.Facts.Decls); parent = f.Facts.Decls[parent].Parent {
			depth++
		}
		detail.Declarations = append(detail.Declarations, OutlineRow{
			Name: d.Name, Kind: d.Kind, Line: d.Line, EndLine: d.EndLine, Signature: d.Signature, Depth: depth,
		})
	}
	return detail
}

func (s *Snapshot) paths(files []int) []string {
	out := make([]string, 0, len(files))
	for _, i := range files {
		out = append(out, s.files[i].Path)
	}
	return out
}

// query is a name as a person types it: `save`, `save()`, `Cart.total`, `Cart::total`.
type query struct {
	owner, name string
}

func parseQuery(raw string) query {
	q := strings.TrimSuffix(strings.TrimSpace(raw), "()")
	if owner, name, found := strings.Cut(q, "::"); found {
		return query{owner: lastPart(owner), name: name}
	}
	if i := strings.LastIndexByte(q, '.'); i > 0 && i < len(q)-1 {
		return query{owner: lastPart(q[:i]), name: q[i+1:]}
	}
	return query{name: q}
}

func lastPart(s string) string {
	if i := strings.LastIndexAny(s, ".:"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// How well a declaration's name matches a query, best first.
const (
	matchExact = iota
	matchFolded
	matchPrefix
	matchContains
	noMatch
)

func matchOf(name, wanted string) int {
	switch {
	case name == wanted:
		return matchExact
	case strings.EqualFold(name, wanted):
		return matchFolded
	}
	lower, want := strings.ToLower(name), strings.ToLower(wanted)
	switch {
	case strings.HasPrefix(lower, want):
		return matchPrefix
	case len(want) >= 3 && strings.Contains(lower, want):
		return matchContains
	}
	return noMatch
}

// Search finds declarations by name, the closest matches first and, among those, the most used.
func (s *Snapshot) Search(raw string, limit int) []DeclHit {
	q := parseQuery(raw)
	type ranked struct {
		hit   DeclHit
		match int
	}
	found := make([]ranked, 0, 32)
	if q.name != "" {
		for name, ats := range s.decls {
			match := matchOf(name, q.name)
			if match == noMatch {
				continue
			}
			for _, at := range ats {
				if q.owner == "" || strings.EqualFold(s.parentName(at), q.owner) {
					found = append(found, ranked{hit: s.hit(at), match: match})
				}
			}
		}
	}
	slices.SortFunc(found, func(a, b ranked) int {
		return cmp.Or(
			cmp.Compare(a.match, b.match),
			cmp.Compare(b.hit.UsedBy, a.hit.UsedBy),
			cmp.Compare(a.hit.Path, b.hit.Path),
			cmp.Compare(a.hit.Line, b.hit.Line),
		)
	})
	hits := make([]DeclHit, 0, min(limit, len(found)))
	for _, r := range found[:min(limit, len(found))] {
		hits = append(hits, r.hit)
	}
	return hits
}

func (s *Snapshot) parentName(at declAt) string {
	d := s.decl(at)
	if d.Parent < 0 {
		return ""
	}
	return s.files[at.file].Facts.Decls[d.Parent].Name
}

func (s *Snapshot) hit(at declAt) DeclHit {
	d := s.decl(at)
	return DeclHit{
		Path: s.files[at.file].Path, Name: d.Name, Kind: d.Kind, Line: d.Line, EndLine: d.EndLine,
		Signature: d.Signature, Parent: s.parentName(at), UsedBy: s.usedBy(at),
	}
}

// usedBy counts the other files that mention a declaration's name and can see its file.
func (s *Snapshot) usedBy(at declAt) int {
	n := 0
	for _, m := range s.mentions[s.decl(at).Name] {
		if m.file != at.file && s.sees(m.file, at.file) {
			n++
		}
	}
	return n
}

// Usages lists the files that use a name — of the declarations in path, when one is given. A file
// that mentions the name without seeing any declaring file is kept as a use by name only, unless
// its imports are all known and in the same language: then it means some other declaration of the
// name, and it is counted as excluded rather than listed.
func (s *Snapshot) Usages(raw, path string, limit int) UsageReport {
	q := parseQuery(raw)
	report := UsageReport{Declarations: make([]DeclHit, 0, 4), Usages: make([]Usage, 0, 16)}
	ats := s.decls[q.name]
	if in, ok := s.lookup(path); ok && strings.TrimSpace(path) != "" {
		ats = slices.DeleteFunc(slices.Clone(ats), func(at declAt) bool { return at.file != in })
	}
	if q.owner != "" {
		ats = slices.DeleteFunc(slices.Clone(ats), func(at declAt) bool { return !strings.EqualFold(s.parentName(at), q.owner) })
	}
	if len(ats) == 0 {
		return report
	}
	owners := make(map[int]bool, len(ats))
	for _, at := range ats {
		owners[at.file] = true
		report.Declarations = append(report.Declarations, s.hit(at))
	}

	for _, m := range s.mentions[q.name] {
		if owners[m.file] {
			continue
		}
		confirmed, sameFamily := false, true
		for owner := range owners {
			confirmed = confirmed || s.sees(m.file, owner)
			sameFamily = sameFamily && family(s.files[owner].Facts.Lang) == family(s.files[m.file].Facts.Lang)
		}
		if !confirmed && sameFamily && s.knowsImports[m.file] {
			report.Excluded++
			continue
		}
		usage := Usage{Path: s.files[m.file].Path, Line: m.line, Confirmed: confirmed}
		if within := enclosing(s.files[m.file].Facts, m.line); within != nil {
			usage.Within = within.Signature
		}
		report.Usages = append(report.Usages, usage)
	}
	slices.SortFunc(report.Usages, func(a, b Usage) int {
		if a.Confirmed != b.Confirmed {
			if a.Confirmed {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Path, b.Path)
	})
	if len(report.Usages) > limit {
		report.Usages = report.Usages[:limit]
	}
	return report
}

// genericNames are too common to mean one declaration, so they never make the most-used list.
var genericNames = map[string]bool{
	"main": true, "init": true, "index": true, "test": true, "tests": true, "setup": true, "default": true,
	"get": true, "set": true, "run": true, "new": true, "create": true, "update": true, "delete": true,
	"data": true, "value": true, "values": true, "type": true, "name": true, "props": true, "state": true,
	"item": true, "items": true, "list": true, "util": true, "utils": true, "helper": true, "helpers": true,
	"config": true, "options": true, "handler": true, "handle": true, "render": true, "build": true,
}

// MostUsed is the top-level declarations the most other files use, by confirmed uses.
func (s *Snapshot) MostUsed(limit int) []DeclHit {
	hits := make([]DeclHit, 0, limit)
	for name, ats := range s.decls {
		if len(name) < 3 || genericNames[strings.ToLower(name)] {
			continue
		}
		for _, at := range ats {
			d := s.decl(at)
			if d.Parent >= 0 || d.Kind == KindMethod {
				continue
			}
			if used := s.usedBy(at); used > 0 {
				hit := s.hit(at)
				hit.UsedBy = used
				hits = append(hits, hit)
			}
		}
	}
	slices.SortFunc(hits, func(a, b DeclHit) int {
		return cmp.Or(cmp.Compare(b.UsedBy, a.UsedBy), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line))
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}
