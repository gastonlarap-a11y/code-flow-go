package repomap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// file builds a parsed TypeScript file's facts by hand.
func file(path string, lines int, decls []Decl, imports []string, refs ...Ref) fileRow {
	facts := &Facts{Lang: LangTypeScript, Lines: lines, Parsed: true, Decls: decls, Imports: make([]Import, 0, len(imports)), Refs: refs}
	if decls == nil {
		facts.Decls = []Decl{}
	}
	for _, spec := range imports {
		facts.Imports = append(facts.Imports, Import{Spec: spec, Line: 1})
	}
	return fileRow{Path: path, Facts: facts}
}

func decl(name string, kind Kind, line, end, parent int) Decl {
	return Decl{Name: name, Kind: kind, Line: line, EndLine: end, Signature: string(kind) + " " + name, Parent: parent}
}

// shop is a small repository: a cart that prices through a pricing module, a page that uses both,
// and a second file that declares its own `total` and imports nothing of ours.
func shop() *Snapshot {
	return buildSnapshot([]fileRow{
		file("src/cart/cart.ts", 30, []Decl{
			decl("Cart", KindClass, 1, 20, -1),
			decl("total", KindMethod, 5, 9, 0),
			decl("applyDiscount", KindFunction, 22, 30, -1),
		}, []string{"../pricing/price"}, Ref{Name: "priceOf", Line: 6}),
		file("src/pricing/price.ts", 10, []Decl{decl("priceOf", KindFunction, 1, 10, -1)}, nil),
		file("src/pages/checkout.ts", 40, []Decl{decl("Checkout", KindFunction, 1, 40, -1)},
			[]string{"../cart/cart", "../pricing/price", "react"},
			Ref{Name: "Cart", Line: 3}, Ref{Name: "applyDiscount", Line: 12}, Ref{Name: "priceOf", Line: 14}, Ref{Name: "total", Line: 15}),
		file("src/report/summary.ts", 12, []Decl{decl("total", KindFunction, 1, 12, -1)}, []string{"lodash"}, Ref{Name: "Cart", Line: 2}),
		// An import that resolves nowhere: what this file uses could come from anywhere.
		file("README.ts", 3, nil, []string{"./gone"}, Ref{Name: "applyDiscount", Line: 1}),
	}, manifests{}, 0)
}

func ids(nodes []MapNode) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}

func TestTheFolderViewOpensALoneSubfolderAndCountsImportersFromOutside(t *testing.T) {
	s := shop()

	root := s.FolderGraph("")
	assert.Equal(t, "", root.Focus, "the root holds a file of its own, so it stays")
	assert.Equal(t, []string{"README.ts", "src"}, ids(root.Nodes))

	src := s.FolderGraph("src/")
	assert.Equal(t, "src", src.Focus)
	assert.Equal(t, []string{"src/cart", "src/pages", "src/pricing", "src/report"}, ids(src.Nodes))
	assert.Equal(t, []MapEdge{
		{From: "src/cart", To: "src/pricing", Weight: 1},
		{From: "src/pages", To: "src/cart", Weight: 1},
		{From: "src/pages", To: "src/pricing", Weight: 1},
	}, src.Edges)
	for _, n := range src.Nodes {
		switch n.ID {
		case "src/pricing":
			assert.Equal(t, 2, n.ImportedBy)
			assert.True(t, n.Folder)
			assert.Equal(t, "src", n.Group)
			assert.Equal(t, "TypeScript", n.Language)
		case "src/cart":
			assert.Equal(t, 3, n.Declarations)
			assert.Equal(t, 30, n.Lines)
		}
	}
	assert.Equal(t, 5, src.Files, "the whole repository, whatever the focus")

	only := buildSnapshot([]fileRow{file("app/src/main/a.ts", 1, nil, nil), file("app/src/main/b.ts", 1, nil, nil)}, manifests{}, 0)
	assert.Equal(t, "app/src/main", only.FolderGraph("").Focus, "a chain of lone folders opens all the way down")
}

func TestTheProjectViewFoldsFoldersUntilItFits(t *testing.T) {
	s := shop()

	all := s.ProjectGraph(ProjectNodes)
	assert.Len(t, all.Nodes, 5, "five files fit: every file is a node")

	folded := s.ProjectGraph(4)
	assert.Equal(t, []string{"README.ts", "src"}, ids(folded.Nodes), "cut at the first level, the root file stays its own node")
	assert.Empty(t, folded.Edges, "imports inside one node are not drawn")
	assert.Equal(t, "", folded.Nodes[0].Group)

	six := buildSnapshot([]fileRow{
		file("a/x/1.ts", 1, nil, nil), file("a/x/2.ts", 1, nil, nil), file("a/y/3.ts", 1, nil, nil),
		file("b/z/4.ts", 1, nil, nil), file("b/z/5.ts", 1, nil, nil), file("root.ts", 1, nil, nil),
	}, manifests{}, 0)
	assert.Equal(t, []string{"a/x", "a/y", "b/z", "root.ts"}, ids(six.ProjectGraph(5).Nodes), "the deepest cut that fits")
}

