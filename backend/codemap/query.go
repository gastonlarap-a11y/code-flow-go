package codemap

import (
	"cmp"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// The questions the map answers (CODEMAP-007 … CODEMAP-011).

// ---------- finding a symbol ----------

// Resolve finds the nodes a person means: an ID, a full or trailing name ("CreateExpense",
// "Service.CreateExpense"), a TypeScript file path, or "file:line" for the innermost symbol there.
func (g *Graph) Resolve(name string) []*Node {
	name = strings.TrimSpace(name)
	if name == "" {
		return []*Node{}
	}
	if n := g.Nodes[name]; n != nil {
		return []*Node{n}
	}
	if n := g.Nodes["ts:"+name]; n != nil {
		return []*Node{n}
	}
	if file, line, ok := splitFileLine(name); ok {
		if n := g.innermostAt(file, line); n != nil {
			return []*Node{n}
		}
	}

	var exact, suffix, folded []*Node
	lower := strings.ToLower(name)
	for _, n := range g.Nodes {
		switch {
		case n.Name == name:
			exact = append(exact, n)
		case strings.HasSuffix(n.Name, "."+name):
			suffix = append(suffix, n)
		case strings.EqualFold(n.Name, name) || strings.HasSuffix(strings.ToLower(n.Name), "."+lower):
			folded = append(folded, n)
		}
	}
	for _, list := range [][]*Node{exact, suffix, folded} {
		if len(list) > 0 {
			sortNodes(list)
			return list
		}
	}
	return []*Node{}
}

func splitFileLine(s string) (string, int, bool) {
	i := strings.LastIndexByte(s, ':')
	if i <= 0 {
		return "", 0, false
	}
	line, err := strconv.Atoi(s[i+1:])
	if err != nil || line <= 0 {
		return "", 0, false
	}
	return s[:i], line, true
}

// innermostAt is the smallest symbol whose range holds file:line.
func (g *Graph) innermostAt(file string, line int) *Node {
	var best *Node
	for _, n := range g.Nodes {
		if n.Pos.File != file || n.Kind == KindModule || n.Kind == KindTestFile || n.Kind == KindTable || n.Kind == KindEndpoint {
			continue
		}
		end := max(n.Pos.EndLine, n.Pos.Line)
		if line < n.Pos.Line || line > end {
			continue
		}
		if best == nil || end-n.Pos.Line < max(best.Pos.EndLine, best.Pos.Line)-best.Pos.Line {
			best = n
		}
	}
	return best
}

func sortNodes(list []*Node) {
	slices.SortFunc(list, func(a, b *Node) int {
		return cmp.Or(cmp.Compare(kindRank(a.Kind), kindRank(b.Kind)), cmp.Compare(len(a.Name), len(b.Name)), cmp.Compare(a.ID, b.ID))
	})
}

// kindRank orders kinds by how likely a person is looking for them.
func kindRank(k Kind) int {
	switch k {
	case KindMethod, KindComponent, KindFunc, KindInterfaceMethod:
		return 0
	case KindHook, KindType, KindInterface, KindEndpoint:
		return 1
	case KindTable:
		return 2
	case KindModule:
		return 3
	}
	return 4
}

// SearchOptions narrows a search.
type SearchOptions struct {
	Kind  Kind // 0 = any
	Lang  Lang // 0 = any
	Limit int  // default 30
}

// Search ranks nodes by how well their name matches the query: exact, prefix, camel-hump initials
// ("CE" → CreateExpense), substring, then subsequence.
func (g *Graph) Search(query string, options SearchOptions) []SymbolRef {
	query = strings.TrimSpace(query)
	if query == "" {
		return []SymbolRef{}
	}
	if options.Limit <= 0 {
		options.Limit = 30
	}
	type hit struct {
		n     *Node
		score int
	}
	hits := make([]hit, 0, 64)
	for _, n := range g.Nodes {
		if (options.Kind != 0 && n.Kind != options.Kind) || (options.Lang != 0 && n.Lang != options.Lang) {
			continue
		}
		if score := matchScore(n.Name, query); score > 0 {
			hits = append(hits, hit{n, score*10 - kindRank(n.Kind)})
		}
	}
	slices.SortFunc(hits, func(a, b hit) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(len(a.n.Name), len(b.n.Name)), cmp.Compare(a.n.ID, b.n.ID))
	})
	out := make([]SymbolRef, 0, min(len(hits), options.Limit))
	for _, h := range hits[:min(len(hits), options.Limit)] {
		out = append(out, ref(h.n))
	}
	return out
}

