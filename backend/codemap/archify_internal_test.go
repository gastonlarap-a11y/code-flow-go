package codemap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/archify"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

func step(id, name, kind, lang, file, via string, children ...FlowNode) FlowNode {
	return FlowNode{
		Symbol:   SymbolRef{ID: id, Name: name, Kind: kind, Lang: lang, File: file, Line: 3},
		Via:      via,
		Children: append([]FlowNode{}, children...),
	}
}

// A component saves through an IPC wrapper, an endpoint carries the call into Go, and Go writes a
// table and reads another.
func sampleFlow() Flow {
	return Flow{Root: step("ts:web/List.tsx#List", "List", "component", "ts", "web/List.tsx", "",
		step("ts:web/ipc.ts#save", "save", "func", "ts", "web/ipc.ts", "call",
			step("ep:item_save", "item_save", "endpoint", "go", "backend/items/commands.go", "cross",
				step("go:app/items.Register$item_save", `items.Register("item_save")`, "func", "go", "backend/items/commands.go", "dispatch",
					step("go:app/items.Save", "items.Save", "func", "go", "backend/items/save.go", "call",
						step("sql:items", "items", "table", "sql", "db/001.sql", "write"),
						step("sql:prices", "prices", "table", "sql", "db/001.sql", "read"),
					),
				),
			),
		),
	)}
}

func TestASequenceHasAnArrowEachTimeTheFlowChangesLane(t *testing.T) {
	doc, err := archifySequenceOf(sampleFlow(), archify.Repo{}, "list-flow.html", "en")
	require.NoError(t, err)

	labels := make([]string, 0, len(doc.Participants))
	for _, p := range doc.Participants {
		labels = append(labels, p.Label+"/"+p.Type)
	}
	assert.Equal(t, []string{"List/frontend", "ipc/frontend", "items/backend", "Database/database"}, labels)

	messages := make([]string, 0, len(doc.Messages))
	for _, m := range doc.Messages {
		messages = append(messages, m.From+">"+m.To+" "+m.Label+" "+m.Variant)
	}
	assert.Equal(t, []string{
		"ts-web-List-tsx>ts-web-ipc-ts save() ",
		"ts-web-ipc-ts>go-backend-items item_save emphasis",
		"go-backend-items>db write items ",
		"go-backend-items>db read prices dashed",
	}, messages)
	assert.Equal(t, 170.0, doc.Messages[0].Y)
	assert.Equal(t, 170.0+3*28, doc.Messages[3].Y)
	assert.Nil(t, doc.Meta.Repository, "no citation, no pinned repository: Archify rejects one with no sources")
	assert.Equal(t, "Flow of List", doc.Meta.Title)
}

func TestASequenceStopsAtSevenLifelinesAndSaysSo(t *testing.T) {
	root := step("ts:a.ts#a", "a", "func", "ts", "a.ts", "")
	for i := range 9 {
		file := "f" + string(rune('a'+i)) + ".ts"
		root.Children = append(root.Children, step("ts:"+file+"#f", "f", "func", "ts", file, "call"))
	}
	doc, err := archifySequenceOf(Flow{Root: root}, archify.Repo{}, "a.html", "es")
	require.NoError(t, err)

	assert.Len(t, doc.Participants, maxParticipants)
	require.Len(t, doc.Cards, 1)
	assert.Contains(t, doc.Cards[0].Items[0], "3 pasos más no caben")
}

func TestAFlowInsideOneFileHasNoSequence(t *testing.T) {
	flow := Flow{Root: step("ts:a.ts#a", "a", "func", "ts", "a.ts", "", step("ts:a.ts#b", "b", "func", "ts", "a.ts", "call"))}
	_, err := archifySequenceOf(flow, archify.Repo{}, "a.html", "en")
	require.ErrorIs(t, err, ErrNoSequence)
}

