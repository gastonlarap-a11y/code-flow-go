package codemap

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gastonlarap-a11y/code-flow/backend/repomap"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
)

// The code map as an MCP server (CODEMAP-015), served by CodeFlow's own binary:
//
//	CodeFlow codemap-mcp --root <repository>
//
// An AI run CodeFlow starts is handed this server, so the model can ask the map — who calls this,
// where does this lead, what breaks — instead of reading files to rebuild the same graph by hand.

// MCPCommand is the first argument that makes the binary serve the map over stdio instead of
// opening a window. main checks it before anything else runs.
const MCPCommand = "codemap-mcp"

// MCPServerName is the server's name in an MCP configuration; its tools are `mcp__codemap__*` to
// Claude Code, which is what an allow-list names.
const MCPServerName = "codemap"

const mcpInstructions = `codemap answers questions about this repository from a compiler-grade map of its Go and
TypeScript code: real calls, interface dispatch, React renders, the hops from TypeScript into Go
(Wails bindings, string-keyed endpoints and routes) and the SQL tables each symbol reads or writes.

- codemap_overview: start here — where the code is, what Go exposes to the frontend, the tables.
- codemap_search: find a symbol when you only know part of its name.
- codemap_symbol: one symbol — signature, doc, callers, callees, implementations, tables, tests.
- codemap_flow: everything a symbol leads to, across languages, down to the tables.
- codemap_impact: what breaks if a symbol changes, and the exact test commands to run.

Names accept an ID, a full or trailing name (CreateExpense, Service.CreateExpense) or file:line.
Every answer cites file:line. "Only known at run time" marks a table or callee held in a variable.

The map also indexes every code file of the repository file by file — TypeScript, JavaScript, Go,
Python, Java and Dart with their declarations, any other code file by its name — with nothing to
build first:

- codemap_files: the repository at a glance — its languages, its folders, what is used most.
- codemap_outline: what one file declares, what it imports and who imports it.
- codemap_declarations: where a class, function or type is declared, by part of its name.
- codemap_name_usages: the files that use a name, confirmed by an import or by name only.`

// graphPatience is how long a tool waits for a cold build before answering that the map is still
// being built; the build carries on for the next call.
const graphPatience = 20 * time.Second

var errStillBuilding = errors.New("the code map is still being built (the first build can take a minute on a large repository); ask again in a few seconds")

type searchInput struct {
	Query string `json:"query" jsonschema:"part of a symbol or table name"`
	Kind  string `json:"kind,omitempty" jsonschema:"only this kind: func, method, type, interface, interface_method, component, hook, test, test_file, module, table, endpoint"`
	Lang  string `json:"lang,omitempty" jsonschema:"only go, ts or sql"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum results (default 30)"`
}

type nameInput struct {
	Name string `json:"name" jsonschema:"a symbol ID, a full or trailing name, or file:line"`
}

type flowInput struct {
	Name     string `json:"name" jsonschema:"where the flow starts (same forms as codemap_symbol)"`
	Depth    int    `json:"depth,omitempty" jsonschema:"maximum depth (default 10)"`
	DataOnly bool   `json:"data_only,omitempty" jsonschema:"keep only the branches that reach a table or cross into Go"`
}

type impactInput struct {
	Name  string `json:"name" jsonschema:"the symbol about to change (same forms as codemap_symbol)"`
	Depth int    `json:"depth,omitempty" jsonschema:"how many levels upstream to walk (default 6)"`
}

type pathInput struct {
	Path string `json:"path" jsonschema:"a file's path in the repository, or a unique trailing part of it"`
}

type queryInput struct {
	Query string `json:"query" jsonschema:"a name or part of one: save, Cart, Cart.total"`
}

type usagesInput struct {
	Name string `json:"name" jsonschema:"the declaration's name, or Owner.name for a member"`
	Path string `json:"path,omitempty" jsonschema:"only the declarations in this file"`
}

// filesOverviewChars bounds codemap_files' answer, inside the tools' own clip.
const filesOverviewChars = 7_000

