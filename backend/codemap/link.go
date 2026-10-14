package codemap

import (
	"maps"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Putting the halves together, and the links across the language boundary (CODEMAP-004,
// CODEMAP-006).

// assemble builds a fresh graph from the two halves. Nodes are copied: the halves are kept and
// reused by later rebuilds, and a finished graph is shared with concurrent readers.
func assemble(replayed tableSet, goSide *goHalf, tsSide *tsExtract, meta Meta) *Graph {
	g := NewGraph()
	g.Meta = meta

	tables := tableSet{}
	maps.Copy(tables, replayed)
	if goSide != nil {
		for _, s := range goSide.strings {
			addCreatedTables(tables, s.text, s.at)
		}
	}
	if tsSide != nil {
		for _, s := range tsSide.Strings {
			addCreatedTables(tables, s.Text, Pos{File: s.File, Line: s.Line})
		}
	}
	// A model whose table is written down (a bun tag, a TableName method) names its table even when
	// no DDL in the repository creates it. A name only derived from a convention needs the schema
	// to confirm it: an edge to a table that is not there is dropped with it.
	if goSide != nil {
		for _, e := range goSide.edges {
			name, ok := strings.CutPrefix(e.To, "sql:")
			if !ok || goSide.guessedTables[name] {
				continue
			}
			if _, known := tables[name]; !known {
				tables[name] = Pos{}
			}
		}
	}
	for name, at := range tables {
		g.AddNode(&Node{ID: tableID(name), Name: name, Lang: LangSQL, Kind: KindTable, Pos: at})
	}

	if goSide != nil {
		for _, n := range goSide.nodes {
			copied := *n
			copied.Unresolved = append([]Pos(nil), n.Unresolved...)
			g.AddNode(&copied)
		}
	}
	if tsSide != nil {
		for _, s := range tsSide.Symbols {
			if n := s.node(); n.Kind != 0 {
				g.AddNode(n)
			}
		}
	}

	if goSide != nil {
		for _, e := range goSide.edges {
			g.AddEdge(e)
		}
		for _, s := range goSide.strings {
			if s.from != "" {
				linkSQL(g, tables, s.from, s.text, s.at)
			}
		}
	}
	if tsSide != nil {
		for _, e := range tsSide.Edges {
			if kind, ok := tsEdgeKinds[e.Kind]; ok {
				g.AddEdge(Edge{From: e.From, To: e.To, Kind: kind, At: Pos{File: e.File, Line: e.Line}})
			}
		}
		for _, s := range tsSide.Strings {
			linkSQL(g, tables, s.From, s.Text, Pos{File: s.File, Line: s.Line})
		}
		linkTableCalls(g, tables, tsSide.Calls)
		if goSide != nil {
			linkBindings(g, goSide, tsSide)
			linkEndpoints(g, goSide.registrations, tsSide.Calls)
		}
	}

	g.Finish()
	return g
}

// linkSQL classifies one string a symbol uses against the known tables.
func linkSQL(g *Graph, tables tableSet, from, text string, at Pos) {
	if !looksLikeSQL(text, tables) {
		return
	}
	accesses, dynamic := classifySQL(text, tables)
	for _, access := range accesses {
		g.AddEdge(Edge{From: from, To: tableID(access.table), Kind: access.kind, At: at})
	}
	if n := g.Nodes[from]; dynamic && n != nil {
		n.Unresolved = append(n.Unresolved, at)
	}
}

var (
	readCalleeRe  = regexp.MustCompile(`(?i)select|from|query|find|get|read|list|count|all`)
	writeCalleeRe = regexp.MustCompile(`(?i)insert|update|delete|upsert|into|remove|save|create|put|replace`)
)

// linkTableCalls handles a table named as a call's first argument — `knex("users")`,
// `db.selectFrom("users")`, `from("users")` — reading what the call does from its name.
func linkTableCalls(g *Graph, tables tableSet, calls []tsCall) {
	for _, call := range calls {
		name := strings.ToLower(call.Value)
		if _, known := tables[name]; !known {
			continue
		}
		kind := EdgeMention
		switch {
		case writeCalleeRe.MatchString(call.Callee):
			kind = EdgeWrite
		case readCalleeRe.MatchString(call.Callee):
			kind = EdgeRead
		}
		g.AddEdge(Edge{From: call.From, To: tableID(name), Kind: kind, At: Pos{File: call.File, Line: call.Line}})
	}
}

// linkBindings joins TypeScript to the Go methods Wails binds, by the binding's own import path:
// the directory under `bindings/` is the Go package, the imported name (or the file) the service,
// the call the method. Nothing is matched by a bare method name.
func linkBindings(g *Graph, goSide *goHalf, tsSide *tsExtract) {
	for _, b := range tsSide.Bindings {
		if to, ok := goSide.boundMethod(b.Specifier, b.Name, b.Member); ok {
			g.AddEdge(Edge{From: b.From, To: to, Kind: EdgeCross, At: Pos{File: b.File, Line: b.Line}})
		}
	}

	members := map[string][]*Node{}
	for _, n := range g.Nodes {
		if n.Kind == KindInterfaceMethod && n.Lang == LangTS {
			members[n.Parent] = append(members[n.Parent], n)
		}
	}
	for _, alias := range tsSide.BindingAliases {
		for _, member := range members[alias.Interface] {
			method := member.Name[strings.LastIndexByte(member.Name, '.')+1:]
			if to, ok := goSide.boundMethod(alias.Specifier, alias.Name, method); ok {
				g.AddEdge(Edge{From: member.ID, To: to, Kind: EdgeCross, At: g.Nodes[to].Pos})
			}
		}
	}
}

// boundMethod finds the Go method a binding names. The import may name the package (and the
// service by its exported name) or the service's own file.
func (h *goHalf) boundMethod(specifier, name, member string) (string, bool) {
	candidates := []boundKey{
		{pkg: path.Dir(specifier), typ: strings.ToLower(path.Base(specifier)), method: member},
	}
	if name != "" {
		candidates = append([]boundKey{{pkg: specifier, typ: strings.ToLower(name), method: member}}, candidates...)
	}
	for _, key := range candidates {
		if id, ok := h.bound[key]; ok {
			return id, true
		}
	}
	return "", false
}

// linkEndpoints joins a TypeScript call naming a key — `invoke("diagram_list_documents")`,
// `fetch("/api/items/" + id)` — to the Go handler registered under the same key. The endpoint gets
// a node of its own, so a flow shows the hop and a search finds it.
func linkEndpoints(g *Graph, registrations []registration, calls []tsCall) {
	if len(registrations) == 0 {
		return
	}
	exact := map[string][]registration{}
	var routes []registration
	for _, r := range registrations {
		if g.Nodes[r.handler] == nil {
			continue
		}
		if routeKeyRe.MatchString(r.key) {
			routes = append(routes, r)
			continue
		}
		exact[r.key] = append(exact[r.key], r)
	}

	for _, call := range calls {
		if g.Nodes[call.From] == nil {
			continue
		}
		matched := exact[call.Value]
		if len(matched) == 0 && looksLikeURL(call.Value) {
			requested := routeSegments(call.Value)
			for _, r := range routes {
				if route := routeSegments(r.key); hasLiteralSegment(route) && segmentsMatch(route, requested) {
					matched = append(matched, r)
				}
			}
		}
		for _, r := range matched {
			id := "ep:" + r.key
			g.AddNode(&Node{ID: id, Name: r.key, Lang: LangGo, Kind: KindEndpoint, Pos: r.at, Signature: "endpoint " + r.key})
			g.AddEdge(Edge{From: id, To: r.handler, Kind: EdgeDispatch, At: r.at})
			g.AddEdge(Edge{From: call.From, To: id, Kind: EdgeCross, At: Pos{File: call.File, Line: call.Line}})
		}
	}
}

// routeSegments splits a route or a requested URL into its path segments, with every parameter —
// `{id}`, `:id`, `*`, a TypeScript template hole — reduced to "*".
func routeSegments(raw string) []string {
	value := raw
	if method, rest, ok := strings.Cut(value, " "); ok && method == strings.ToUpper(method) {
		value = rest
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		value = parsed.Path
	}
	value, _, _ = strings.Cut(value, "?")
	value, _, _ = strings.Cut(value, "#")
	segments := strings.Split(strings.Trim(value, "/"), "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, "{") || strings.HasPrefix(segment, ":") || segment == "*" || strings.Contains(segment, "{}") {
			segments[i] = "*"
		}
	}
	return segments
}

// looksLikeURL keeps a string that is a path or a URL with at least one named segment: "/" alone,
// or a separator handed to split, is not a request.
func looksLikeURL(value string) bool {
	if !strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		return false
	}
	return hasLiteralSegment(routeSegments(value))
}

// hasLiteralSegment rejects a route made only of parameters, which would match every request of its
// length.
func hasLiteralSegment(segments []string) bool {
	for _, segment := range segments {
		if segment != "" && segment != "*" {
			return true
		}
	}
	return false
}

func segmentsMatch(route, requested []string) bool {
	if len(route) != len(requested) {
		return false
	}
	for i := range route {
		if route[i] != requested[i] && route[i] != "*" && requested[i] != "*" {
			return false
		}
	}
	return true
}
