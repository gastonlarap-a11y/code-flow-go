package repomap

import (
	"cmp"
	"path"
	"slices"
	"strings"
)

// The two drawn views of a map (REPOMAP-005): one folder's contents, and the whole project folded
// until it fits on a screen.

// ProjectNodes bounds the project view. Past it, files are grouped into folders cut at the deepest
// level that still fits — enough cards to see the shape of a repository, few enough to read them.
const ProjectNodes = 120

// FolderGraph is focus's direct contents: each subfolder as one node holding everything below it,
// and each file directly inside. A focus holding nothing but one subfolder opens that subfolder
// instead, as many times as it takes, since a card alone on the screen says nothing.
func (s *Snapshot) FolderGraph(focus string) MapGraph {
	focus = strings.Trim(strings.ReplaceAll(focus, "\\", "/"), "/")
	for {
		only, alone := s.onlySubfolder(focus)
		if !alone {
			break
		}
		focus = only
	}
	return s.graph(focus, func(p string) (string, bool) {
		rest := p
		if focus != "" {
			inside, ok := strings.CutPrefix(p, focus+"/")
			if !ok {
				return "", false
			}
			rest = inside
		}
		if child, _, nested := strings.Cut(rest, "/"); nested {
			return joinPath(focus, child), true
		}
		return p, true
	})
}

// onlySubfolder answers focus's one subfolder when it holds no file of its own and exactly one.
func (s *Snapshot) onlySubfolder(focus string) (string, bool) {
	only := ""
	for _, f := range s.files {
		rest := f.Path
		if focus != "" {
			inside, ok := strings.CutPrefix(f.Path, focus+"/")
			if !ok {
				continue
			}
			rest = inside
		}
		child, _, nested := strings.Cut(rest, "/")
		if !nested {
			return "", false // a file of its own
		}
		if only != "" && only != child {
			return "", false
		}
		only = child
	}
	return joinPath(focus, only), only != ""
}

// ProjectGraph is the whole repository: every file when it fits in maxNodes, else every file folded
// into its folder cut at the deepest level that fits. Files at the root are always their own node.
func (s *Snapshot) ProjectGraph(maxNodes int) MapGraph {
	if len(s.files) <= maxNodes {
		return s.graph("", func(p string) (string, bool) { return p, true })
	}
	deepest := 1
	for _, f := range s.files {
		deepest = max(deepest, strings.Count(f.Path, "/"))
	}
	depth := 1
	for d := deepest; d >= 1; d-- {
		if s.countNodes(d) <= maxNodes {
			depth = d
			break
		}
	}
	return s.graph("", func(p string) (string, bool) { return foldedAt(p, depth), true })
}

// foldedAt is the node a file belongs to when folders are cut depth levels deep.
func foldedAt(p string, depth int) string {
	dir := dirOf(p)
	if dir == "" {
		return p
	}
	parts := strings.Split(dir, "/")
	return strings.Join(parts[:min(depth, len(parts))], "/")
}

func (s *Snapshot) countNodes(depth int) int {
	seen := make(map[string]bool)
	for _, f := range s.files {
		seen[foldedAt(f.Path, depth)] = true
	}
	return len(seen)
}

// graph aggregates the files into nodes by nodeOf, which answers false for a file outside the view.
func (s *Snapshot) graph(focus string, nodeOf func(path string) (string, bool)) MapGraph {
	type tally struct {
		node      MapNode
		languages map[string]int
		importers map[int]bool
	}
	keys := make([]string, len(s.files))
	nodes := make(map[string]*tally)
	for i, f := range s.files {
		key, ok := nodeOf(f.Path)
		if !ok {
			continue
		}
		keys[i] = key
		t := nodes[key]
		if t == nil {
			t = &tally{
				node:      MapNode{ID: key, Label: path.Base(key), Folder: key != f.Path, Group: groupOf(key, key != f.Path)},
				languages: make(map[string]int),
				importers: make(map[int]bool),
			}
			nodes[key] = t
		}
		t.node.Files++
		t.node.Declarations += len(f.Facts.Decls)
		t.node.Lines += f.Facts.Lines
		t.languages[f.Facts.Lang.Name()]++
	}

	weights := make(map[[2]string]int)
	for i, targets := range s.imports {
		for _, target := range targets {
			to := keys[target]
			if to == "" || to == keys[i] {
				continue
			}
			// A file outside the view still counts as importing what it imports.
			nodes[to].importers[i] = true
			if keys[i] != "" {
				weights[[2]string{keys[i], to}]++
			}
		}
	}

	graph := MapGraph{
		Focus:        focus,
		Nodes:        make([]MapNode, 0, len(nodes)),
		Edges:        make([]MapEdge, 0, len(weights)),
		Files:        len(s.files),
		Declarations: s.declarationCount(),
		Languages:    s.languages(),
	}
	for _, t := range nodes {
		t.node.Language = majority(t.languages)
		t.node.ImportedBy = len(t.importers)
		graph.Nodes = append(graph.Nodes, t.node)
	}
	slices.SortFunc(graph.Nodes, func(a, b MapNode) int { return cmp.Compare(a.ID, b.ID) })
	for pair, weight := range weights {
		graph.Edges = append(graph.Edges, MapEdge{From: pair[0], To: pair[1], Weight: weight})
	}
	slices.SortFunc(graph.Edges, func(a, b MapEdge) int { return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To)) })
	return graph
}

// groupOf is the top-level folder a node belongs to, "" for a file at the root.
func groupOf(id string, folder bool) string {
	top, _, nested := strings.Cut(id, "/")
	if nested || folder {
		return top
	}
	return ""
}

// majority is the language most files have, the first by name on a tie.
func majority(counts map[string]int) string {
	best, bestCount := "", 0
	for name, count := range counts {
		if count > bestCount || (count == bestCount && name < best) {
			best, bestCount = name, count
		}
	}
	return best
}

func (s *Snapshot) declarationCount() int {
	n := 0
	for _, f := range s.files {
		n += len(f.Facts.Decls)
	}
	return n
}

// languages counts the map's files by language, the most common first.
func (s *Snapshot) languages() []LanguageCount {
	counts := make(map[string]int)
	for _, f := range s.files {
		counts[f.Facts.Lang.Name()]++
	}
	out := make([]LanguageCount, 0, len(counts))
	for name, files := range counts {
		out = append(out, LanguageCount{Language: name, Files: files})
	}
	slices.SortFunc(out, func(a, b LanguageCount) int {
		return cmp.Or(cmp.Compare(b.Files, a.Files), cmp.Compare(a.Language, b.Language))
	})
	return out
}