func TestAFileShowsItsOutlineAndConnections(t *testing.T) {
	s := shop()
	detail := s.File("cart/cart.ts") // a unique trailing part is enough
	require.NotNil(t, detail)
	assert.Equal(t, "src/cart/cart.ts", detail.Path)
	assert.Equal(t, []OutlineRow{
		{Name: "Cart", Kind: KindClass, Line: 1, EndLine: 20, Signature: "class Cart", Depth: 0},
		{Name: "total", Kind: KindMethod, Line: 5, EndLine: 9, Signature: "method total", Depth: 1},
		{Name: "applyDiscount", Kind: KindFunction, Line: 22, EndLine: 30, Signature: "function applyDiscount", Depth: 0},
	}, detail.Declarations)
	assert.Equal(t, []string{"src/pricing/price.ts"}, detail.Imports)
	assert.Equal(t, []string{"src/pages/checkout.ts"}, detail.ImportedBy)

	page := s.File("src/pages/checkout.ts")
	require.NotNil(t, page)
	assert.Equal(t, []string{"react"}, page.External)

	assert.Nil(t, s.File("nowhere.ts"))
}

func TestSearchRanksTheClosestNameFirst(t *testing.T) {
	s := shop()

	names := func(hits []DeclHit) []string {
		out := make([]string, 0, len(hits))
		for _, h := range hits {
			out = append(out, h.Path+"#"+h.Name)
		}
		return out
	}
	assert.Equal(t, []string{"src/cart/cart.ts#Cart"}, names(s.Search("Cart", SearchLimit)), "exact, and nothing merely containing it")
	assert.Equal(t, []string{"src/cart/cart.ts#Cart"}, names(s.Search("cart", SearchLimit)))
	assert.Equal(t, []string{"src/cart/cart.ts#total"}, names(s.Search("Cart.total", SearchLimit)), "a member, by its owner")
	assert.Equal(t, []string{"src/pricing/price.ts#priceOf"}, names(s.Search("price()", SearchLimit)))

	both := s.Search("total", SearchLimit)
	require.Len(t, both, 2)
	assert.Equal(t, "Cart", both[0].Parent, "the one other files use comes first")
	assert.Equal(t, 1, both[0].UsedBy)
	assert.Equal(t, 0, both[1].UsedBy)

	assert.Empty(t, s.Search("  ", SearchLimit))
}

func TestUsagesConfirmByImportAndExcludeANamesakesUsers(t *testing.T) {
	s := shop()

	report := s.Usages("applyDiscount", "", UsagesLimit)
	require.Len(t, report.Declarations, 1)
	assert.Equal(t, []Usage{
		{Path: "src/pages/checkout.ts", Line: 12, Within: "function Checkout", Confirmed: true},
		{Path: "README.ts", Line: 1, Confirmed: false},
	}, report.Usages, "a file with an import that resolves nowhere stays, by name only")

	cart := s.Usages("Cart", "src/cart/cart.ts", UsagesLimit)
	assert.Equal(t, []Usage{{Path: "src/pages/checkout.ts", Line: 3, Within: "function Checkout", Confirmed: true}}, cart.Usages)
	assert.Equal(t, 1, cart.Excluded, "summary.ts imports only lodash, so the Cart it mentions is not this one")

	assert.Empty(t, s.Usages("nothing", "", UsagesLimit).Declarations)
}

func TestTheMapReadsAsTextAModelCanCite(t *testing.T) {
	s := shop()

	overview := s.Overview(4000)
	assert.Contains(t, overview, "Repository map: 5 code files (TypeScript 5), 6 declarations.")
	assert.Contains(t, overview, "src/ (4 · 6)")
	assert.Contains(t, overview, "  src/cart/ (1 · 3)")
	assert.Contains(t, overview, "priceOf() — src/pricing/price.ts:1 · 2")
	assert.LessOrEqual(t, len(s.Overview(120)), 124, "the budget holds, cut on a line")

	outline := s.OutlineText("src/cart/cart.ts")
	assert.Contains(t, outline, "src/cart/cart.ts — TypeScript, 30 lines")
	assert.Contains(t, outline, "    5-9  method total  — method total", "a member sits under its class")
	assert.Contains(t, outline, "Imports:\n  src/pricing/price.ts")
	assert.Contains(t, s.OutlineText("gone.ts"), "is not in the map")

	assert.Contains(t, s.DeclarationsText("Cart"), "src/cart/cart.ts:1-20  class Cart")
	usages := s.UsagesText("applyDiscount", "")
	assert.Contains(t, usages, "src/pages/checkout.ts:12  (in function Checkout)")
	assert.Contains(t, usages, "README.ts:1  [name only: imports nothing that declares it]")
	assert.Contains(t, s.UsagesText("Cart", ""), "Not uses of this one: 1 more files mention a different Cart.")
}

func TestMostUsedCountsConfirmedUsersOfTopLevelDeclarations(t *testing.T) {
	hits := shop().MostUsed(MostUsedLimit)
	got := make(map[string]int, len(hits))
	for _, h := range hits {
		got[h.Name] = h.UsedBy
	}
	assert.Equal(t, map[string]int{"priceOf": 2, "Cart": 1, "applyDiscount": 1}, got, "no method, nothing used by nobody")
	assert.Equal(t, "priceOf", hits[0].Name)
}
