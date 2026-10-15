package repomap

import (
	"path"
	"strings"
)

// From an import as written to the files it names (REPOMAP-004).
//
// Each language answers with its own rules and the repository's own manifests, rather than by
// matching names: a TypeScript alias is read from the tsconfig that declares it, a Go import from
// the go.mod whose module it falls under, a Dart `package:` import from the pubspec that publishes
// the name. What does not resolve to a file is either a package from outside the repository —
// recorded under its name — or unknown, which is what keeps a use of a same-named declaration from
// being ruled out (REPOMAP-006).

// resolution is what one import named.
type resolution struct {
	files    []int
	external string // the package outside the repository, when that is what it named
	known    bool   // false when the import should be in the repository and could not be found
}

func local(files []int) resolution   { return resolution{files: files, known: len(files) > 0} }
func outside(name string) resolution { return resolution{external: name, known: true} }
func nothingToFollow() resolution    { return resolution{known: true} }

var unresolved = resolution{known: false}

type resolver struct {
	files []fileRow
	index map[string]int
	byDir map[string][]int

	goModules    []goModule
	tsConfigs    []tsConfig
	jsPackages   map[string]string
	dartPackages map[string]string
	pythonRoots  []string
	javaPackages map[string][]int
	topFolders   map[string]bool
}

func newResolver(files []fileRow, m manifests) *resolver {
	r := &resolver{
		files:        files,
		index:        make(map[string]int, len(files)),
		byDir:        make(map[string][]int),
		goModules:    m.goModules(),
		tsConfigs:    m.tsConfigs(),
		jsPackages:   m.packageNames("package.json"),
		dartPackages: m.packageNames("pubspec.yaml"),
		pythonRoots:  m.pythonRoots(),
		javaPackages: make(map[string][]int),
		topFolders:   make(map[string]bool),
	}
	for i, f := range files {
		r.index[f.Path] = i
		dir := dirOf(f.Path)
		r.byDir[dir] = append(r.byDir[dir], i)
		if top, _, found := strings.Cut(f.Path, "/"); found {
			r.topFolders[top] = true
		}
		if f.Facts.Lang == LangJava && f.Facts.Package != "" {
			r.javaPackages[f.Facts.Package] = append(r.javaPackages[f.Facts.Package], i)
		}
	}
	return r
}

// resolve answers what file `from` names with spec.
func (r *resolver) resolve(from int, spec string) resolution {
	file := r.files[from]
	switch file.Facts.Lang {
	case LangTypeScript, LangTSX, LangJavaScript:
		return r.resolveJS(file.Path, spec)
	case LangGo:
		return r.resolveGo(spec)
	case LangPython:
		return r.resolvePython(file.Path, spec)
	case LangJava:
		return r.resolveJava(spec)
	case LangDart:
		return r.resolveDart(file.Path, spec)
	}
	return unresolved
}

// ---- TypeScript and JavaScript ------------------------------------------------------------------

