package codemap_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/codemap"
)

// The fixture under testdata/repo, mapped end to end with the real go command, the real Node and a
// real TypeScript: every relation the map promises appears there once.
//
// TypeScript comes from CODEMAP_TYPESCRIPT (a list of `typescript` package folders) and from this
// repository's own frontend once it is installed, so the classic compiler API and the native one
// can both be exercised. With none available the TypeScript half is skipped, unless
// CODEMAP_REQUIRE_TYPESCRIPT says the machine is meant to have one.

// copyFixture copies testdata/repo into a fresh directory, so a node_modules can be linked into it.
func copyFixture(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src, err := filepath.Abs(filepath.Join("testdata", "repo"))
	require.NoError(t, err)
	err = filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
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
	require.NoError(t, err)
	return dst
}

// typeScriptInstalls lists the `typescript` package folders the TypeScript half can be tested with.
func typeScriptInstalls(t *testing.T) []string {
	t.Helper()
	var installs []string
	for _, dir := range filepath.SplitList(os.Getenv("CODEMAP_TYPESCRIPT")) {
		if dir != "" {
			installs = append(installs, dir)
		}
	}
	if own, err := filepath.Abs(filepath.Join("..", "..", "frontend", "node_modules", "typescript")); err == nil {
		if _, err := os.Stat(filepath.Join(own, "package.json")); err == nil {
			installs = append(installs, own)
		}
	}
	return installs
}

func edgeTo(g *codemap.Graph, from, to string, kind codemap.EdgeKind) bool {
	return slices.ContainsFunc(g.Out[from], func(e codemap.Edge) bool { return e.To == to && e.Kind == kind })
}

func TestTheGoHalfOfARepository(t *testing.T) {
	tools := codemap.DiscoverTools()
	if tools.Go == "" {
		t.Skip("no go command on this machine")
	}
	root := copyFixture(t)
	service := codemap.NewService(func() codemap.Tools { return codemap.Tools{Go: tools.Go, Path: tools.Path} }, nil)

	g, status, err := service.Graph(t.Context(), root)
	require.NoError(t, err)
	require.Empty(t, status.Notes)

	const shop = "go:example.com/shop/shop."
	t.Run("the schema is what the migrations leave behind", func(t *testing.T) {
		for _, table := range []string{"items", "carts", "customers", "orders"} {
			assert.NotNil(t, g.Nodes["sql:"+table], table)
		}
		for _, gone := range []string{"carts_old", "legacy", "ghosts"} {
			assert.Nil(t, g.Nodes["sql:"+gone], gone)
		}
	})

	t.Run("a call through an interface reaches its implementation", func(t *testing.T) {
		assert.True(t, edgeTo(g, shop+"Service.AddItem", shop+"Store.Save", codemap.EdgeCall))
		assert.True(t, edgeTo(g, shop+"Store.Save", shop+"sqlStore.Save", codemap.EdgeDispatch))
		assert.True(t, edgeTo(g, shop+"Closer.Close", shop+"base.Close", codemap.EdgeDispatch), "promoted through an embedded field")
	})

	t.Run("tables are read and written", func(t *testing.T) {
		assert.True(t, edgeTo(g, shop+"sqlStore.Save", "sql:items", codemap.EdgeWrite), "bun's insert of a tagged model")
		assert.True(t, edgeTo(g, shop+"Service.ListItems", "sql:items", codemap.EdgeRead), "a SQL constant")
		assert.True(t, edgeTo(g, shop+"Service.ListItems", "sql:carts", codemap.EdgeRead))
		assert.True(t, edgeTo(g, shop+"Service.Count", "sql:items", codemap.EdgeRead), "a model handed to a helper as any")
		assert.True(t, edgeTo(g, shop+"Service.Customers", "sql:customers", codemap.EdgeRead), "GORM's naming convention")
		assert.True(t, edgeTo(g, shop+"Service.Customers", "sql:orders", codemap.EdgeWrite), "GORM's TableName")
		assert.NotEmpty(t, g.Nodes[shop+"Dynamic"].Unresolved, "a table only known at run time is shown, not guessed")
	})

	t.Run("a handler registered under a key is a node of its own", func(t *testing.T) {
		handler := shop + "Register$shop_list_items"
		require.NotNil(t, g.Nodes[handler])
		assert.True(t, edgeTo(g, shop+"Register", handler, codemap.EdgeRef))
		assert.True(t, edgeTo(g, handler, shop+"Service.ListItems", codemap.EdgeCall))
		assert.True(t, edgeTo(g, shop+"Register", shop+"helper", codemap.EdgeRef), "a function passed as a value")
		assert.Nil(t, g.Nodes[shop+"Register$not a key"], "a label is not a key")
	})

	t.Run("Wails binds the service's exported methods", func(t *testing.T) {
		assert.Equal(t, "Service", g.Nodes[shop+"Service.AddItem"].Bound)
		assert.Empty(t, g.Nodes[shop+"countRows"].Bound)
	})

	t.Run("tests are found, and run from their package", func(t *testing.T) {
		impact := g.Impact(g.Nodes[shop+"sqlStore.Save"], 0)
		require.Len(t, impact.GoTests, 1)
		assert.Equal(t, "go test -run '^(TestAddItem)$' ./shop", impact.GoTests[0].Command)
	})
}

