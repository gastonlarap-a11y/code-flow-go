// Package codemap is the code map (CODEMAP-001 … ): a compiler-grade graph of an open repository's
// Go and TypeScript code — symbols, calls, interface dispatch, React renders, the hops across the
// language boundary (Wails bindings, string-keyed endpoints) and the SQL tables the code reads and
// writes — and the questions the renderer asks of it: search, a symbol's neighbourhood, the flow
// forward from it, and the blast radius back from it.
//
// Nothing here guesses a relation from a name. Go is type-checked with go/types against the
// compiler's export data; TypeScript is resolved by the repository's own TypeScript checker. What
// cannot be resolved statically (a table name in a variable) is recorded as unresolved and shown,
// never dropped and never filled in.
package codemap

import (
	"cmp"
	"slices"
)

// Lang is the language a node is written in.
type Lang uint8

const (
	LangGo Lang = iota + 1
	LangTS
	LangSQL
)

var langNames = map[Lang]string{LangGo: "go", LangTS: "ts", LangSQL: "sql"}

func (l Lang) String() string {
	if name, ok := langNames[l]; ok {
		return name
	}
	return "?"
}

// Kind says what a node is.
type Kind uint8

const (
	KindFunc Kind = iota + 1
	KindMethod
	// KindType is a struct, class, type alias or any other named type that is not an interface.
	KindType
	KindInterface
	// KindInterfaceMethod is a member of an interface: the hub a call through the interface lands
	// on, and from which it dispatches to every implementation.
	KindInterfaceMethod
	KindComponent
	KindHook
	KindTestFunc
	KindTestFile
	// KindModule is a TypeScript file itself: code at its top level is attributed to it.
	KindModule
	KindTable
	// KindEndpoint is a string key a Go handler is registered under (an HTTP route, a command name)
	// that TypeScript calls by the same string.
	KindEndpoint
)

var kindNames = map[Kind]string{
	KindFunc:            "func",
	KindMethod:          "method",
	KindType:            "type",
	KindInterface:       "interface",
	KindInterfaceMethod: "interface_method",
	KindComponent:       "component",
	KindHook:            "hook",
	KindTestFunc:        "test",
	KindTestFile:        "test_file",
	KindModule:          "module",
	KindTable:           "table",
	KindEndpoint:        "endpoint",
}

func (k Kind) String() string {
	if name, ok := kindNames[k]; ok {
		return name
	}
	return "?"
}

// ParseKind is the inverse of Kind.String; ok is false for a name it does not know.
func ParseKind(name string) (Kind, bool) {
	for kind, known := range kindNames {
		if known == name {
			return kind, true
		}
	}
	return 0, false
}

// IsTest reports whether the node is test code.
func (k Kind) IsTest() bool { return k == KindTestFunc || k == KindTestFile }

// EdgeKind says how two nodes relate.
type EdgeKind uint8

const (
	EdgeCall EdgeKind = iota + 1
	// EdgeRef is a function used as a value: a handler, a callback, a method value.
	EdgeRef
	// EdgeRender is a React component rendering another one.
	EdgeRender
	// EdgeDispatch runs from an interface member to an implementation of it.
	EdgeDispatch
	// EdgeCross is a hop across the language boundary: TypeScript calling a Wails-bound Go method,
	// or calling an endpoint a Go handler serves.
	EdgeCross
	EdgeRead
	EdgeWrite
	// EdgeMention is code naming a table without a verb that says what it does with it: a table
	// name passed to a generic helper.
	EdgeMention
)

var edgeNames = map[EdgeKind]string{
	EdgeCall:     "call",
	EdgeRef:      "ref",
	EdgeRender:   "render",
	EdgeDispatch: "dispatch",
	EdgeCross:    "cross",
	EdgeRead:     "read",
	EdgeWrite:    "write",
	EdgeMention:  "mention",
}

func (k EdgeKind) String() string {
	if name, ok := edgeNames[k]; ok {
		return name
	}
	return "?"
}

// IsData reports whether the edge reaches a table.
func (k EdgeKind) IsData() bool { return k == EdgeRead || k == EdgeWrite || k == EdgeMention }