func matchScore(name, query string) int {
	last := name[strings.LastIndexAny(name, "./")+1:]
	ln, lq, ll := strings.ToLower(name), strings.ToLower(query), strings.ToLower(last)
	switch {
	case ll == lq || ln == lq:
		return 10
	case strings.HasPrefix(ll, lq):
		return 8
	case len(query) > 1 && strings.HasPrefix(humps(last), strings.ToUpper(query)):
		return 6
	case strings.Contains(ln, lq):
		return 4
	case isSubsequence(ln, lq):
		return 2
	}
	return 0
}

// humps is the initials of a camelCase or snake_case word: CreateExpense → CE.
func humps(word string) string {
	var b strings.Builder
	prev := '_'
	for _, r := range word {
		if unicode.IsUpper(r) || (prev == '_' && r != '_') {
			b.WriteRune(unicode.ToUpper(r))
		}
		prev = r
	}
	return b.String()
}

func isSubsequence(s, sub string) bool {
	want := []rune(sub)
	i := 0
	for _, r := range s {
		if i < len(want) && r == want[i] {
			i++
		}
	}
	return i == len(want)
}

// ---------- a symbol's neighbourhood ----------

// maxNeighbours bounds each list of a symbol's detail: a logging helper called from four hundred
// places does not need four hundred rows to make the point.
const maxNeighbours = 200

// Lookup answers a name with the symbol's detail when it names exactly one, and the candidates when
// it names several.
func (g *Graph) Lookup(name string) SymbolLookup {
	found := g.Resolve(name)
	lookup := SymbolLookup{Candidates: make([]SymbolRef, 0, len(found))}
	if len(found) == 1 {
		detail := g.Detail(found[0])
		lookup.Detail = &detail
		return lookup
	}
	for _, n := range found[:min(len(found), 50)] {
		lookup.Candidates = append(lookup.Candidates, ref(n))
	}
	return lookup
}

// isCallEdge reports whether an edge is one symbol using another.
func isCallEdge(k EdgeKind) bool {
	return k == EdgeCall || k == EdgeRef || k == EdgeRender || k == EdgeCross
}

// Detail is a symbol and everything next to it.
func (g *Graph) Detail(n *Node) SymbolDetail {
	detail := SymbolDetail{
		Symbol:          ref(n),
		Doc:             n.Doc,
		Parent:          refPtr(g.Nodes[n.Parent]),
		Bound:           n.Bound,
		Callers:         make([]Relation, 0, 8),
		Callees:         make([]Relation, 0, 8),
		Implementations: make([]Relation, 0),
		Implements:      make([]Relation, 0),
		Tables:          g.tables(n),
		Tests:           g.tests(n),
		Unresolved:      make([]Location, 0, len(n.Unresolved)),
	}
	// One row per neighbour — its first site — and no tests among the callers: they have their own
	// list, and a test file calling a method twenty times would otherwise bury the real callers.
	callers, callees := map[string]bool{}, map[string]bool{}
	addCaller := func(from *Node, e Edge, via *Node) {
		key := from.ID
		if via != nil {
			key += "|" + via.ID
		}
		if isTestNode(from) || callers[key] {
			return
		}
		callers[key] = true
		detail.Callers = appendRelation(detail.Callers, from, e, via)
	}
	for _, e := range g.In[n.ID] {
		switch {
		case isCallEdge(e.Kind):
			addCaller(g.Nodes[e.From], e, nil)
		case e.Kind == EdgeDispatch:
			hub := g.Nodes[e.From]
			detail.Implements = appendRelation(detail.Implements, hub, e, nil)
			// Whoever calls the interface method — or the endpoint — reaches this one through it.
			for _, upstream := range g.In[hub.ID] {
				if isCallEdge(upstream.Kind) {
					addCaller(g.Nodes[upstream.From], upstream, hub)
				}
			}
		}
	}
	for _, e := range g.Out[n.ID] {
		switch {
		case isCallEdge(e.Kind) && !callees[e.To]:
			callees[e.To] = true
			detail.Callees = appendRelation(detail.Callees, g.Nodes[e.To], e, nil)
		case e.Kind == EdgeDispatch:
			detail.Implementations = appendRelation(detail.Implementations, g.Nodes[e.To], e, nil)
		}
	}
	for _, at := range n.Unresolved {
		detail.Unresolved = append(detail.Unresolved, Location{File: at.File, Line: at.Line})
	}
	return detail
}

