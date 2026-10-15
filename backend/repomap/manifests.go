package repomap

import (
	"cmp"
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strings"
)

// What a repository's own manifests say about how its imports resolve (REPOMAP-004): which Go
// module a folder is, which name a package is published under, where a tsconfig maps `@/` to.

// manifestNames are the files a listing keeps for the resolver, beside the code.
var manifestNames = map[string]bool{
	"go.mod": true, "package.json": true, "jsconfig.json": true, "pubspec.yaml": true,
	"pyproject.toml": true, "setup.py": true, "setup.cfg": true,
}

// isManifest says whether a repository-relative path is one the resolver reads.
func isManifest(p string) bool {
	name := path.Base(p)
	if manifestNames[name] {
		return true
	}
	return strings.HasPrefix(name, "tsconfig") && strings.HasSuffix(name, ".json")
}

// maxManifestBytes skips a manifest too big to be hand-written configuration.
const maxManifestBytes = 256 << 10

// manifests are the manifest files of one repository, by path.
type manifests map[string][]byte

var (
	goModuleLine   = regexp.MustCompile(`(?m)^\s*module\s+"?([^\s"]+)"?`)
	pubspecName    = regexp.MustCompile(`(?m)^name:\s*["']?([A-Za-z0-9_]+)`)
	jsonComment    = regexp.MustCompile(`(?s)//[^\n]*|/\*.*?\*/`)
	jsonTrailComma = regexp.MustCompile(`,(\s*[}\]])`)
)

// goModule is one go.mod: the import path it declares and the folder it sits in.
type goModule struct {
	path, dir string
}

func (m manifests) goModules() []goModule {
	out := make([]goModule, 0, 2)
	for file, content := range m {
		if path.Base(file) != "go.mod" {
			continue
		}
		if match := goModuleLine.FindSubmatch(content); match != nil {
			out = append(out, goModule{path: string(match[1]), dir: dirOf(file)})
		}
	}
	// The longest module path first, so a nested module wins over the one around it.
	sortByLengthDesc(out, func(g goModule) string { return g.path })
	return out
}

// packageNames maps the name a package.json or pubspec.yaml publishes to the folder it describes —
// how one package of a monorepo imports another by name.
func (m manifests) packageNames(file string) map[string]string {
	out := make(map[string]string)
	for p, content := range m {
		if path.Base(p) != file {
			continue
		}
		switch file {
		case "package.json":
			var pkg struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(content, &pkg) == nil && pkg.Name != "" {
				out[pkg.Name] = dirOf(p)
			}
		case "pubspec.yaml":
			if match := pubspecName.FindSubmatch(content); match != nil {
				out[string(match[1])] = dirOf(p)
			}
		}
	}
	return out
}

// pythonRoots are the folders an absolute Python import is looked up from: the repository, its
// `src`, and every folder holding a Python project's own manifest, with its `src`.
func (m manifests) pythonRoots() []string {
	roots := []string{"", "src"}
	for p := range m {
		switch path.Base(p) {
		case "pyproject.toml", "setup.py", "setup.cfg":
			dir := dirOf(p)
			roots = append(roots, dir, joinPath(dir, "src"))
		}
	}
	return dedupe(roots)
}

// tsConfig is one tsconfig or jsconfig, resolved: the folder its settings cover, the folder bare
// specifiers are looked up from (`baseUrl`), and its path aliases.
type tsConfig struct {
	dir     string
	baseURL string
	hasBase bool
	paths   []tsAlias
}

// tsAlias is one `paths` entry: `@/*` mapped to `["src/*"]`, targets relative to the repository.
type tsAlias struct {
	pattern string
	targets []string
}

type rawTSConfig struct {
	Extends         json.RawMessage `json:"extends"`
	CompilerOptions struct {
		BaseURL *string             `json:"baseUrl"`
		Paths   map[string][]string `json:"paths"`
	} `json:"compilerOptions"`
}

