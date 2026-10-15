package repomap

import (
	"slices"
	"strings"
)

// fileRow is one indexed file.
type fileRow struct {
	Path  string
	Facts *Facts
}

// declAt names one declaration: the file it is in, and its index in that file.
type declAt struct{ file, decl int }

// mentionAt is a file that mentions a name, and the first line it does.
type mentionAt struct{ file, line int }

// Snapshot is a repository's map at one moment: every file's facts, resolved into who imports whom
// and who mentions what. It is immutable once built, so queries need no lock.
type Snapshot struct {
	files []fileRow
	index map[string]int
	// imports and importedBy are file indexes, sorted.
	imports    [][]int
	importedBy [][]int
	// external are the packages from outside the repository each file imports, sorted.
	external [][]string
	// knowsImports is true for a file whose every import resolved, to a file or to a package — the
	// condition under which a name it mentions without importing its owner is not that owner's.
	knowsImports []bool
	decls        map[string][]declAt
	// mentions are kept only for names some file declares: one per file, at its first line.
	mentions  map[string][]mentionAt
	truncated int
}

func buildSnapshot(files []fileRow, m manifests, truncated int) *Snapshot {
	s := &Snapshot{
		files:        files,
		index:        make(map[string]int, len(files)),
		imports:      make([][]int, len(files)),
		importedBy:   make([][]int, len(files)),
		external:     make([][]string, len(files)),
		knowsImports: make([]bool, len(files)),
		decls:        make(map[string][]declAt),
		mentions:     make(map[string][]mentionAt),
		truncated:    truncated,
	}
	for i, f := range files {
		s.index[f.Path] = i
		for d, decl := range f.Facts.Decls {
			s.decls[decl.Name] = append(s.decls[decl.Name], declAt{file: i, decl: d})
		}
	}

	r := newResolver(files, m)
	for i, f := range files {
		known := f.Facts.Parsed
		targets := make([]int, 0, len(f.Facts.Imports))
		packages := make([]string, 0, 4)
		for _, imp := range f.Facts.Imports {
			res := r.resolve(i, imp.Spec)
			known = known && res.known
			for _, target := range res.files {
				if target != i {
					targets = append(targets, target)
				}
			}
			if res.external != "" {
				packages = append(packages, res.external)
			}
		}
		slices.Sort(targets)
		slices.Sort(packages)
		s.imports[i] = slices.Compact(targets)
		s.external[i] = slices.Compact(packages)
		s.knowsImports[i] = known
	}
	for i, targets := range s.imports {
		for _, target := range targets {
			s.importedBy[target] = append(s.importedBy[target], i)
		}
	}

	for i, f := range files {
		for _, ref := range f.Facts.Refs {
			if _, declared := s.decls[ref.Name]; declared {
				s.mentions[ref.Name] = append(s.mentions[ref.Name], mentionAt{file: i, line: ref.Line})
			}
		}
	}
	return s
}

// decl is the declaration a declAt names.
func (s *Snapshot) decl(at declAt) *Decl {
	return &s.files[at.file].Facts.Decls[at.decl]
}

// imports reports whether file user imports file owner directly.
func (s *Snapshot) importsFile(user, owner int) bool {
	_, found := slices.BinarySearch(s.imports[user], owner)
	return found
}

// samePackage reports whether two files see each other's declarations without an import: Go files
// of one folder and package, Java files of one package.
func (s *Snapshot) samePackage(a, b int) bool {
	fa, fb := s.files[a].Facts, s.files[b].Facts
	if fa.Lang != fb.Lang || fa.Package == "" || fa.Package != fb.Package {
		return false
	}
	switch fa.Lang {
	case LangGo:
		return dirOf(s.files[a].Path) == dirOf(s.files[b].Path)
	case LangJava:
		return true
	}
	return false
}

// sees reports whether a mention in file user can be of a declaration in file owner: the same
// file, an import of it, or the same package.
func (s *Snapshot) sees(user, owner int) bool {
	return user == owner || s.importsFile(user, owner) || s.samePackage(user, owner)
}

// family is the language a file's imports are resolved in: TypeScript and JavaScript import each
// other, every other language only itself.
func family(lang Lang) Lang {
	switch lang {
	case LangTSX, LangJavaScript:
		return LangTypeScript
	}
	return lang
}

// lookup finds a file by its exact path, or by a unique trailing part of one.
func (s *Snapshot) lookup(path string) (int, bool) {
	path = strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"), "/")
	if i, ok := s.index[path]; ok {
		return i, true
	}
	found := -1
	for i, f := range s.files {
		if strings.HasSuffix(f.Path, "/"+path) {
			if found >= 0 {
				return -1, false
			}
			found = i
		}
	}
	return found, found >= 0
}

// enclosing is the innermost declaration of a file whose lines hold line, or nil.
func enclosing(facts *Facts, line int) *Decl {
	var best *Decl
	for i := range facts.Decls {
		d := &facts.Decls[i]
		if d.Line <= line && line <= d.EndLine && (best == nil || d.EndLine-d.Line < best.EndLine-best.Line) {
			best = d
		}
	}
	return best
}