// IsFlow reports whether a flow walks the edge: everything except table access, which ends a flow
// rather than continuing it.
func (k EdgeKind) IsFlow() bool { return !k.IsData() }

// Pos is a source range. File is relative to the repository root, with forward slashes.
type Pos struct {
	File    string
	Line    int
	EndLine int
}

// Node is one symbol, table or endpoint.
type Node struct {
	// ID is unique and stable across runs: "go:<pkgpath>.<Recv>.<Name>", "ts:<relpath>#<Outer.Name>"
	// (a bare "ts:<relpath>" is the file), "sql:<table>" or "ep:<key>".
	ID string
	// Name is what a person searches for: "finance.Service.CreateExpense", "ExpenseForm.onSubmit",
	// "expenses".
	Name      string
	Lang      Lang
	Kind      Kind
	Pos       Pos
	Signature string
	// Doc is the first sentence of the declaration's comment.
	Doc string
	// Parent is the ID of the enclosing type, function or object, when there is one.
	Parent string
	// Bound names the Go service type when Wails exposes this method to TypeScript.
	Bound string
	// Unresolved lists places whose callee or table is only known at run time. Shown to the reader,
	// never silently dropped.
	Unresolved []Pos
}

// Edge is a directed relation From → To; At is where it happens (the call site).
type Edge struct {
	From, To string
	Kind     EdgeKind
	At       Pos
}

type edgeKey struct {
	from, to string
	kind     EdgeKind
	at       Pos
}

// Meta is what the queries need to know about the repository beyond its symbols.
type Meta struct {
	// GoModules are the directories holding a go.mod, relative to the root ("" is the root).
	GoModules []string
	// TestRunners maps a JavaScript package's directory to its test runner ("vitest", "jest").
	TestRunners map[string]string
}

// Graph is the whole map. Build it with AddNode and AddEdge, then call Finish once before asking
// it anything; a finished graph is never mutated, so it can be shared between concurrent readers.
type Graph struct {
	Nodes map[string]*Node
	Out   map[string][]Edge
	In    map[string][]Edge
	Meta  Meta

	seen map[edgeKey]struct{}
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{
		Nodes: map[string]*Node{},
		Out:   map[string][]Edge{},
		In:    map[string][]Edge{},
		seen:  map[edgeKey]struct{}{},
	}
}

// AddNode inserts n unless a node with its ID exists, and returns the stored node.
func (g *Graph) AddNode(n *Node) *Node {
	if existing, ok := g.Nodes[n.ID]; ok {
		return existing
	}
	g.Nodes[n.ID] = n
	return n
}

// AddEdge inserts e once. An edge to or from a node that is not in the graph is dropped: a call
// into code that was not indexed, like the standard library or a dependency.
func (g *Graph) AddEdge(e Edge) {
	if g.Nodes[e.From] == nil || g.Nodes[e.To] == nil || e.From == e.To {
		return
	}
	key := edgeKey{from: e.From, to: e.To, kind: e.Kind, at: e.At}
	if _, duplicate := g.seen[key]; duplicate {
		return
	}
	g.seen[key] = struct{}{}
	g.Out[e.From] = append(g.Out[e.From], e)
	g.In[e.To] = append(g.In[e.To], e)
}

// Finish sorts every adjacency list, so that every answer is deterministic.
func (g *Graph) Finish() {
	for _, list := range g.Out {
		slices.SortFunc(list, compareEdges(func(e Edge) string { return e.To }))
	}
	for _, list := range g.In {
		slices.SortFunc(list, compareEdges(func(e Edge) string { return e.From }))
	}
	g.seen = nil
}

func compareEdges(other func(Edge) string) func(a, b Edge) int {
	return func(a, b Edge) int {
		return cmp.Or(
			cmp.Compare(a.Kind, b.Kind),
			cmp.Compare(other(a), other(b)),
			cmp.Compare(a.At.File, b.At.File),
			cmp.Compare(a.At.Line, b.At.Line),
		)
	}
}

// EdgeCount is the number of edges in the graph.
func (g *Graph) EdgeCount() int {
	total := 0
	for _, list := range g.Out {
		total += len(list)
	}
	return total
}
