package archify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Making a model's document one Archify accepts and one that cites only what is there (ARCHIFY-007).

// entityLists are the arrays whose elements carry `sources`, across the diagram types a model draws.
var entityLists = []string{"components", "nodes", "participants", "states", "types", "entities"}

// sequenceStep is how far apart a sequence's messages are drawn, from where the first one sits.
const (
	sequenceFirstY = 160
	sequenceStep   = 40
)

// prepare takes a model's document and makes it ready to validate: its diagram type set, its output
// named, a sequence's messages and a data flow's rows filled in where the model left them out, and
// every citation checked against the repository — a cited
// file must exist inside it, and lines must be inside the file. Citations of a file that differs
// from the pinned commit are dropped, and with no commit pinned there are none at all, since Archify
// checks each one against the commit.
func prepare(document []byte, diagramType, output, root string, repo Repo) ([]byte, error) {
	var doc map[string]any
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoDocument, err)
	}
	doc["diagram_type"] = diagramType
	if _, ok := doc["schema_version"]; !ok {
		doc["schema_version"] = 1
	}
	meta, _ := doc["meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["output"] = output
	delete(meta, "repository")
	doc["meta"] = meta

	switch diagramType {
	case "sequence":
		placeMessages(doc)
	case "dataflow":
		placeRows(doc)
	}

	cited := false
	for _, key := range entityLists {
		entities, ok := doc[key].([]any)
		if !ok {
			continue
		}
		for _, raw := range entities {
			entity, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			sources := groundSources(entity["sources"], root, repo)
			if len(sources) == 0 {
				delete(entity, "sources")
				continue
			}
			entity["sources"] = sources
			cited = true
		}
	}
	if pinned := repo.Pinned(cited); pinned != nil {
		meta["repository"] = pinned
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("writing the document: %w", err)
	}
	return append(out, '\n'), nil
}

// placeMessages gives a `y` to each sequence message that has none, below the one before it. A `y`
// the model gave stays: a segment or an activation may span it.
func placeMessages(doc map[string]any) {
	messages, _ := doc["messages"].([]any)
	previous := sequenceFirstY - sequenceStep
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		y := intOf(message["y"])
		if y < sequenceFirstY {
			y = previous + sequenceStep
			message["y"] = y
		}
		previous = y
	}
}

// placeRows gives a data-flow node with a stage but no row the next free row of its stage.
func placeRows(doc map[string]any) {
	nodes, _ := doc["nodes"].([]any)
	next := map[int]int{}
	for _, raw := range nodes {
		if node, ok := raw.(map[string]any); ok {
			if _, has := node["row"]; has {
				stage := intOf(node["stage"])
				next[stage] = max(next[stage], intOf(node["row"])+1)
			}
		}
	}
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, has := node["row"]; has {
			continue
		}
		if _, has := node["stage"]; !has {
			continue // where it belongs is the model's to say; the repair round asks it
		}
		stage := intOf(node["stage"])
		node["row"] = next[stage]
		next[stage]++
	}
}

// groundSources keeps the citations that name a real, committed file of the repository, with lines
// inside it, at most three.
func groundSources(raw any, root string, repo Repo) []Source {
	if repo.Meta == nil {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	refs := make([]Ref, 0, len(list))
	for _, item := range list {
		var ref Ref
		switch v := item.(type) {
		case string:
			ref.Path = v
		case map[string]any:
			ref.Path, _ = v["path"].(string)
			ref.Line = intOf(v["line"])
			ref.EndLine = intOf(v["end_line"])
		}
		ref.Path = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(ref.Path)), "./")
		if lines, ok := fileLines(root, ref.Path); ok {
			if ref.Line < 1 || ref.Line > lines {
				ref.Line, ref.EndLine = 0, 0
			}
			if ref.EndLine <= ref.Line || ref.EndLine > lines {
				ref.EndLine = 0
			}
			refs = append(refs, ref)
		}
	}
	return repo.Cite(refs)
}

// intOf reads a whole number the decoder kept as a json.Number, or that prepare itself set; anything
// else is 0.
func intOf(v any) int {
	switch n := v.(type) {
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return int(f)
		}
	case int:
		return n
	}
	return 0
}

// maxCitedFileBytes bounds a file whose lines are counted to check a citation.
const maxCitedFileBytes = 8 << 20

// fileLines answers how many lines a repository-relative file has, and false for a path that is not
// a regular file inside root.
func fileLines(root, path string) (int, bool) {
	if path == "" || filepath.IsAbs(path) || strings.HasPrefix(path, "../") || strings.Contains(path, "/../") || path == ".." {
		return 0, false
	}
	full := filepath.Join(root, filepath.FromSlash(path))
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return 0, false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil || !inside(realRoot, resolved) {
		return 0, false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxCitedFileBytes {
		return 0, false
	}
	content, err := os.ReadFile(resolved) //nolint:gosec // G304: confined to the repository above
	if err != nil {
		return 0, false
	}
	lines := bytes.Count(content, []byte("\n"))
	if len(content) > 0 && content[len(content)-1] != '\n' {
		lines++
	}
	return lines, true
}