func TestTheArchitectureCountsWhatJoinsTheAreas(t *testing.T) {
	g := NewGraph()
	for _, n := range []*Node{
		{ID: "ts:web/lib/api/x.ts#call", Name: "call", Lang: LangTS, Kind: KindFunc, Pos: Pos{File: "web/lib/api/x.ts", Line: 1}},
		{ID: "ts:web/components/api/y.tsx#Y", Name: "Y", Lang: LangTS, Kind: KindComponent, Pos: Pos{File: "web/components/api/y.tsx", Line: 1}},
		{ID: "ep:item_save", Name: "item_save", Lang: LangGo, Kind: KindEndpoint, Pos: Pos{File: "backend/items/commands.go", Line: 4}},
		{ID: "go:app/items.handler", Name: "items.handler", Lang: LangGo, Kind: KindFunc, Pos: Pos{File: "backend/items/commands.go", Line: 5}},
		{ID: "sql:items", Name: "items", Lang: LangSQL, Kind: KindTable, Pos: Pos{File: "db/001.sql", Line: 1}},
	} {
		g.AddNode(n)
	}
	g.AddEdge(Edge{From: "ts:web/lib/api/x.ts#call", To: "ep:item_save", Kind: EdgeCross})
	g.AddEdge(Edge{From: "ep:item_save", To: "go:app/items.handler", Kind: EdgeDispatch})
	g.AddEdge(Edge{From: "go:app/items.handler", To: "sql:items", Kind: EdgeWrite})
	g.Finish()

	doc := archifyArchitectureOf(g, g.Overview(Status{}), archify.Repo{}, "architecture.html", "en")

	labels := map[string]string{}
	for _, c := range doc.Components {
		labels[c.ID] = c.Label
	}
	assert.ElementsMatch(t, []string{"lib/api", "components/api", "items", "Database"}, mapValues(labels),
		"two folders both called api are told apart")
	connections := make([]string, 0, len(doc.Connections))
	for _, c := range doc.Connections {
		connections = append(connections, labels[c.From]+">"+labels[c.To]+" "+c.Label)
	}
	assert.ElementsMatch(t, []string{"lib/api>items 1 calls", "items>Database writes 1"}, connections)
	assert.Len(t, doc.Boundaries, 2)

	// Cited by the most used symbols, the same every time the same map is drawn.
	repo := archify.Repo{Meta: &archify.Repository{URL: "https://github.com/acme/app", Revision: strings.Repeat("a", 40)}}
	first, err := json.Marshal(archifyArchitectureOf(g, g.Overview(Status{}), repo, "architecture.html", "en"))
	require.NoError(t, err)
	for range 20 {
		again, err := json.Marshal(archifyArchitectureOf(g, g.Overview(Status{}), repo, "architecture.html", "en"))
		require.NoError(t, err)
		require.JSONEq(t, string(first), string(again))
	}
	var cited archifyArchitectureDoc
	require.NoError(t, json.Unmarshal(first, &cited))
	for _, c := range cited.Components {
		if c.Label == "items" {
			// One caller each: the tie goes to the ID, the same way every time.
			assert.Equal(t, []archify.Source{{Path: "backend/items/commands.go", Line: 4}, {Path: "backend/items/commands.go", Line: 5}}, c.Sources)
		}
	}
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// A symbol's file, start and end become an Archify citation; the rules about which ones are kept
// are package archify's, tested there.
func TestASymbolIsCitedByItsFileAndLines(t *testing.T) {
	repo := archify.Repo{Meta: &archify.Repository{URL: "https://github.com/acme/app", Revision: strings.Repeat("a", 40)}}
	assert.Equal(t, []archify.Source{{Path: "web/a.ts", Line: 2, EndLine: 9}},
		cite(repo, []SymbolRef{{File: "web/a.ts", Line: 2, EndLine: 9}}))
	assert.Nil(t, cite(archify.Repo{}, []SymbolRef{{File: "a.ts", Line: 1}}), "no repository, no citation")
}

// With a real Archify (ARCHIFY_BIN) and node, the fixture repository renders both diagrams.
func TestARealArchifyRendersBothDiagrams(t *testing.T) {
	script := os.Getenv("ARCHIFY_BIN")
	tools := DiscoverTools()
	if script == "" || tools.Node == "" || tools.Go == "" {
		t.Skip("needs ARCHIFY_BIN, node and go")
	}
	root := t.TempDir()
	src, err := filepath.Abs(filepath.Join("testdata", "repo"))
	require.NoError(t, err)
	require.NoError(t, copyTree(src, root))

	service := NewService(nil, nil)
	out := t.TempDir()
	architecture, err := service.Archify(t.Context(), out, nil, ArchifyRequest{RootPath: root, Language: "en"})
	require.NoError(t, err)
	assert.FileExists(t, architecture.HTMLPath)

	sequence, err := service.Archify(t.Context(), out, nil, ArchifyRequest{RootPath: root, Symbol: "shop.Register", Language: "es"})
	require.NoError(t, err)
	assert.Equal(t, ArchifySequence, sequence.DiagramType)
	assert.FileExists(t, sequence.HTMLPath)

	// Committed, with an origin: the diagrams cite file:line, and Archify checks every citation
	// against the commit before it renders.
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture"},
		{"remote", "add", "origin", "https://github.com/acme/shop.git"},
	} {
		cmd := proc.Command(t.Context(), "git", append([]string{"-C", root}, args...)...)
		cmd.Env = proc.Environment(tools.Path)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	service = NewService(nil, nil)
	cited, err := service.Archify(t.Context(), out, nil, ArchifyRequest{RootPath: root, Language: "en"})
	require.NoError(t, err)
	raw, err := os.ReadFile(strings.TrimSuffix(cited.HTMLPath, ".html") + ".json")
	require.NoError(t, err)
	var document archifyArchitectureDoc
	require.NoError(t, json.Unmarshal(raw, &document))
	require.NotNil(t, document.Meta.Repository)
	assert.Equal(t, "local-only", document.Meta.Repository.LinkMode, "the commit is on no remote branch")
	assert.Equal(t, "https://github.com/acme/shop.git", document.Meta.Repository.URL)
	assert.NotEmpty(t, document.Components[0].Sources)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}
