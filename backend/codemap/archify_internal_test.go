package codemap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	doc, err := archifySequenceOf(sampleFlow(), archifyRepo{}, "list-flow.html", "en")
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
	doc, err := archifySequenceOf(Flow{Root: root}, archifyRepo{}, "a.html", "es")
	require.NoError(t, err)

	assert.Len(t, doc.Participants, maxParticipants)
	require.Len(t, doc.Cards, 1)
	assert.Contains(t, doc.Cards[0].Items[0], "3 pasos más no caben")
}

func TestAFlowInsideOneFileHasNoSequence(t *testing.T) {
	flow := Flow{Root: step("ts:a.ts#a", "a", "func", "ts", "a.ts", "", step("ts:a.ts#b", "b", "func", "ts", "a.ts", "call"))}
	_, err := archifySequenceOf(flow, archifyRepo{}, "a.html", "en")
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

	doc := archifyArchitectureOf(g, g.Overview(Status{}), archifyRepo{}, "architecture.html", "en")

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
	repo := archifyRepo{meta: &archifyRepository{URL: "https://github.com/acme/app", Revision: strings.Repeat("a", 40)}}
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
			assert.Equal(t, []archifySource{{Path: "backend/items/commands.go", Line: 4}, {Path: "backend/items/commands.go", Line: 5}}, c.Sources)
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

func TestCitationsSkipWhatIsNotCommitted(t *testing.T) {
	repo := archifyRepo{
		meta:  &archifyRepository{URL: "https://github.com/acme/app", Provider: "github", LinkMode: "web", Revision: strings.Repeat("a", 40)},
		dirty: map[string]bool{"web/dirty.ts": true},
	}
	cited := repo.cite([]SymbolRef{
		{File: "web/dirty.ts", Line: 1},
		{File: "web/a.ts", Line: 2, EndLine: 9},
		{File: "web/a.ts", Line: 2},
		{File: "web/b.ts", Line: 3}, {File: "web/c.ts", Line: 4}, {File: "web/d.ts", Line: 5},
	})
	assert.Equal(t, []archifySource{{Path: "web/a.ts", Line: 2, EndLine: 9}, {Path: "web/b.ts", Line: 3}, {Path: "web/c.ts", Line: 4}}, cited)
	assert.Nil(t, repo.pinned(false))
	assert.Nil(t, archifyRepo{}.cite([]SymbolRef{{File: "a.ts", Line: 1}}), "no repository, no citation")
}

// Archify's own rules: strip HTTP(S) userinfo and keep the rest; a supported host's SSH origin may
// be written as the HTTPS address it matches; never rewrite another SSH origin; a folder is no
// repository address at all.
func TestArchifyRemote(t *testing.T) {
	tests := []struct {
		origin, address, provider string
		ok                        bool
	}{
		{"git@github.com:acme/app.git", "https://github.com/acme/app", "github", true},
		{"https://token:x-oauth@github.com/acme/app.git", "https://github.com/acme/app.git", "github", true},
		{"https://user@dev.azure.com/org/p/_git/r", "https://dev.azure.com/org/p/_git/r", "", true},
		{"git@git.internal:team/app.git", "git@git.internal:team/app.git", "", true},
		{"ssh://git@git.internal:2222/team/app.git", "ssh://git@git.internal:2222/team/app.git", "", true},
		{"http://git.internal:8080/app", "http://git.internal:8080/app", "", true},
		{"/local/path", "", "", false},
		{"file:///local/path", "", "", false},
		{"../sibling", "", "", false},
	}
	for _, tt := range tests {
		address, provider, ok := archifyRemote(tt.origin)
		assert.Equal(t, tt.ok, ok, tt.origin)
		assert.Equal(t, tt.address, address, tt.origin)
		assert.Equal(t, tt.provider, provider, tt.origin)
	}
}

// fakeArchify lays out an install the way `npx skills add` does.
func fakeArchify(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o750))
	script := filepath.Join(dir, "bin", "archify.mjs")
	require.NoError(t, os.WriteFile(script, []byte("// archify"), 0o600))
	manifest, err := json.Marshal(map[string]string{"version": version})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), manifest, 0o600))
	return script
}

func TestOnlyASupportedArchifyIsOffered(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no real install interferes

	t.Setenv("ARCHIFY_BIN", fakeArchify(t, "3.1.0"))
	cli, err := findArchify(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "3.1.0", cli.version)

	t.Setenv("ARCHIFY_BIN", fakeArchify(t, "2.9.0"))
	_, err = findArchify(t.TempDir())
	require.ErrorIs(t, err, ErrNoArchify)

	for version, want := range map[string]bool{"3.1.0": true, "3.4.2": true, "3.0.9": false, "4.0.0": false, "x": false} {
		assert.Equal(t, want, supportedArchify(version), version)
	}
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
		_, err := runGit(t.Context(), tools, root, args...)
		require.NoError(t, err)
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
