//go:build cgo

package archify

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/repomap"
)

func writeFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}

func snapshotOf(t *testing.T, root string) *repomap.Snapshot {
	t.Helper()
	snap, _, err := repomap.NewService(t.TempDir()).Snapshot(t.Context(), root)
	require.NoError(t, err)
	return snap
}

func decodeClass(t *testing.T, raw []byte) classDoc {
	t.Helper()
	var doc classDoc
	require.NoError(t, json.Unmarshal(raw, &doc))
	return doc
}

func boxNamed(t *testing.T, doc classDoc, id string) classType {
	t.Helper()
	for _, box := range doc.Types {
		if box.ID == id {
			return box
		}
	}
	require.Failf(t, "no box", "%s", id)
	return classType{}
}

func counts(drawn, total int) string { return "" }

func TestATypeScriptFolderIsDrawnAsItsClassesAndHowTheyRelate(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/shop.ts", `export interface Shop { name: string; buy(n: number): void; }
export abstract class Base implements Shop { name = ""; abstract buy(n: number): void; }
export class Cart extends Base {
  private items: Item[] = [];
  total(rate: number): Price { return new Price(); }
  buy(n: number): void {}
}
export class Item {}
export class Price {}
export enum Color { Red, Green }
`)
	built, err := BuildClassDiagram(snapshotOf(t, root), Repo{}, "src", "src — classes", counts, "en")
	require.NoError(t, err)
	assert.Equal(t, 6, built.Types)
	assert.Zero(t, built.Omitted)

	doc := decodeClass(t, built.Document)
	assert.Equal(t, "class", doc.DiagramType)
	assert.Equal(t, "grid", doc.Layout.Mode)
	assert.Equal(t, "abstract", boxNamed(t, doc, "Base").Kind)
	assert.Equal(t, "interface", boxNamed(t, doc, "Shop").Kind)
	assert.Equal(t, "enum", boxNamed(t, doc, "Color").Kind)

	cart := boxNamed(t, doc, "Cart")
	assert.Equal(t, []classAttribute{{Name: "items", Type: "Item[]", Visibility: "private"}}, cart.Attributes)
	assert.Contains(t, cart.Methods, classMethod{Name: "total", Parameters: "rate: number", Returns: "Price", Visibility: "public"})

	assert.ElementsMatch(t, []classRelationship{
		{From: "Base", To: "Shop", Kind: "realization"},
		{From: "Cart", To: "Base", Kind: "inheritance"},
		{From: "Cart", To: "Item", Kind: "composition"},
		{From: "Cart", To: "Price", Kind: "dependency"},
	}, doc.Relationships, "between two types, only the strongest relation")

	assert.Less(t, boxNamed(t, doc, "Shop").Row, boxNamed(t, doc, "Base").Row)
	assert.Less(t, boxNamed(t, doc, "Base").Row, cart.Row)
	assert.Nil(t, doc.Meta.Repository, "no pinned commit, no citation")
}

func TestAGoPackageGathersAStructsMethodsFromEveryFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/shop\n")
	writeFile(t, root, "cart/cart.go", "package cart\n\ntype Store interface {\n\tReader\n\tSave(c *Cart) error\n}\n\ntype Reader interface{ Read() error }\n\ntype Cart struct {\n\tBase\n\tItems []Item\n}\n\ntype Base struct{}\n\ntype Item struct{ Price int }\n")
	writeFile(t, root, "cart/total.go", "package cart\n\nfunc (c *Cart) Total() int { return 0 }\n")
	built, err := BuildClassDiagram(snapshotOf(t, root), Repo{}, "", "shop — classes", counts, "en")
	require.NoError(t, err)

	doc := decodeClass(t, built.Document)
	cart := boxNamed(t, doc, "Cart")
	assert.Equal(t, []classMethod{{Name: "Total", Returns: "int", Visibility: "public"}}, cart.Methods, "declared in another file of the package")
	assert.ElementsMatch(t, []classRelationship{
		{From: "Cart", To: "Base", Kind: "composition"},
		{From: "Cart", To: "Item", Kind: "composition"},
		{From: "Store", To: "Reader", Kind: "inheritance"},
		{From: "Store", To: "Cart", Kind: "dependency"},
	}, doc.Relationships, "an embedded struct is held; an embedded interface is extended")
}

func TestAScopeWithoutTypesSaysSo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "util.py", "def helper():\n    pass\n")
	_, err := BuildClassDiagram(snapshotOf(t, root), Repo{}, "", "x", counts, "en")
	require.ErrorIs(t, err, ErrNoTypes)
}

func TestAManyTypedFolderDrawsTheMostUsed(t *testing.T) {
	root := t.TempDir()
	for i := range MaxClassTypes + 5 {
		writeFile(t, root, filepath.Join("m", "t"+string(rune('a'+i/26))+string(rune('a'+i%26))+".ts"), "export class C"+string(rune('a'+i/26))+string(rune('a'+i%26))+" {}\n")
	}
	var told [2]int
	built, err := BuildClassDiagram(snapshotOf(t, root), Repo{}, "m", "m", func(drawn, total int) string {
		told = [2]int{drawn, total}
		return "partial"
	}, "en")
	require.NoError(t, err)
	assert.Equal(t, MaxClassTypes, built.Types)
	assert.Equal(t, 5, built.Omitted)
	assert.Equal(t, [2]int{MaxClassTypes, MaxClassTypes + 5}, told)
	for _, box := range decodeClass(t, built.Document).Types {
		assert.LessOrEqual(t, box.Row, archifyGridLimit)
		assert.LessOrEqual(t, box.Col, float64(archifyGridLimit))
	}
}

func TestTopicsAreSuggestedFromWhatTheCodeDeclares(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "api/orders_controller.ts", "export class OrdersController {}\nexport function helper() {}\n")
	writeFile(t, root, "api/routes.py", "def UserHandler():\n    pass\n")
	writeFile(t, root, "domain/order.ts", "export enum OrderStatus { New, Paid }\nexport type PaymentState = 'a' | 'b'\nexport class OrderEntity {}\nexport interface OrderRepository {}\n")
	writeFile(t, root, "web/use.ts", "import { OrderStatus } from '../domain/order'\nconst s: OrderStatus = OrderStatus.New\n")
	writeFile(t, root, "api/router_test.go", "package api\n\nfunc TestTheRouterRejectsAHandler() {}\n")
	writeFile(t, root, "api/orders_controller.test.ts", "export class FakeOrdersController {}\n")
	snap := snapshotOf(t, root)

	labels := func(kind string) []string {
		out := []string{}
		for _, s := range Suggestions(snap, root, kind) {
			out = append(out, s.Label)
			assert.Contains(t, s.Topic, s.Label)
			assert.Regexp(t, `^[a-z/_.]+:\d+$`, s.Hint)
		}
		return out
	}
	assert.ElementsMatch(t, []string{"OrdersController", "UserHandler"}, labels("sequence"))
	assert.Equal(t, []string{"OrderStatus", "PaymentState"}, labels("lifecycle"), "the most used first")
	assert.ElementsMatch(t, []string{"OrderEntity", "OrderRepository"}, labels("dataflow"))
	assert.Empty(t, labels("architecture"), "an architecture is of the whole repository")
}

// With a real Archify, the class diagrams of this repository's own crowded folders — forty types,
// several packages, a few dozen relationships — are accepted once routed. Unrouted, Archify's own
// router drew lines through boxes in each of them.
func TestARealArchifyAcceptsTheClassDiagramsOfThisRepository(t *testing.T) {
	script := os.Getenv("ARCHIFY_BIN")
	node, err := exec.LookPath("node")
	if script == "" || err != nil {
		t.Skip("needs ARCHIFY_BIN and node")
	}
	setHome(t, t.TempDir())
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	rt := Runtime{Node: node, Path: filepath.SplitList(os.Getenv("PATH"))}
	cli, err := Find(root)
	require.NoError(t, err)
	snap := snapshotOf(t, root)
	// Pinned to this checkout's commit when it has an origin, so the citations are checked as well.
	repo := ReadRepo(t.Context(), rt, root)
	for _, scope := range []string{"backend/ai", "backend/codemap", "backend/providers", "backend"} {
		t.Run(scope, func(t *testing.T) {
			built, err := BuildClassDiagram(snap, repo, scope, scope+" — classes", counts, "en")
			require.NoError(t, err)
			_, report, err := cli.RouteClassDiagram(t.Context(), rt, root, built.Document)
			require.NoError(t, err)
			assert.True(t, report.OK, report.Problems())
		})
	}
}

// Each written name means the type its code means: a qualified one the type of that package, an
// unqualified one in Go the type of its own package, and elsewhere the only one of its name.
func TestANameMeansTheTypeItsCodeMeans(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "storage/store.go", "package storage\n\ntype Store struct{ path string }\n\ntype Deps struct{ Path string }\n")
	writeFile(t, root, "workspaces/store.go", "package workspaces\n\nimport \"acme/storage\"\n\ntype Store struct{ db *storage.Store }\n\ntype Deps struct{ Store Store }\n")
	writeFile(t, root, "web/cart.ts", "export class Cart {}\n")
	writeFile(t, root, "shop/cart.ts", "export class Cart {}\n")
	writeFile(t, root, "app/page.ts", "export class Page { cart: Cart; item: Item }\n")
	writeFile(t, root, "app/item.ts", "export class Item {}\n")
	built, err := BuildClassDiagram(snapshotOf(t, root), Repo{}, "", "x", counts, "en")
	require.NoError(t, err)
	doc := decodeClass(t, built.Document)

	idOf := func(path, label string) string {
		for _, box := range doc.Types {
			if box.Sublabel == path && box.Label == label {
				return box.ID
			}
		}
		require.Failf(t, "no box", "%s %s", path, label)
		return ""
	}
	related := map[[2]string]string{}
	for _, r := range doc.Relationships {
		related[[2]string{r.From, r.To}] = r.Kind
	}
	assert.Equal(t, map[[2]string]string{
		{idOf("workspaces/store.go", "Store"), idOf("storage/store.go", "Store")}:   "composition",
		{idOf("workspaces/store.go", "Deps"), idOf("workspaces/store.go", "Store")}: "composition",
		{idOf("app/page.ts", "Page"), idOf("app/item.ts", "Item")}:                  "composition",
	}, related, "storage's Deps holds no Store, and a Cart declared in two other folders is not guessed")
}

// With a real Archify, the diagram the map draws is one Archify accepts.
func TestARealArchifyAcceptsTheClassDiagramTheMapDraws(t *testing.T) {
	script := os.Getenv("ARCHIFY_BIN")
	node, err := exec.LookPath("node")
	if script == "" || err != nil {
		t.Skip("needs ARCHIFY_BIN and node")
	}
	setHome(t, t.TempDir())
	root := t.TempDir()
	writeFile(t, root, "src/shop.ts", "export interface Shop { buy(): void }\nexport class Cart implements Shop { private items: Item[] = []; buy(): void {} }\nexport class Item {}\n")
	built, err := BuildClassDiagram(snapshotOf(t, root), Repo{}, "src", "src — classes", func(d, _ int) string { return "drawn" }, "en")
	require.NoError(t, err)
	cli, err := Find(root)
	require.NoError(t, err)
	report, err := cli.Validate(t.Context(), Runtime{Node: node, Path: filepath.SplitList(os.Getenv("PATH"))}, root, built.Document)
	require.NoError(t, err)
	assert.True(t, report.OK, report.Problems())
}