// NewMCPServer exposes the map of root as read-only MCP tools: the symbol graph's, and — when maps
// is given — the file index's (REPOMAP-010).
func NewMCPServer(ctx context.Context, service *Service, maps *repomap.Service, root, version string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: MCPServerName, Version: version}, &mcp.ServerOptions{Instructions: mcpInstructions})
	graph := func(callCtx context.Context) (*Graph, Status, error) { return awaitGraph(ctx, callCtx, service, root) }
	if maps != nil {
		addMapTools(server, maps, root)
	}

	addTool(server, graph, "codemap_overview",
		"The map at a glance: where the code is, the Go services Wails binds to the frontend, the endpoints, every table with its readers and writers, and the most depended-on symbols.",
		func(g *Graph, status Status, _ struct{}) (string, error) {
			return RenderOverview(g.Overview(status)), nil
		})

	addTool(server, graph, "codemap_search",
		"Find Go and TypeScript symbols and SQL tables by part of their name: exact, prefix, camel humps, substring.",
		func(g *Graph, _ Status, in searchInput) (string, error) {
			options := SearchOptions{Limit: in.Limit}
			if in.Kind != "" {
				kind, ok := ParseKind(in.Kind)
				if !ok {
					return "", fmt.Errorf("unknown kind %q", in.Kind) //nolint:err113 // names what was sent
				}
				options.Kind = kind
			}
			for lang, name := range langNames {
				if name == in.Lang {
					options.Lang = lang
				}
			}
			return RenderSearch(g.Search(in.Query, options)), nil
		})

	addTool(server, graph, "codemap_symbol",
		"One symbol: signature, doc, callers (including through an interface or an endpoint), callees, implementations, the tables it reaches and the tests that reach it.",
		func(g *Graph, _ Status, in nameInput) (string, error) { return RenderLookup(g.Lookup(in.Name)), nil })

	addTool(server, graph, "codemap_flow",
		"Everything a symbol leads to — calls, renders, interface dispatch, hops from TypeScript into Go — down to the SQL tables, with the site of every step.",
		func(g *Graph, _ Status, in flowInput) (string, error) {
			n, err := single(g, in.Name)
			if err != nil {
				return "", err
			}
			return RenderFlow(g.Flow(n, FlowOptions{Depth: in.Depth, DataOnly: in.DataOnly})), nil
		})

	addTool(server, graph, "codemap_impact",
		"What may break if a symbol changes: everything upstream level by level, where it surfaces (components, endpoints, bound methods), and the exact go test / vitest commands to run.",
		func(g *Graph, _ Status, in impactInput) (string, error) {
			n, err := single(g, in.Name)
			if err != nil {
				return "", err
			}
			return RenderImpact(g.Impact(n, in.Depth)), nil
		})
	return server
}

// single resolves a name to exactly one node, or explains why it cannot.
func single(g *Graph, name string) (*Node, error) {
	found := g.Resolve(name)
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return nil, fmt.Errorf("%w: %s", ErrUnknownSymbol, name)
	}
	refs := make([]SymbolRef, 0, min(len(found), 20))
	for _, n := range found[:min(len(found), 20)] {
		refs = append(refs, ref(n))
	}
	return nil, fmt.Errorf("%q names several symbols; ask again with one of these IDs:\n%s", name, renderIDs(refs)) //nolint:err113 // the candidates are the message
}

type graphFunc func(context.Context) (*Graph, Status, error)

// addTool registers a read-only tool whose answer is text, clipped to a size an agent can use.
func addTool[In any](server *mcp.Server, graph graphFunc, name, description string, answer func(*Graph, Status, In) (string, error)) {
	closedWorld := false
	tool := &mcp.Tool{
		Name:        name,
		Description: description,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld},
	}
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		g, status, err := graph(ctx)
		if err != nil {
			return nil, nil, err // the SDK turns it into an error result the model can read
		}
		text, err := answer(g, status, in)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: Clip(text, MaxTextChars)}}}, nil, nil
	})
}

