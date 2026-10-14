package codemap_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/codemap"
)

// sampleGraph is a small repository drawn by hand: a component calls an interface method, which
// dispatches to a store; the store crosses into Go through an endpoint, and Go writes a table.
func sampleGraph() *codemap.Graph {
	g := codemap.NewGraph()
	g.Meta = codemap.Meta{GoModules: []string{"backend"}, TestRunners: map[string]string{"web": "vitest"}}
	nodes := []*codemap.Node{
		{ID: "ts:web/List.tsx#List", Name: "List", Lang: codemap.LangTS, Kind: codemap.KindComponent, Pos: codemap.Pos{File: "web/List.tsx", Line: 3, EndLine: 20}},
		{ID: "ts:web/List.tsx#List.onSave", Name: "List.onSave", Lang: codemap.LangTS, Kind: codemap.KindFunc, Pos: codemap.Pos{File: "web/List.tsx", Line: 5, EndLine: 7}, Parent: "ts:web/List.tsx#List"},
		{ID: "ts:web/store.ts#Store.save", Name: "Store.save", Lang: codemap.LangTS, Kind: codemap.KindInterfaceMethod, Pos: codemap.Pos{File: "web/store.ts", Line: 2}},
		{ID: "ts:web/store.ts#useStore.save", Name: "useStore.save", Lang: codemap.LangTS, Kind: codemap.KindMethod, Pos: codemap.Pos{File: "web/store.ts", Line: 9}},
		{ID: "ts:web/List.test.tsx", Name: "web/List.test.tsx", Lang: codemap.LangTS, Kind: codemap.KindTestFile, Pos: codemap.Pos{File: "web/List.test.tsx", Line: 1}},
		{ID: "ep:item_save", Name: "item_save", Lang: codemap.LangGo, Kind: codemap.KindEndpoint, Pos: codemap.Pos{File: "backend/items/commands.go", Line: 12}},
		{ID: "go:app/items.Register$item_save", Name: `items.Register("item_save")`, Lang: codemap.LangGo, Kind: codemap.KindFunc, Pos: codemap.Pos{File: "backend/items/commands.go", Line: 12}},
		{ID: "go:app/items.Save", Name: "items.Save", Lang: codemap.LangGo, Kind: codemap.KindFunc, Pos: codemap.Pos{File: "backend/items/save.go", Line: 4}, Doc: "Save stores one item."},
		{ID: "go:app/items.TestSave", Name: "items.TestSave", Lang: codemap.LangGo, Kind: codemap.KindTestFunc, Pos: codemap.Pos{File: "backend/items/save_test.go", Line: 8}},
		{ID: "sql:items", Name: "items", Lang: codemap.LangSQL, Kind: codemap.KindTable, Pos: codemap.Pos{File: "backend/migrations/001.sql", Line: 1}},
	}
	for _, n := range nodes {
		g.AddNode(n)
	}
	at := func(file string, line int) codemap.Pos { return codemap.Pos{File: file, Line: line} }
	for _, e := range []codemap.Edge{
		{From: "ts:web/List.tsx#List", To: "ts:web/List.tsx#List.onSave", Kind: codemap.EdgeRef, At: at("web/List.tsx", 15)},
		{From: "ts:web/List.tsx#List.onSave", To: "ts:web/store.ts#Store.save", Kind: codemap.EdgeCall, At: at("web/List.tsx", 6)},
		{From: "ts:web/store.ts#Store.save", To: "ts:web/store.ts#useStore.save", Kind: codemap.EdgeDispatch, At: at("web/store.ts", 9)},
		{From: "ts:web/store.ts#useStore.save", To: "ep:item_save", Kind: codemap.EdgeCross, At: at("web/store.ts", 10)},
		{From: "ep:item_save", To: "go:app/items.Register$item_save", Kind: codemap.EdgeDispatch, At: at("backend/items/commands.go", 12)},
		{From: "go:app/items.Register$item_save", To: "go:app/items.Save", Kind: codemap.EdgeCall, At: at("backend/items/commands.go", 13)},
		{From: "go:app/items.Save", To: "sql:items", Kind: codemap.EdgeWrite, At: at("backend/items/save.go", 6)},
		{From: "go:app/items.TestSave", To: "go:app/items.Save", Kind: codemap.EdgeCall, At: at("backend/items/save_test.go", 9)},
		{From: "ts:web/List.test.tsx", To: "ts:web/List.tsx#List", Kind: codemap.EdgeCall, At: at("web/List.test.tsx", 4)},
		{From: "ts:web/List.test.tsx", To: "ts:web/store.ts#Store.save", Kind: codemap.EdgeCall, At: at("web/List.test.tsx", 5)},
	} {
		g.AddEdge(e)
	}
	g.Finish()
	return g
}

func TestResolve(t *testing.T) {
	g := sampleGraph()
	tests := []struct {
		name string
		want []string
	}{
		{"go:app/items.Save", []string{"go:app/items.Save"}},
		{"items.Save", []string{"go:app/items.Save"}},
		{"Save", []string{"go:app/items.Save"}},
		{"save", []string{"ts:web/store.ts#Store.save", "ts:web/store.ts#useStore.save"}},
		{"web/List.tsx:6", []string{"ts:web/List.tsx#List.onSave"}},
		{"web/List.test.tsx", []string{"ts:web/List.test.tsx"}},
		{"nothing", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found := g.Resolve(tt.name)
			ids := make([]string, 0, len(found))
			for _, n := range found {
				ids = append(ids, n.ID)
			}
			assert.Equal(t, tt.want, ids)
		})
	}
}

