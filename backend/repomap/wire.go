package repomap

// What the map's commands answer (REPOMAP-007). Every field is snake_case and no slice is nil: the
// renderer maps over them.

// MapNode is one card on the map: a folder, or a file.
type MapNode struct {
	// ID is the folder's or the file's path in the repository.
	ID     string `json:"id"`
	Label  string `json:"label"`
	Folder bool   `json:"folder"`
	Files  int    `json:"files"`
	// Declarations counts what its files declare, members included.
	Declarations int `json:"declarations"`
	Lines        int `json:"lines"`
	// Language is the one most of its files are written in.
	Language string `json:"language"`
	// ImportedBy counts the files outside the node that import something in it.
	ImportedBy int `json:"imported_by"`
	// Group is the top-level folder it belongs to, "" for a file at the root; the project view
	// colours by it.
	Group string `json:"group"`
}

// MapEdge is the imports from one node into another.
type MapEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Weight counts file-to-file imports between the two.
	Weight int `json:"weight"`
}

// LanguageCount is how many of the map's files a language has.
type LanguageCount struct {
	Language string `json:"language"`
	Files    int    `json:"files"`
}

// MapGraph is one view of the map.
type MapGraph struct {
	// Focus is the folder shown; "" is the repository, and also what the project view answers.
	Focus string    `json:"focus"`
	Nodes []MapNode `json:"nodes"`
	Edges []MapEdge `json:"edges"`
	// Files, Declarations and Languages describe the whole repository, whatever the focus.
	Files        int             `json:"files"`
	Declarations int             `json:"declarations"`
	Languages    []LanguageCount `json:"languages"`
}

// Build says how the map was read this time.
type Build struct {
	Files int `json:"files"`
	// Parsed counts the files read this time rather than taken from the cache.
	Parsed int   `json:"parsed"`
	Millis int64 `json:"millis"`
	// Grammars is false in a build without tree-sitter, where no file has declarations.
	Grammars bool `json:"grammars"`
	// Truncated counts the code files left out past the map's bound.
	Truncated int `json:"truncated"`
	// Git says git chose the files, honouring .gitignore and .gitattributes.
	Git bool `json:"git"`
}

// MapView is a graph and the build behind it.
type MapView struct {
	Graph MapGraph `json:"graph"`
	Build Build    `json:"build"`
}

// OutlineRow is one declaration in a file's outline, indented by depth.
type OutlineRow struct {
	Name      string `json:"name"`
	Kind      Kind   `json:"kind"`
	Line      int    `json:"line"`
	EndLine   int    `json:"end_line"`
	Signature string `json:"signature"`
	Depth     int    `json:"depth"`
}

// FileDetail is what one file declares and how it connects.
type FileDetail struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Lines    int    `json:"lines"`
	// Parsed is false for a language without a grammar here: its outline and imports are unknown.
	Parsed       bool         `json:"parsed"`
	Declarations []OutlineRow `json:"declarations"`
	Imports      []string     `json:"imports"`
	ImportedBy   []string     `json:"imported_by"`
	External     []string     `json:"external"`
}

// DeclHit is a declaration found by name.
type DeclHit struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Kind      Kind   `json:"kind"`
	Line      int    `json:"line"`
	EndLine   int    `json:"end_line"`
	Signature string `json:"signature"`
	// Parent is the name of the declaration it sits inside, "" at the top of its file.
	Parent string `json:"parent"`
	// UsedBy counts the other files that mention it and import its file (or share its package).
	UsedBy int `json:"used_by"`
}

// Usage is one file that mentions a declaration.
type Usage struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	// Within is the signature of the declaration the mention sits inside, "" at the top level.
	Within string `json:"within"`
	// Confirmed says the file imports the declaring one, or shares its package. Unconfirmed is a
	// mention by name only, kept because its file's imports are not all known.
	Confirmed bool `json:"confirmed"`
}

// UsageReport is everything that uses a name.
type UsageReport struct {
	Declarations []DeclHit `json:"declarations"`
	Usages       []Usage   `json:"usages"`
	// Excluded counts the files that mention the name but, as far as their known imports tell,
	// mean a different declaration of it.
	Excluded int `json:"excluded"`
}
