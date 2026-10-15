package repomap

import (
	"cmp"
	"slices"
	"strings"
)

// The types a class diagram draws (ARCHIFY-006): what the map knows of each class, interface, enum,
// struct, record and mixin, with its members gathered from wherever its language puts them.

// MethodInfo is a method of a type.
type MethodInfo struct {
	Name       string
	Signature  string
	Visibility string
	Abstract   bool
	Line       int
}

// TypeInfo is a type declared in the repository.
type TypeInfo struct {
	Name    string
	Kind    Kind
	Path    string
	Line    int
	EndLine int
	Lang    Lang
	// Package is its file's package, where its language declares one (Go, Java): what a qualified
	// name such as `storage.Store` names.
	Package    string
	Abstract   bool
	Fields     []Member
	Methods    []MethodInfo
	Bases      []string
	Implements []string
	// UsedBy counts the other files that use it, confirmed.
	UsedBy int
}

// Types answers the types declared under scope — a folder, a file, or "" for the repository — the
// most used first.
//
// A type's methods are the declarations nested in it; a Go type's are also the methods declared on
// it in any file of its package, which is where Go keeps them.
func (s *Snapshot) Types(scope string) []TypeInfo {
	scope = strings.Trim(strings.ReplaceAll(scope, "\\", "/"), "/")
	in := func(path string) bool {
		return scope == "" || path == scope || strings.HasPrefix(path, scope+"/")
	}
	goMethods := s.goMethodsByOwner()

	out := make([]TypeInfo, 0, 32)
	for i, f := range s.files {
		if !in(f.Path) {
			continue
		}
		for d, decl := range f.Facts.Decls {
			if !decl.Kind.IsType() {
				continue
			}
			t := TypeInfo{
				Name: decl.Name, Kind: decl.Kind, Path: f.Path, Line: decl.Line, EndLine: decl.EndLine, Lang: f.Facts.Lang, Package: f.Facts.Package,
				Abstract: decl.Abstract, Fields: decl.Fields, Bases: decl.Bases, Implements: decl.Implements,
				Methods: make([]MethodInfo, 0, 8), UsedBy: s.usedBy(declAt{file: i, decl: d}),
			}
			seen := make(map[string]bool)
			for _, member := range f.Facts.Decls {
				if member.Parent == d && member.Kind == KindMethod && !seen[member.Name] {
					seen[member.Name] = true
					t.Methods = append(t.Methods, methodOf(member))
				}
			}
			if f.Facts.Lang == LangGo {
				for _, m := range goMethods[goOwnerKey(dirOf(f.Path), decl.Name)] {
					if !seen[m.Name] {
						seen[m.Name] = true
						t.Methods = append(t.Methods, m)
					}
				}
			}
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b TypeInfo) int {
		return cmp.Or(cmp.Compare(b.UsedBy, a.UsedBy), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line))
	})
	return out
}

// DeclsMatching are the top-level declarations a predicate accepts, the most used first, at most
// limit — what the diagram topics are picked from (ARCHIFY-009).
func (s *Snapshot) DeclsMatching(accept func(hit DeclHit) bool, limit int) []DeclHit {
	out := make([]DeclHit, 0, limit)
	for i, f := range s.files {
		for d, decl := range f.Facts.Decls {
			if decl.Parent >= 0 {
				continue
			}
			if hit := s.hit(declAt{file: i, decl: d}); accept(hit) {
				out = append(out, hit)
			}
		}
	}
	slices.SortFunc(out, func(a, b DeclHit) int {
		return cmp.Or(cmp.Compare(b.UsedBy, a.UsedBy), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line))
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func methodOf(d Decl) MethodInfo {
	return MethodInfo{Name: d.Name, Signature: d.Signature, Visibility: d.Visibility, Abstract: d.Abstract, Line: d.Line}
}

func goOwnerKey(dir, owner string) string { return dir + "\x00" + owner }

// goMethodsByOwner gathers every Go method by the folder and the receiver type it is declared on.
func (s *Snapshot) goMethodsByOwner() map[string][]MethodInfo {
	out := make(map[string][]MethodInfo)
	for _, f := range s.files {
		if f.Facts.Lang != LangGo {
			continue
		}
		for _, d := range f.Facts.Decls {
			if d.Kind == KindMethod && d.Owner != "" {
				key := goOwnerKey(dirOf(f.Path), d.Owner)
				out[key] = append(out[key], methodOf(d))
			}
		}
	}
	return out
}
