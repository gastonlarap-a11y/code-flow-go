package archify

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/repomap"
)

// Topics worth drawing, picked from the map (ARCHIFY-009): what a sequence, a lifecycle, a data
// flow or a workflow of this repository could be about. They are starting points — the user may
// type anything — so they are cheap: names the index already holds, and three files every
// repository keeps its processes in.

// Suggestion is one topic: what the chip says, where it comes from, and what is sent as the topic.
type Suggestion struct {
	Label string `json:"label"`
	Hint  string `json:"hint"`
	Topic string `json:"topic"`
}

// maxSuggestions is how many chips a kind offers.
const maxSuggestions = 12

var (
	// A name that handles requests: a controller, a handler, a router, a resolver, an endpoint.
	entryName = regexp.MustCompile(`(?i)(controller|handler|router|routes?|resolver|endpoint|gateway)s?$`)
	// A name that holds a state: OrderStatus, PaymentState, Phase, Stage.
	stateName = regexp.MustCompile(`(State|Status|Phase|Stage|Step)$`)
	// A name that is data: a model, an entity, a repository of them, a schema.
	dataName = regexp.MustCompile(`(Model|Entity|Repository|Repo|Store|Schema|Record|Dto|DTO)$`)
	// A make target worth naming: `build:`, `deploy:`, `release:`.
	makeTarget = regexp.MustCompile(`(?m)^([A-Za-z][A-Za-z0-9_-]*)\s*:[^=]`)
	// A process worth drawing, by the name of a script or a target.
	processName = regexp.MustCompile(`(?i)^(build|release|deploy|publish|migrate|seed|package|ci|test|e2e|lint|start|dev)`)
)

// Suggestions answers the topics for a kind of diagram; none for an architecture, which is drawn of
// the whole repository.
func Suggestions(snap *repomap.Snapshot, root, kind string) []Suggestion {
	out := make([]Suggestion, 0, maxSuggestions)
	// What a test declares is about the code, not a part of it: `TestRouterRejectsAHandler` is no topic.
	accept := func(match func(repomap.DeclHit) bool) func(repomap.DeclHit) bool {
		return func(h repomap.DeclHit) bool { return !isTestPath(h.Path) && match(h) }
	}
	add := func(hits []repomap.DeclHit, topic string) {
		for _, hit := range hits {
			out = append(out, Suggestion{Label: hit.Name, Hint: fmt.Sprintf("%s:%d", hit.Path, hit.Line), Topic: fmt.Sprintf(topic, hit.Name, hit.Path)})
		}
	}
	switch kind {
	case "sequence":
		add(snap.DeclsMatching(accept(func(h repomap.DeclHit) bool {
			return entryName.MatchString(h.Name) && (h.Kind.IsType() || h.Kind == repomap.KindFunction)
		}), maxSuggestions), "the request flow through %s (%s)")
	case "lifecycle":
		add(snap.DeclsMatching(accept(func(h repomap.DeclHit) bool {
			return stateName.MatchString(h.Name) && (h.Kind.IsType() || h.Kind == repomap.KindType) // a TS union of literals is a type alias
		}), maxSuggestions), "the lifecycle of %s (%s)")
	case "dataflow":
		add(snap.DeclsMatching(accept(func(h repomap.DeclHit) bool {
			return dataName.MatchString(h.Name) && h.Kind.IsType()
		}), maxSuggestions), "how the %s data moves (%s)")
	case "workflow":
		out = append(out, processes(root)...)
	}
	if len(out) > maxSuggestions {
		out = out[:maxSuggestions]
	}
	return out
}

// testPath matches the files each language keeps its tests in, by their name or their folder.
var testPath = regexp.MustCompile(`(_test\.(go|dart|py)|\.(test|spec)\.[cm]?[jt]sx?|Tests?\.java)$|(^|/)(test_[^/]*\.py|__tests__/|tests?/|src/test/)`)

func isTestPath(path string) bool { return testPath.MatchString(path) }

// processes are the repository's own named processes: its package scripts, its CI workflows and its
// make targets.
func processes(root string) []Suggestion {
	out := make([]Suggestion, 0, maxSuggestions)
	if raw, ok := readSmall(filepath.Join(root, "package.json")); ok {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(raw, &pkg) == nil {
			names := make([]string, 0, len(pkg.Scripts))
			for name := range pkg.Scripts {
				if processName.MatchString(name) {
					names = append(names, name)
				}
			}
			slices.Sort(names)
			for _, name := range names {
				out = append(out, Suggestion{Label: "npm run " + name, Hint: "package.json", Topic: fmt.Sprintf("the %q script and what it runs (package.json)", name)})
			}
		}
	}
	if entries, err := os.ReadDir(filepath.Join(root, ".github", "workflows")); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if ext := filepath.Ext(name); entry.Type().IsRegular() && (ext == ".yml" || ext == ".yaml") {
				path := ".github/workflows/" + name
				out = append(out, Suggestion{Label: "CI: " + strings.TrimSuffix(name, ext), Hint: path, Topic: fmt.Sprintf("the CI workflow %s and its jobs", path)})
			}
		}
	}
	for _, file := range []string{"Makefile", "Taskfile.yml", "justfile"} {
		raw, ok := readSmall(filepath.Join(root, file))
		if !ok {
			continue
		}
		seen := map[string]bool{}
		for _, match := range makeTarget.FindAllSubmatch(raw, -1) {
			name := string(match[1])
			if processName.MatchString(name) && !seen[name] {
				seen[name] = true
				out = append(out, Suggestion{Label: name, Hint: file, Topic: fmt.Sprintf("the %q target in %s and what it runs", name, file)})
			}
		}
	}
	return out
}

// readSmall reads a file of the repository's root that is small enough to be configuration.
func readSmall(path string) ([]byte, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<10 {
		return nil, false
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a fixed name at the repository's root
	return raw, err == nil
}