// tsConfigs reads every tsconfig and jsconfig, following `extends` inside the repository.
func (m manifests) tsConfigs() []tsConfig {
	out := make([]tsConfig, 0, 4)
	for p := range m {
		// Only the config a folder's own tooling reads covers it; tsconfig.build.json and the like
		// are read when another config extends them.
		if name := path.Base(p); name != "tsconfig.json" && name != "jsconfig.json" {
			continue
		}
		if config, ok := m.tsConfig(p, 0); ok {
			out = append(out, config)
		}
	}
	// The deepest folder first: a file is covered by the nearest config above it.
	sortByLengthDesc(out, func(c tsConfig) string { return c.dir })
	return out
}

// maxExtends bounds a chain of `extends`, and breaks a cycle.
const maxExtends = 8

func (m manifests) tsConfig(p string, depth int) (tsConfig, bool) {
	content, ok := m[p]
	if !ok || depth > maxExtends {
		return tsConfig{}, false
	}
	var raw rawTSConfig
	if json.Unmarshal(lenientJSON(content), &raw) != nil {
		return tsConfig{}, false
	}
	dir := dirOf(p)
	config := tsConfig{dir: dir}

	// What the config extends comes first; its own settings override.
	var extends string
	if json.Unmarshal(raw.Extends, &extends) == nil && strings.HasPrefix(extends, ".") {
		target := joinPath(dir, extends)
		if !strings.HasSuffix(target, ".json") {
			target += ".json"
		}
		if parent, ok := m.tsConfig(target, depth+1); ok {
			config.baseURL, config.hasBase, config.paths = parent.baseURL, parent.hasBase, parent.paths
		}
	}
	if raw.CompilerOptions.BaseURL != nil {
		config.baseURL, config.hasBase = joinPath(dir, *raw.CompilerOptions.BaseURL), true
	}
	if len(raw.CompilerOptions.Paths) > 0 {
		// Paths are relative to baseUrl when there is one, else to the config's own folder.
		from := dir
		if config.hasBase {
			from = config.baseURL
		}
		config.paths = make([]tsAlias, 0, len(raw.CompilerOptions.Paths))
		for pattern, targets := range raw.CompilerOptions.Paths {
			alias := tsAlias{pattern: pattern, targets: make([]string, 0, len(targets))}
			for _, target := range targets {
				alias.targets = append(alias.targets, joinPath(from, target))
			}
			config.paths = append(config.paths, alias)
		}
		// The longest pattern first, so `@/lib/*` wins over `@/*`.
		sortByLengthDesc(config.paths, func(a tsAlias) string { return a.pattern })
	}
	return config, true
}

// lenientJSON is a tsconfig turned into JSON: TypeScript accepts comments and trailing commas.
// Comments are removed only outside strings, which is why the string literals are stepped over.
func lenientJSON(content []byte) []byte {
	var out strings.Builder
	text := string(content)
	for len(text) > 0 {
		quote := strings.IndexByte(text, '"')
		if quote < 0 {
			out.WriteString(jsonComment.ReplaceAllString(text, ""))
			break
		}
		out.WriteString(jsonComment.ReplaceAllString(text[:quote], ""))
		end := quote + 1
		for end < len(text) && text[end] != '"' {
			if text[end] == '\\' {
				end++
			}
			end++
		}
		if end >= len(text) {
			out.WriteString(text[quote:])
			break
		}
		out.WriteString(text[quote : end+1])
		text = text[end+1:]
	}
	return []byte(jsonTrailComma.ReplaceAllString(out.String(), "$1"))
}

// dirOf is a repository-relative path's folder, "" at the root.
func dirOf(p string) string {
	dir, _, _ := strings.CutLast(p, "/")
	if dir == p {
		return ""
	}
	return dir
}

// joinPath joins repository-relative parts and cleans the result; "" is the root, and a path that
// climbs above the root answers "..".
func joinPath(parts ...string) string {
	joined := path.Clean(path.Join(parts...))
	if joined == "." {
		return ""
	}
	return joined
}

func dedupe(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := values[:0]
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// sortByLengthDesc orders items by their key, longest first, then alphabetically — so the most
// specific match is tried first, the same way every time.
func sortByLengthDesc[T any](items []T, key func(T) string) {
	slices.SortFunc(items, func(a, b T) int {
		ka, kb := key(a), key(b)
		return cmp.Or(cmp.Compare(len(kb), len(ka)), cmp.Compare(ka, kb))
	})
}