// addMapTools registers the file index's four tools. The index reads its cache from disk, so even
// a cold call answers in the time it takes to look at what changed.
func addMapTools(server *mcp.Server, maps *repomap.Service, root string) {
	snapshot := func(ctx context.Context) (*repomap.Snapshot, error) {
		snap, _, err := maps.Snapshot(ctx, root)
		return snap, err
	}
	addSnapshotTool(server, snapshot, "codemap_files",
		"The repository at a glance, every language: its size and languages, its folders with their files and declarations, and the declarations other files use most.",
		func(s *repomap.Snapshot, _ struct{}) string { return s.Overview(filesOverviewChars) })
	addSnapshotTool(server, snapshot, "codemap_outline",
		"What one file declares (classes, functions, methods, types, with their lines), what it imports, and which files import it.",
		func(s *repomap.Snapshot, in pathInput) string { return s.OutlineText(in.Path) })
	addSnapshotTool(server, snapshot, "codemap_declarations",
		"Where a class, function, method or type is declared, by part of its name — closest and most used first — with its signature.",
		func(s *repomap.Snapshot, in queryInput) string { return s.DeclarationsText(in.Query) })
	addSnapshotTool(server, snapshot, "codemap_name_usages",
		"The files that use a declaration: confirmed when they import its file or share its package, by name only otherwise, and how many mean a different declaration of the name.",
		func(s *repomap.Snapshot, in usagesInput) string { return s.UsagesText(in.Name, in.Path) })
}

// addSnapshotTool registers a read-only tool answered from the file index.
func addSnapshotTool[In any](server *mcp.Server, snapshot func(context.Context) (*repomap.Snapshot, error), name, description string, answer func(*repomap.Snapshot, In) string) {
	closedWorld := false
	tool := &mcp.Tool{
		Name:        name,
		Description: description,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld},
	}
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		snap, err := snapshot(ctx)
		if err != nil {
			return nil, nil, err // the SDK turns it into an error result the model can read
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: Clip(answer(snap, in), MaxTextChars)}}}, nil, nil
	})
}

// awaitGraph answers the map, or errStillBuilding when a cold build takes longer than the patience.
// The build runs under the server's context, not the call's, so giving up on one call does not
// throw away the work the next one needs.
func awaitGraph(serverCtx, callCtx context.Context, service *Service, root string) (*Graph, Status, error) {
	type result struct {
		g      *Graph
		status Status
		err    error
	}
	done := make(chan result, 1)
	safego.Go("codemap-mcp-graph", func() {
		g, status, err := service.Graph(serverCtx, root) //nolint:contextcheck // the build outlives the call on purpose
		done <- result{g, status, err}
	})
	timer := time.NewTimer(graphPatience)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.g, r.status, r.err
	case <-timer.C:
		return nil, Status{}, errStillBuilding
	case <-callCtx.Done():
		return nil, Status{}, callCtx.Err()
	}
}

// RunMCP serves the map of the repository named by `--root` over stdio until the client goes away,
// and answers the process's exit code. maps is the file index, read-only in this process: it reads
// the cache the app keeps and writes nothing of the user's (CODEMAP-015).
func RunMCP(ctx context.Context, args []string, version string, maps *repomap.Service, stdin io.ReadCloser, stdout io.WriteCloser, stderr io.Writer) int {
	flags := flag.NewFlagSet(MCPCommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", "", "the repository to map")
	if err := flags.Parse(args); err != nil || *root == "" {
		_, _ = fmt.Fprintln(stderr, "usage: "+MCPCommand+" --root <repository>") // stderr is all a client shows
		return 2
	}
	service := NewService(DiscoverTools, nil)
	// Warm the map before the first question arrives; a failure here resurfaces on that question.
	safego.Go("codemap-mcp-warm", func() { _, _, _ = service.Graph(ctx, *root) })

	server := NewMCPServer(ctx, service, maps, *root, version)
	if err := server.Run(ctx, &mcp.IOTransport{Reader: stdin, Writer: stdout}); err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintln(stderr, "codemap mcp:", err)
		return 1
	}
	return 0
}