func appendRelation(list []Relation, n *Node, e Edge, via *Node) []Relation {
	if n == nil || len(list) >= maxNeighbours {
		return list
	}
	return append(list, Relation{Symbol: ref(n), Kind: e.Kind.String(), File: e.At.File, Line: e.At.Line, Via: refPtr(via)})
}

// isTestNode reports whether a node is test code: a Go test, a TypeScript test file, or a symbol
// declared inside one.
func isTestNode(n *Node) bool {
	return n.Kind.IsTest() || strings.HasSuffix(n.Pos.File, "_test.go") || tsTestFileRe(n.Pos.File)
}

func tsTestFileRe(file string) bool {
	base := path.Base(file)
	for _, marker := range []string{".test.", ".spec."} {
		if strings.Contains(base, marker) {
			return true
		}
	}
	return strings.Contains(file, "/__tests__/")
}

// maxWalk bounds the breadth-first walks behind tables and tests.
const maxWalk = 20_000

// tables lists the tables n reaches through what it calls, nearest first.
func (g *Graph) tables(n *Node) []TableUse {
	uses := map[string]*TableUse{}
	seen := map[string]bool{n.ID: true}
	queue := []string{n.ID}
	for len(queue) > 0 && len(seen) < maxWalk {
		current := queue[0]
		queue = queue[1:]
		for _, e := range g.Out[current] {
			to := g.Nodes[e.To]
			if to.Kind == KindTable {
				use := uses[to.ID]
				if use == nil {
					use = &TableUse{Table: ref(to), Direct: current == n.ID}
					if !use.Direct {
						use.Via = refPtr(g.Nodes[current])
					}
					uses[to.ID] = use
				}
				use.Read = use.Read || e.Kind == EdgeRead
				use.Write = use.Write || e.Kind == EdgeWrite
				use.Mention = use.Mention || e.Kind == EdgeMention
				continue
			}
			if !e.Kind.IsFlow() || isTestNode(to) || seen[to.ID] {
				continue
			}
			seen[to.ID] = true
			queue = append(queue, to.ID)
		}
	}
	out := make([]TableUse, 0, len(uses))
	for _, use := range uses {
		out = append(out, *use)
	}
	slices.SortFunc(out, func(a, b TableUse) int {
		return cmp.Or(compareBool(b.Direct, a.Direct), compareBool(b.Write, a.Write), cmp.Compare(a.Table.Name, b.Table.Name))
	})
	return out
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// maxTests bounds the tests a detail lists.
const maxTests = 50

// tests lists the tests that reach n, nearest first.
func (g *Graph) tests(n *Node) []SymbolRef {
	out := make([]SymbolRef, 0, 8)
	seen := map[string]bool{n.ID: true}
	frontier := []string{n.ID}
	for depth := 0; depth < 6 && len(frontier) > 0 && len(out) < maxTests; depth++ {
		var next []string
		for _, id := range frontier {
			for _, e := range g.In[id] {
				if e.Kind.IsData() || seen[e.From] {
					continue
				}
				seen[e.From] = true
				from := g.Nodes[e.From]
				if from.Kind.IsTest() {
					if len(out) < maxTests {
						out = append(out, ref(from))
					}
					continue
				}
				next = append(next, from.ID)
			}
		}
		frontier = next
	}
	return out
}

// ---------- flow ----------

// FlowOptions shape a flow.
type FlowOptions struct {
	// Depth limits the tree (default 10). A component reaching a table through a store, an IPC
	// wrapper, an endpoint, a handler and a repository is eight or nine steps; the budget, not the
	// depth, is what keeps a wide flow small.
	Depth int
	// Budget caps the number of steps (default 400).
	Budget int
	// DataOnly keeps only the branches that reach a table or cross the language boundary.
	DataOnly bool
}

// Flow walks forward from n: what it calls, renders and dispatches to, across the language
// boundary, down to the tables.
func (g *Graph) Flow(n *Node, options FlowOptions) Flow {
	if options.Depth <= 0 {
		options.Depth = 10
	}
	if options.Budget <= 0 {
		options.Budget = 400
	}
	f := &flower{g: g, options: options, expanded: map[string]bool{}, memo: map[string]int8{}}
	root := FlowNode{Symbol: ref(n), Children: []FlowNode{}}
	f.expand(&root, n.ID, 0)
	return Flow{Root: root, Steps: f.steps, Truncated: f.truncated}
}

type flower struct {
	g         *Graph
	options   FlowOptions
	expanded  map[string]bool
	memo      map[string]int8 // 1 reaches data, -1 does not, 2 in progress
	steps     int
	truncated bool
}

func (f *flower) expand(fn *FlowNode, id string, depth int) {
	f.expanded[id] = true
	for _, e := range f.g.Out[id] {
		to := f.g.Nodes[e.To]
		if isTestNode(to) || to.Kind == KindModule {
			continue
		}
		if f.options.DataOnly && !f.reachesData(e) {
			continue
		}
		if depth >= f.options.Depth || f.steps >= f.options.Budget {
			fn.Truncated = true
			f.truncated = true
			return
		}
		f.steps++
		child := FlowNode{Symbol: ref(to), Via: e.Kind.String(), File: e.At.File, Line: e.At.Line, Children: []FlowNode{}}
		if f.expanded[to.ID] {
			child.Repeat = len(f.g.Out[to.ID]) > 0
		} else if !e.Kind.IsData() {
			f.expand(&child, to.ID, depth+1)
		}
		fn.Children = append(fn.Children, child)
	}
}

// reachesData reports whether following e can reach a table or a hop across languages. The handler
// behind an endpoint counts too: a flow that crosses into Go should show where it lands.
func (f *flower) reachesData(e Edge) bool {
	if e.Kind.IsData() || e.Kind == EdgeCross || f.g.Nodes[e.From].Kind == KindEndpoint {
		return true
	}
	switch f.memo[e.To] {
	case 1:
		return true
	case -1, 2:
		return false
	}
	f.memo[e.To] = 2
	for _, next := range f.g.Out[e.To] {
		if !isTestNode(f.g.Nodes[next.To]) && f.reachesData(next) {
			f.memo[e.To] = 1
			return true
		}
	}
	f.memo[e.To] = -1
	return false
}

// ---------- impact ----------

// maxImpact bounds how many upstream symbols an impact lists.
const maxImpact = 400

// Impact walks upstream from n — callers, renderers, references, and from an implementation to
// the interface method or endpoint it serves — up to depth steps (default 6).
func (g *Graph) Impact(n *Node, depth int) Impact {
	if depth <= 0 {
		depth = 6
	}
	impact := Impact{Root: ref(n), Levels: make([][]SymbolRef, 0, depth), EntryPoints: make([]SymbolRef, 0, 8)}
	goTests := map[string][]string{}
	tsTests := map[string]bool{}
	seen := map[string]bool{n.ID: true}
	frontier := []string{n.ID}
	listed := 0

	for level := 0; level < depth && len(frontier) > 0; level++ {
		var next []string
		nodes := make([]*Node, 0, 8)
		for _, id := range frontier {
			for _, e := range g.In[id] {
				if e.Kind.IsData() || seen[e.From] {
					continue
				}
				seen[e.From] = true
				from := g.Nodes[e.From]
				switch {
				case from.Kind == KindTestFunc:
					dir := path.Dir(from.Pos.File)
					goTests[dir] = append(goTests[dir], from.Name[strings.LastIndexByte(from.Name, '.')+1:])
					continue
				case from.Lang == LangTS && tsTestFileRe(from.Pos.File):
					tsTests[from.Pos.File] = true
					continue
				case strings.HasSuffix(from.Pos.File, "_test.go"):
					next = append(next, from.ID) // a test helper: its tests are one step up
					continue
				}
				next = append(next, from.ID)
				if listed >= maxImpact {
					impact.Truncated = true
					continue
				}
				listed++
				nodes = append(nodes, from)
				if isEntryPoint(from) {
					impact.EntryPoints = append(impact.EntryPoints, ref(from))
				}
			}
		}
		if len(nodes) > 0 {
			sortNodes(nodes)
			refs := make([]SymbolRef, 0, len(nodes))
			for _, node := range nodes {
				refs = append(refs, ref(node))
			}
			impact.Levels = append(impact.Levels, refs)
		}
		frontier = next
	}
	if len(frontier) > 0 {
		impact.Truncated = true
	}
	impact.GoTests = g.goTestGroups(goTests)
	impact.TSTests = g.tsTestGroups(tsTests)
	return impact
}

// isEntryPoint reports whether a change surfaces at n: what a person sees or calls from outside.
func isEntryPoint(n *Node) bool {
	switch {
	case n.Kind == KindComponent, n.Kind == KindEndpoint, n.Bound != "":
		return true
	case n.Lang == LangGo && n.Kind == KindFunc && strings.HasSuffix(n.Name, ".main"):
		return true
	}
	return false
}

// goTestGroups turns the reached Go tests into one `go test -run` per package, run from the module
// that holds it.
func (g *Graph) goTestGroups(byDir map[string][]string) []GoTestGroup {
	groups := make([]GoTestGroup, 0, len(byDir))
	for dir, tests := range byDir {
		slices.Sort(tests)
		tests = slices.Compact(tests)
		module := longestPrefix(g.Meta.GoModules, dir)
		pkg := strings.TrimPrefix(strings.TrimPrefix(dir, module), "/")
		target := "./" + pkg
		if pkg == "" || pkg == "." {
			target = "."
		}
		groups = append(groups, GoTestGroup{
			PackageDir: dir,
			Tests:      tests,
			WorkingDir: module,
			Command:    "go test -run '^(" + strings.Join(tests, "|") + ")$' " + target,
		})
	}
	slices.SortFunc(groups, func(a, b GoTestGroup) int { return cmp.Compare(a.PackageDir, b.PackageDir) })
	return groups
}

// tsTestGroups groups the reached test files by the package whose runner runs them.
func (g *Graph) tsTestGroups(files map[string]bool) []TSTestGroup {
	byPackage := map[string][]string{}
	for file := range files {
		dirs := make([]string, 0, len(g.Meta.TestRunners))
		for dir := range g.Meta.TestRunners {
			dirs = append(dirs, dir)
		}
		pkg := longestPrefix(dirs, file)
		byPackage[pkg] = append(byPackage[pkg], file)
	}
	groups := make([]TSTestGroup, 0, len(byPackage))
	for pkg, list := range byPackage {
		slices.Sort(list)
		relative := make([]string, 0, len(list))
		for _, file := range list {
			relative = append(relative, strings.TrimPrefix(strings.TrimPrefix(file, pkg), "/"))
		}
		group := TSTestGroup{PackageDir: pkg, Files: list, Runner: g.Meta.TestRunners[pkg]}
		switch group.Runner {
		case "vitest":
			group.Command = "npx vitest run " + strings.Join(relative, " ")
		case "jest":
			group.Command = "npx jest " + strings.Join(relative, " ")
		}
		groups = append(groups, group)
	}
	slices.SortFunc(groups, func(a, b TSTestGroup) int { return cmp.Compare(a.PackageDir, b.PackageDir) })
	return groups
}

// longestPrefix answers the directory in dirs that holds file most closely, "" (the root) when none
// does.
func longestPrefix(dirs []string, file string) string {
	best := ""
	for _, dir := range dirs {
		if dir != "" && (file == dir || strings.HasPrefix(file, dir+"/")) && len(dir) > len(best) {
			best = dir
		}
	}
	return best
}

// ---------- overview ----------

const (
	maxAreas    = 30
	maxHotspots = 15
)

// Overview is the map at a glance: where the code is, what Go exposes to TypeScript, which tables
// exist and who touches them, and what everything depends on.
func (g *Graph) Overview(status Status) Overview {
	overview := Overview{
		Status:   status,
		Areas:    make([]Area, 0, maxAreas),
		Services: make([]BoundService, 0),
		Tables:   make([]TableSummary, 0),
		Hotspots: make([]Hotspot, 0, maxHotspots),
	}

	areas := map[[2]string]int{}
	services := map[string]*BoundService{}
	callers := map[string]map[string]bool{}
	hotspots := make([]Hotspot, 0, 64)
	for _, n := range g.Nodes {
		switch n.Kind {
		case KindEndpoint:
			overview.Endpoints++
		case KindTable:
			summary := TableSummary{Table: ref(n)}
			for _, e := range g.In[n.ID] {
				switch e.Kind {
				case EdgeRead:
					summary.Readers++
				case EdgeWrite:
					summary.Writers++
				}
			}
			overview.Tables = append(overview.Tables, summary)
			continue
		}
		if isTestNode(n) || n.Kind == KindModule || n.Pos.File == "" {
			continue
		}
		areas[[2]string{path.Dir(n.Pos.File), n.Lang.String()}]++

		if n.Bound != "" {
			service := services[n.Parent]
			if service == nil {
				service = &BoundService{Name: n.Bound}
				if parent := g.Nodes[n.Parent]; parent != nil {
					service.File = parent.Pos.File
				}
				services[n.Parent] = service
				callers[n.Parent] = map[string]bool{}
			}
			service.Methods++
			for _, e := range g.In[n.ID] {
				if e.Kind == EdgeCross {
					callers[n.Parent][e.From] = true
				}
			}
		}

		distinct := map[string]bool{}
		for _, e := range g.In[n.ID] {
			if isCallEdge(e.Kind) && !isTestNode(g.Nodes[e.From]) {
				distinct[e.From] = true
			}
		}
		if len(distinct) > 1 {
			hotspots = append(hotspots, Hotspot{Symbol: ref(n), Callers: len(distinct)})
		}
	}

	for key, count := range areas {
		overview.Areas = append(overview.Areas, Area{Path: key[0], Lang: key[1], Symbols: count})
	}
	slices.SortFunc(overview.Areas, func(a, b Area) int {
		return cmp.Or(cmp.Compare(b.Symbols, a.Symbols), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Lang, b.Lang))
	})
	overview.Areas = overview.Areas[:min(len(overview.Areas), maxAreas)]

	for id, service := range services {
		service.CalledBy = len(callers[id])
		overview.Services = append(overview.Services, *service)
	}
	slices.SortFunc(overview.Services, func(a, b BoundService) int { return cmp.Compare(a.Name, b.Name) })

	slices.SortFunc(overview.Tables, func(a, b TableSummary) int {
		return cmp.Or(cmp.Compare(b.Readers+b.Writers, a.Readers+a.Writers), cmp.Compare(a.Table.Name, b.Table.Name))
	})

	slices.SortFunc(hotspots, func(a, b Hotspot) int {
		return cmp.Or(cmp.Compare(b.Callers, a.Callers), cmp.Compare(a.Symbol.ID, b.Symbol.ID))
	})
	overview.Hotspots = append(overview.Hotspots, hotspots[:min(len(hotspots), maxHotspots)]...)
	return overview
}