// jsExtensions are tried, in order, after a specifier that names no file of its own.
var jsExtensions = []string{".ts", ".tsx", ".d.ts", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts"}

// assetExtensions are imports of something that is not code — a stylesheet, an image — which have
// nothing to follow.
var assetExtensions = map[string]bool{
	".css": true, ".scss": true, ".sass": true, ".less": true, ".svg": true, ".png": true, ".jpg": true,
	".jpeg": true, ".gif": true, ".webp": true, ".ico": true, ".json": true, ".md": true, ".html": true,
	".wasm": true, ".woff": true, ".woff2": true, ".ttf": true, ".txt": true, ".yaml": true, ".yml": true,
}

func (r *resolver) resolveJS(from, spec string) resolution {
	spec, _, _ = strings.Cut(spec, "?")
	if assetExtensions[strings.ToLower(path.Ext(spec))] || strings.Contains(spec, "?raw") {
		return nothingToFollow()
	}
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || spec == "." || spec == ".." {
		if found := r.probeJS(joinPath(dirOf(from), spec)); found >= 0 {
			return local([]int{found})
		}
		return unresolved
	}

	if config, ok := r.tsConfigFor(from); ok {
		for _, alias := range config.paths {
			for _, target := range aliasTargets(alias, spec) {
				if found := r.probeJS(target); found >= 0 {
					return local([]int{found})
				}
			}
		}
		if config.hasBase {
			if found := r.probeJS(joinPath(config.baseURL, spec)); found >= 0 {
				return local([]int{found})
			}
		}
	}

	name, rest := packageOf(spec)
	if dir, ok := r.jsPackages[name]; ok {
		for _, candidate := range []string{joinPath(dir, rest), joinPath(dir, "src", rest)} {
			if rest == "" {
				candidate = joinPath(candidate, "index")
			}
			if found := r.probeJS(candidate); found >= 0 {
				return local([]int{found})
			}
		}
		return unresolved
	}
	return outside(name)
}

// tsConfigFor is the nearest tsconfig or jsconfig above a file.
func (r *resolver) tsConfigFor(file string) (tsConfig, bool) {
	for _, config := range r.tsConfigs {
		if config.dir == "" || strings.HasPrefix(file, config.dir+"/") {
			return config, true
		}
	}
	return tsConfig{}, false
}

// aliasTargets is what a `paths` entry turns spec into, or nothing when the pattern does not match.
func aliasTargets(alias tsAlias, spec string) []string {
	prefix, suffix, wildcard := strings.Cut(alias.pattern, "*")
	if !wildcard {
		if spec != alias.pattern {
			return nil
		}
		return alias.targets
	}
	if !strings.HasPrefix(spec, prefix) || !strings.HasSuffix(spec, suffix) || len(spec) < len(prefix)+len(suffix) {
		return nil
	}
	middle := spec[len(prefix) : len(spec)-len(suffix)]
	out := make([]string, 0, len(alias.targets))
	for _, target := range alias.targets {
		out = append(out, strings.Replace(target, "*", middle, 1))
	}
	return out
}

// packageOf splits a bare specifier into its package name — `react`, `@scope/name` — and the path
// inside it.
func packageOf(spec string) (name, rest string) {
	parts := strings.SplitN(spec, "/", 3)
	if strings.HasPrefix(spec, "@") && len(parts) >= 2 {
		name = parts[0] + "/" + parts[1]
		if len(parts) == 3 {
			rest = parts[2]
		}
		return name, rest
	}
	name, rest, _ = strings.Cut(spec, "/")
	return name, rest
}

// probeJS finds the file a specifier without its extension names, or -1.
func (r *resolver) probeJS(base string) int {
	if base == ".." || strings.HasPrefix(base, "../") {
		return -1
	}
	if i, ok := r.index[base]; ok {
		return i
	}
	// ESM TypeScript writes `./x.js` for the source `./x.ts`.
	stem := base
	switch ext := path.Ext(base); ext {
	case ".js", ".jsx", ".mjs", ".cjs":
		stem = strings.TrimSuffix(base, ext)
	}
	for _, candidate := range []string{stem, joinPath(base, "index")} {
		for _, ext := range jsExtensions {
			if i, ok := r.index[candidate+ext]; ok {
				return i
			}
		}
	}
	return -1
}

// ---- Go ------------------------------------------------------------------------------------------

func (r *resolver) resolveGo(spec string) resolution {
	for _, module := range r.goModules {
		if spec != module.path && !strings.HasPrefix(spec, module.path+"/") {
			continue
		}
		dir := joinPath(module.dir, strings.TrimPrefix(strings.TrimPrefix(spec, module.path), "/"))
		files := make([]int, 0, 4)
		for _, i := range r.byDir[dir] {
			f := r.files[i]
			if f.Facts.Lang == LangGo && !strings.HasSuffix(f.Path, "_test.go") {
				files = append(files, i)
			}
		}
		if len(files) == 0 {
			return unresolved
		}
		return local(files)
	}
	// The standard library has no dot in its first element; anything else is a module from
	// elsewhere, named by the path up to its repository.
	first, _, _ := strings.Cut(spec, "/")
	if !strings.Contains(first, ".") {
		return outside(spec)
	}
	parts := strings.SplitN(spec, "/", 4)
	return outside(strings.Join(parts[:min(3, len(parts))], "/"))
}

// ---- Python --------------------------------------------------------------------------------------

func (r *resolver) resolvePython(from, spec string) resolution {
	if strings.HasPrefix(spec, ".") {
		dots := len(spec) - len(strings.TrimLeft(spec, "."))
		base := dirOf(from)
		for range dots - 1 {
			base = dirOf(base)
		}
		module := strings.TrimLeft(spec, ".")
		if found := r.probePython(joinPath(base, strings.ReplaceAll(module, ".", "/"))); found >= 0 {
			return local([]int{found})
		}
		return unresolved
	}
	relative := strings.ReplaceAll(spec, ".", "/")
	for _, root := range r.pythonRoots {
		// `import a.b.c` may name a module, or `from a.b import c` a name inside one: shorten from
		// the right until a module answers.
		for candidate := relative; candidate != ""; candidate = dirOf(candidate) {
			if found := r.probePython(joinPath(root, candidate)); found >= 0 {
				return local([]int{found})
			}
		}
	}
	// A first element that is one of the repository's own folders is a local module that could not
	// be found; anything else is an installed package.
	first, _, _ := strings.Cut(spec, ".")
	if r.topFolders[first] || r.probePython(joinPath("src", first)) >= 0 {
		return unresolved
	}
	return outside(first)
}

// probePython finds a module's file — `a/b.py`, or the package's `a/b/__init__.py` — or -1.
func (r *resolver) probePython(base string) int {
	if base == "" || base == ".." || strings.HasPrefix(base, "../") {
		return -1
	}
	for _, candidate := range []string{base + ".py", base + ".pyi", joinPath(base, "__init__.py")} {
		if i, ok := r.index[candidate]; ok {
			return i
		}
	}
	return -1
}

// ---- Java ----------------------------------------------------------------------------------------

func (r *resolver) resolveJava(spec string) resolution {
	spec = strings.TrimPrefix(spec, "static ")
	if pkg, found := strings.CutSuffix(spec, ".*"); found {
		if files := r.javaPackages[pkg]; len(files) > 0 {
			return local(files)
		}
		// `import static a.b.Type.*` imports a type's members.
		if i := r.javaType(pkg); i >= 0 {
			return local([]int{i})
		}
		return r.javaOutside(pkg)
	}
	// `a.b.Type`, or a static member `a.b.Type.member`.
	for candidate := spec; strings.Contains(candidate, "."); {
		if i := r.javaType(candidate); i >= 0 {
			return local([]int{i})
		}
		candidate, _, _ = strings.CutLast(candidate, ".")
	}
	return r.javaOutside(spec)
}

// javaType finds the file of a top-level type by its qualified name, which Java requires to be the
// file's own name in its package.
func (r *resolver) javaType(qualified string) int {
	pkg, name, found := strings.CutLast(qualified, ".")
	if !found {
		return -1
	}
	for _, i := range r.javaPackages[pkg] {
		if strings.TrimSuffix(path.Base(r.files[i].Path), ".java") == name {
			return i
		}
	}
	return -1
}

// javaOutside reads an unresolved import: under one of the repository's own package roots it is a
// missing local type; otherwise a library, named by its first two elements.
func (r *resolver) javaOutside(spec string) resolution {
	parts := strings.Split(spec, ".")
	root := strings.Join(parts[:min(2, len(parts))], ".")
	for pkg := range r.javaPackages {
		if pkg == root || strings.HasPrefix(pkg, root+".") {
			return unresolved
		}
	}
	return outside(root)
}

// ---- Dart ----------------------------------------------------------------------------------------

func (r *resolver) resolveDart(from, spec string) resolution {
	if strings.HasPrefix(spec, "dart:") {
		return outside(spec)
	}
	if rest, found := strings.CutPrefix(spec, "package:"); found {
		name, inside, _ := strings.Cut(rest, "/")
		dir, ours := r.dartPackages[name]
		if !ours {
			return outside(name)
		}
		if i, ok := r.index[joinPath(dir, "lib", inside)]; ok {
			return local([]int{i})
		}
		return unresolved
	}
	if i, ok := r.index[joinPath(dirOf(from), spec)]; ok {
		return local([]int{i})
	}
	return unresolved
}
