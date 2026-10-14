package codemap

// What the code map's commands answer (CODEMAP-007 … CODEMAP-011). Every slice is built non-nil:
// the renderer calls `.map` on each one.

// SymbolRef names one node.
type SymbolRef struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Lang      string `json:"lang"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	EndLine   int    `json:"end_line"`
	Signature string `json:"signature"`
}

// Location is a place in a file.
type Location struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

// Relation is a neighbour of a symbol, the kind of edge that joins them, and where it happens.
type Relation struct {
	Symbol SymbolRef `json:"symbol"`
	Kind   string    `json:"kind"`
	File   string    `json:"file"`
	Line   int       `json:"line"`
	// Via is the interface method a caller reaches the symbol through, when it does.
	Via *SymbolRef `json:"via"`
}

// TableUse is how a symbol — itself, or through what it calls — uses one table.
type TableUse struct {
	Table   SymbolRef `json:"table"`
	Read    bool      `json:"read"`
	Write   bool      `json:"write"`
	Mention bool      `json:"mention"`
	Direct  bool      `json:"direct"`
	// Via is the nearest symbol that touches the table itself; nil when the symbol does.
	Via *SymbolRef `json:"via"`
}

// SymbolDetail is a symbol and its neighbourhood.
type SymbolDetail struct {
	Symbol SymbolRef  `json:"symbol"`
	Doc    string     `json:"doc"`
	Parent *SymbolRef `json:"parent"`
	// Bound names the Go service type Wails exposes this method on, "" when it is not bound.
	Bound           string      `json:"bound"`
	Callers         []Relation  `json:"callers"`
	Callees         []Relation  `json:"callees"`
	Implementations []Relation  `json:"implementations"`
	Implements      []Relation  `json:"implements"`
	Tables          []TableUse  `json:"tables"`
	Tests           []SymbolRef `json:"tests"`
	Unresolved      []Location  `json:"unresolved"`
}

// SymbolLookup answers a name: the symbol when it names exactly one, the candidates otherwise.
type SymbolLookup struct {
	Detail     *SymbolDetail `json:"detail"`
	Candidates []SymbolRef   `json:"candidates"`
}

// FlowNode is one step of a flow; Via is the kind of edge that led to it.
type FlowNode struct {
	Symbol   SymbolRef  `json:"symbol"`
	Via      string     `json:"via"`
	File     string     `json:"file"`
	Line     int        `json:"line"`
	Children []FlowNode `json:"children"`
	// Repeat marks a symbol already expanded earlier in the tree.
	Repeat bool `json:"repeat"`
	// Truncated marks a symbol cut off by the depth or the size budget while it still had steps.
	Truncated bool `json:"truncated"`
}

// Flow is the tree of everything a symbol leads to.
type Flow struct {
	Root      FlowNode `json:"root"`
	Steps     int      `json:"steps"`
	Truncated bool     `json:"truncated"`
}

// GoTestGroup is the Go tests of one package a change reaches, and the command that runs exactly
// them.
type GoTestGroup struct {
	PackageDir string   `json:"package_dir"`
	Tests      []string `json:"tests"`
	WorkingDir string   `json:"working_dir"`
	Command    string   `json:"command"`
}

// TSTestGroup is the TypeScript test files of one package a change reaches.
type TSTestGroup struct {
	PackageDir string   `json:"package_dir"`
	Files      []string `json:"files"`
	Runner     string   `json:"runner"`
	Command    string   `json:"command"`
}

// Impact is what may break when a symbol changes.
type Impact struct {
	Root SymbolRef `json:"root"`
	// Levels[i] holds the symbols i+1 steps upstream, tests excluded.
	Levels [][]SymbolRef `json:"levels"`
	// EntryPoints are where the change surfaces: components, endpoints, Wails-bound methods, main.
	EntryPoints []SymbolRef   `json:"entry_points"`
	GoTests     []GoTestGroup `json:"go_tests"`
	TSTests     []TSTestGroup `json:"ts_tests"`
	Truncated   bool          `json:"truncated"`
}

// Area is one folder's share of the map.
type Area struct {
	Path    string `json:"path"`
	Lang    string `json:"lang"`
	Symbols int    `json:"symbols"`
}

// BoundService is a Go type Wails exposes to TypeScript.
type BoundService struct {
	Name     string `json:"name"`
	File     string `json:"file"`
	Methods  int    `json:"methods"`
	CalledBy int    `json:"called_by"`
}

// TableSummary is one table and who touches it.
type TableSummary struct {
	Table   SymbolRef `json:"table"`
	Readers int       `json:"readers"`
	Writers int       `json:"writers"`
}

// Hotspot is a symbol many others depend on.
type Hotspot struct {
	Symbol  SymbolRef `json:"symbol"`
	Callers int       `json:"callers"`
}

// Overview is the whole map at a glance.
type Overview struct {
	Status    Status         `json:"status"`
	Areas     []Area         `json:"areas"`
	Services  []BoundService `json:"services"`
	Endpoints int            `json:"endpoints"`
	Tables    []TableSummary `json:"tables"`
	Hotspots  []Hotspot      `json:"hotspots"`
}

func ref(n *Node) SymbolRef {
	return SymbolRef{
		ID:        n.ID,
		Name:      n.Name,
		Kind:      n.Kind.String(),
		Lang:      n.Lang.String(),
		File:      n.Pos.File,
		Line:      n.Pos.Line,
		EndLine:   max(n.Pos.EndLine, n.Pos.Line),
		Signature: n.Signature,
	}
}

func refPtr(n *Node) *SymbolRef {
	if n == nil {
		return nil
	}
	r := ref(n)
	return &r
}