func TestSearchRanksTheExactNameFirst(t *testing.T) {
	g := sampleGraph()

	results := g.Search("save", codemap.SearchOptions{})
	require.Len(t, results, 7, "partial matches follow the exact ones")
	for _, r := range results[:3] {
		assert.True(t, strings.EqualFold("save", r.Name[len(r.Name)-4:]), "%s is an exact match on its last segment", r.Name)
	}
	assert.Equal(t, "go:app/items.Save", results[0].ID, "a function ranks before an interface member, and the shorter name first")
	assert.Equal(t, "ts:web/List.tsx#List.onSave", results[3].ID, "a prefix of the last segment after the exact ones")
	assert.Len(t, g.Search("save", codemap.SearchOptions{Limit: 2}), 2)

	tables := g.Search("item", codemap.SearchOptions{Kind: codemap.KindTable})
	require.Len(t, tables, 1)
	assert.Equal(t, "sql:items", tables[0].ID)

	assert.Empty(t, g.Search("  ", codemap.SearchOptions{}))
	assert.NotNil(t, g.Search("  ", codemap.SearchOptions{}), "never null on the wire")
}

func TestDetailReachesThroughHubsAndKeepsTestsApart(t *testing.T) {
	g := sampleGraph()

	detail := g.Detail(g.Nodes["ts:web/store.ts#useStore.save"])

	require.Len(t, detail.Implements, 1)
	assert.Equal(t, "ts:web/store.ts#Store.save", detail.Implements[0].Symbol.ID)
	require.Len(t, detail.Callers, 1, "the test file calls the interface too, and is listed under tests instead")
	assert.Equal(t, "ts:web/List.tsx#List.onSave", detail.Callers[0].Symbol.ID)
	require.NotNil(t, detail.Callers[0].Via)
	assert.Equal(t, "ts:web/store.ts#Store.save", detail.Callers[0].Via.ID)

	require.Len(t, detail.Tables, 1)
	assert.Equal(t, "sql:items", detail.Tables[0].Table.ID)
	assert.True(t, detail.Tables[0].Write)
	assert.False(t, detail.Tables[0].Direct)
	assert.Equal(t, "go:app/items.Save", detail.Tables[0].Via.ID)

	ids := []string{}
	for _, test := range detail.Tests {
		ids = append(ids, test.ID)
	}
	assert.Equal(t, []string{"ts:web/List.test.tsx"}, ids)
}

func TestLookupAnswersCandidatesForAnAmbiguousName(t *testing.T) {
	g := sampleGraph()

	lookup := g.Lookup("save")
	assert.Nil(t, lookup.Detail)
	assert.Len(t, lookup.Candidates, 2)

	lookup = g.Lookup("items.Save")
	require.NotNil(t, lookup.Detail)
	assert.Equal(t, "Save stores one item.", lookup.Detail.Doc)
	assert.NotNil(t, lookup.Candidates)
}

func TestFlowCrossesIntoGoAndEndsAtTheTable(t *testing.T) {
	g := sampleGraph()

	flow := g.Flow(g.Nodes["ts:web/List.tsx#List"], codemap.FlowOptions{})

	path := []string{}
	for step := flow.Root; ; {
		path = append(path, step.Symbol.ID+" via "+step.Via)
		if len(step.Children) == 0 {
			break
		}
		step = step.Children[0]
	}
	assert.Equal(t, []string{
		"ts:web/List.tsx#List via ",
		"ts:web/List.tsx#List.onSave via ref",
		"ts:web/store.ts#Store.save via call",
		"ts:web/store.ts#useStore.save via dispatch",
		"ep:item_save via cross",
		"go:app/items.Register$item_save via dispatch",
		"go:app/items.Save via call",
		"sql:items via write",
	}, path)
	assert.False(t, flow.Truncated)
	assert.Equal(t, 7, flow.Steps)
}

func TestFlowStopsAtItsDepthAndSaysSo(t *testing.T) {
	g := sampleGraph()

	flow := g.Flow(g.Nodes["ts:web/List.tsx#List"], codemap.FlowOptions{Depth: 2})

	assert.True(t, flow.Truncated)
	assert.True(t, flow.Root.Children[0].Children[0].Truncated)
}

func TestImpactNamesEntryPointsAndTheTestsToRun(t *testing.T) {
	g := sampleGraph()

	impact := g.Impact(g.Nodes["go:app/items.Save"], 0)

	require.NotEmpty(t, impact.Levels)
	assert.Equal(t, "go:app/items.Register$item_save", impact.Levels[0][0].ID)
	entry := []string{}
	for _, e := range impact.EntryPoints {
		entry = append(entry, e.ID)
	}
	assert.Equal(t, []string{"ep:item_save", "ts:web/List.tsx#List"}, entry)

	require.Len(t, impact.GoTests, 1)
	assert.Equal(t, codemap.GoTestGroup{
		PackageDir: "backend/items",
		Tests:      []string{"TestSave"},
		WorkingDir: "backend",
		Command:    "go test -run '^(TestSave)$' ./items",
	}, impact.GoTests[0])

	require.Len(t, impact.TSTests, 1)
	assert.Equal(t, codemap.TSTestGroup{
		PackageDir: "web",
		Files:      []string{"web/List.test.tsx"},
		Runner:     "vitest",
		Command:    "npx vitest run List.test.tsx",
	}, impact.TSTests[0])
}

func TestOverviewCountsWhatTouchesEachTable(t *testing.T) {
	g := sampleGraph()

	overview := g.Overview(codemap.Status{RootPath: "/repo"})

	assert.Equal(t, 1, overview.Endpoints)
	require.Len(t, overview.Tables, 1)
	assert.Equal(t, 1, overview.Tables[0].Writers)
	assert.NotEmpty(t, overview.Areas)
	assert.NotNil(t, overview.Services)
	assert.NotNil(t, overview.Hotspots)
}