func TestTheWholeRepositoryAcrossLanguages(t *testing.T) {
	tools := codemap.DiscoverTools()
	installs := typeScriptInstalls(t)
	if tools.Go == "" || tools.Node == "" || len(installs) == 0 {
		if os.Getenv("CODEMAP_REQUIRE_TYPESCRIPT") != "" {
			t.Fatalf("go=%q node=%q typescript=%v: the TypeScript half was required", tools.Go, tools.Node, installs)
		}
		t.Skip("needs go, node and a TypeScript install")
	}

	for _, install := range installs {
		real, err := filepath.EvalSymlinks(install)
		require.NoError(t, err)
		t.Run(filepath.Base(filepath.Dir(filepath.Dir(real)))+"/"+filepath.Base(real), func(t *testing.T) {
			root := copyFixture(t)
			modules := filepath.Join(root, "frontend", "node_modules")
			require.NoError(t, os.MkdirAll(modules, 0o750))
			if err := os.Symlink(real, filepath.Join(modules, "typescript")); err != nil {
				if runtime.GOOS == "windows" {
					t.Skip("symbolic links need a privilege here")
				}
				require.NoError(t, err)
			}

			events := &bridge.RecordingEmitter{}
			service := codemap.NewService(nil, events)
			g, status, err := service.Graph(t.Context(), root)
			require.NoError(t, err)
			require.Empty(t, status.Notes)
			assert.Equal(t, codemap.StateMapped, status.Languages[1].State, status.Languages[1].Detail)
			assert.Equal(t, []any{
				codemap.ProgressEvent{RootPath: root, Stage: codemap.StageScanning},
				codemap.ProgressEvent{RootPath: root, Stage: codemap.StageGo},
				codemap.ProgressEvent{RootPath: root, Stage: codemap.StageTypeScript},
				codemap.ProgressEvent{RootPath: root, Stage: codemap.StageLinking},
				codemap.ProgressEvent{RootPath: root, Stage: codemap.StageReady},
			}, events.Named(codemap.EventProgress))

			const api, store, list = "ts:frontend/src/api.ts#", "ts:frontend/src/store.ts#", "ts:frontend/src/ItemList.tsx#"
			const shop = "go:example.com/shop/shop."

			t.Run("TypeScript's own relations", func(t *testing.T) {
				assert.Equal(t, codemap.KindComponent, g.Nodes[list+"ItemList"].Kind)
				assert.Equal(t, codemap.KindHook, g.Nodes[list+"useItems"].Kind)
				assert.True(t, edgeTo(g, list+"ItemList", list+"ItemRow", codemap.EdgeRender))
				assert.True(t, edgeTo(g, list+"ItemList", list+"useItems", codemap.EdgeCall))
				assert.True(t, edgeTo(g, api+"Api.list", api+"api.list", codemap.EdgeDispatch), "an object literal typed as the interface")
				assert.True(t, edgeTo(g, store+"StoreState.load", store+"useStore.load", codemap.EdgeDispatch), "a store's methods are named under it")
				assert.True(t, edgeTo(g, list+"useItems", store+"StoreState.load", codemap.EdgeRef), "a selector picking a method")
				assert.Equal(t, codemap.KindTestFile, g.Nodes["ts:frontend/src/ItemList.test.tsx"].Kind)
			})

			t.Run("calls cross into Go", func(t *testing.T) {
				assert.True(t, edgeTo(g, api+"api.list", "ep:shop_list_items", codemap.EdgeCross), "a command by name")
				assert.True(t, edgeTo(g, "ep:shop_list_items", shop+"Register$shop_list_items", codemap.EdgeDispatch))
				assert.True(t, edgeTo(g, api+"api.save", "ep:/api/items/{id}", codemap.EdgeCross), "a route with a parameter")
				assert.True(t, edgeTo(g, api+"addDirect", shop+"Service.AddItem", codemap.EdgeCross), "a Wails binding")
				assert.True(t, edgeTo(g, api+"ShopContract.AddItem", shop+"Service.AddItem", codemap.EdgeCross), "an interface satisfied by a binding")
			})

			t.Run("the frontend's SQL", func(t *testing.T) {
				assert.True(t, edgeTo(g, store+"readCarts", "sql:items", codemap.EdgeRead))
				assert.True(t, edgeTo(g, store+"readCarts", "sql:carts", codemap.EdgeRead), "a table named to a query builder")
			})

			t.Run("a flow from a component reaches a table in Go", func(t *testing.T) {
				detail := g.Detail(g.Nodes[list+"ItemList"])
				tables := []string{}
				for _, use := range detail.Tables {
					tables = append(tables, use.Table.ID)
				}
				assert.Contains(t, tables, "sql:items")
			})

			t.Run("an impact in Go names the frontend's tests", func(t *testing.T) {
				impact := g.Impact(g.Nodes[shop+"sqlStore.Save"], 8)
				require.Len(t, impact.TSTests, 1)
				assert.Equal(t, "npx vitest run src/ItemList.test.tsx", impact.TSTests[0].Command)
			})
		})
	}
}
