package codemap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteMatching(t *testing.T) {
	tests := []struct {
		route, requested string
		want             bool
	}{
		{"/api/items", "/api/items", true},
		{"GET /api/items/{id}", "/api/items/42", true},
		{"/api/items/:id", "/api/items/{}", true},
		{"/api/items/{id}", "https://example.test/api/items/7?full=1", true},
		{"/api/items/{id}", "/api/items", false},
		{"/api/items", "/api/orders", false},
	}
	for _, tt := range tests {
		t.Run(tt.route+" ← "+tt.requested, func(t *testing.T) {
			assert.Equal(t, tt.want, segmentsMatch(routeSegments(tt.route), routeSegments(tt.requested)))
		})
	}
}

// A separator handed to split, or a route made only of parameters, must not join a catch-all
// handler to every one-segment string in the frontend.
func TestOnlyRealRequestsMatchRoutes(t *testing.T) {
	assert.False(t, looksLikeURL("/"))
	assert.False(t, looksLikeURL("items"))
	assert.True(t, looksLikeURL("/items"))
	assert.False(t, hasLiteralSegment(routeSegments("/{path...}")))
}

func TestAssembleJoinsBothHalves(t *testing.T) {
	goSide := &goHalf{
		nodes: []*Node{
			{ID: "go:app/shop.Service.AddItem", Name: "shop.Service.AddItem", Lang: LangGo, Kind: KindMethod, Bound: "Service"},
			{ID: "go:app/shop.Register$shop_list", Name: `shop.Register("shop_list")`, Lang: LangGo, Kind: KindFunc},
			{ID: "go:app/shop.serveItem", Name: "shop.serveItem", Lang: LangGo, Kind: KindFunc},
			{ID: "go:app/shop.Model", Name: "shop.Model", Lang: LangGo, Kind: KindFunc},
		},
		edges: []Edge{
			{From: "go:app/shop.Service.AddItem", To: "sql:items", Kind: EdgeWrite},
			{From: "go:app/shop.Model", To: "sql:orders", Kind: EdgeRead},
			{From: "go:app/shop.Model", To: "sql:ghosts", Kind: EdgeRead},
		},
		registrations: []registration{
			{key: "shop_list", handler: "go:app/shop.Register$shop_list"},
			{key: "GET /api/items/{id}", handler: "go:app/shop.serveItem"},
		},
		bound:         map[boundKey]string{{pkg: "app/shop", typ: "service", method: "AddItem"}: "go:app/shop.Service.AddItem"},
		guessedTables: map[string]bool{"ghosts": true},
	}
	tsSide := &tsExtract{
		Symbols: []tsSymbol{
			{ID: "ts:src/api.ts#list", Name: "list", Kind: "function", File: "src/api.ts", Line: 1},
			{ID: "ts:src/api.ts#get", Name: "get", Kind: "function", File: "src/api.ts", Line: 5},
			{ID: "ts:src/api.ts#add", Name: "add", Kind: "function", File: "src/api.ts", Line: 9},
			{ID: "ts:src/api.ts#Shop", Name: "Shop", Kind: "interface", File: "src/api.ts", Line: 12},
			{ID: "ts:src/api.ts#Shop.AddItem", Name: "Shop.AddItem", Kind: "interface_method", File: "src/api.ts", Line: 13, Parent: "ts:src/api.ts#Shop"},
		},
		Bindings:       []tsBinding{{From: "ts:src/api.ts#add", Specifier: "app/shop", Name: "Service", Member: "AddItem", File: "src/api.ts", Line: 10}},
		BindingAliases: []tsBindingAlias{{Interface: "ts:src/api.ts#Shop", Specifier: "app/shop/service", Name: ""}},
		Calls: []tsCall{
			{From: "ts:src/api.ts#list", Callee: "invoke", Value: "shop_list", File: "src/api.ts", Line: 2},
			{From: "ts:src/api.ts#get", Callee: "fetch", Value: "/api/items/{}", File: "src/api.ts", Line: 6},
			{From: "ts:src/api.ts#get", Callee: "selectFrom", Value: "items", File: "src/api.ts", Line: 7},
		},
		Strings: []tsString{{From: "ts:src/api.ts#list", Text: "CREATE TABLE prefs (k text)", File: "src/api.ts", Line: 3}},
	}

	g := assemble(tableSet{"items": {File: "001.sql", Line: 1}}, goSide, tsSide, Meta{})

	require.NotNil(t, g.Nodes["sql:items"])
	require.NotNil(t, g.Nodes["sql:prefs"], "a table the frontend creates for itself")
	require.NotNil(t, g.Nodes["sql:orders"], "a table a model names is real even with no DDL")
	assert.Nil(t, g.Nodes["sql:ghosts"], "a conventional name the schema does not confirm is dropped")

	assert.Contains(t, g.Out["ts:src/api.ts#add"], Edge{From: "ts:src/api.ts#add", To: "go:app/shop.Service.AddItem", Kind: EdgeCross, At: Pos{File: "src/api.ts", Line: 10}})
	assert.Contains(t, g.Out["ts:src/api.ts#Shop.AddItem"], Edge{From: "ts:src/api.ts#Shop.AddItem", To: "go:app/shop.Service.AddItem", Kind: EdgeCross})

	require.NotNil(t, g.Nodes["ep:shop_list"])
	assert.Equal(t, KindEndpoint, g.Nodes["ep:shop_list"].Kind)
	assert.Contains(t, g.Out["ts:src/api.ts#list"], Edge{From: "ts:src/api.ts#list", To: "ep:shop_list", Kind: EdgeCross, At: Pos{File: "src/api.ts", Line: 2}})
	assert.Contains(t, g.Out["ep:shop_list"], Edge{From: "ep:shop_list", To: "go:app/shop.Register$shop_list", Kind: EdgeDispatch})
	assert.Contains(t, g.Out["ts:src/api.ts#get"], Edge{From: "ts:src/api.ts#get", To: "ep:GET /api/items/{id}", Kind: EdgeCross, At: Pos{File: "src/api.ts", Line: 6}})
	assert.Contains(t, g.Out["ts:src/api.ts#get"], Edge{From: "ts:src/api.ts#get", To: "sql:items", Kind: EdgeRead, At: Pos{File: "src/api.ts", Line: 7}})
}

func TestNamingConventions(t *testing.T) {
	for name, want := range map[string]string{
		"Order": "orders", "OrderItem": "order_items", "Category": "categories", "Box": "boxes",
		"HTTPLog": "http_logs", "Key": "keys",
	} {
		assert.Equal(t, want, pluralize(snakeCase(name)), name)
	}
}
