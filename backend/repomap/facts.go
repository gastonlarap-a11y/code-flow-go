// Package repomap indexes a repository file by file — what each code file declares, imports and
// mentions — for the code map's drawn view (REPOMAP-001).
//
// It is the code map's second index, and a different kind from the first. Package codemap
// type-checks Go and TypeScript into a symbol graph and needs both toolchains installed; this one
// reads any repository with nothing installed. Six languages are parsed with tree-sitter's
// grammars, linked into the binary (TypeScript, JavaScript, Go, Python, Java, Dart); every other
// code file still appears on the map, with its lines and imports unknown. Files are the unit
// throughout: the map draws folders and files, a declaration is found by name, and a use is
// confirmed when the using file imports the declaring one.
package repomap

import "strings"

// Kind is what a declaration is, as the map names it.
type Kind string

// Declaration kinds, VERBATIM — the renderer labels them from this list.
const (
	KindClass     Kind = "class"
	KindInterface Kind = "interface"
	KindEnum      Kind = "enum"
	KindStruct    Kind = "struct"
	KindRecord    Kind = "record"
	KindMixin     Kind = "mixin"
	KindType      Kind = "type"
	KindFunction  Kind = "function"
	KindMethod    Kind = "method"
	KindConst     Kind = "const"
	// KindVar is a top-level variable: Go's `var`, a TypeScript or JavaScript `let`.
	KindVar Kind = "var"
)

// IsType says whether a kind can own members and appear in a class diagram.
func (k Kind) IsType() bool {
	switch k {
	case KindClass, KindInterface, KindEnum, KindStruct, KindRecord, KindMixin:
		return true
	}
	return false
}

// Member is a field or property of a type, as a class diagram lists it.
type Member struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Line       int    `json:"line"`
	Visibility string `json:"visibility"`
	Static     bool   `json:"static"`
}

// Decl is one declaration in a file.
type Decl struct {
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	// Line and EndLine are 1-based and inclusive.
	Line    int `json:"line"`
	EndLine int `json:"end_line"`
	// Signature is the declaration's head on one line — what a list shows next to the name.
	Signature string `json:"signature"`
	// Parent is the index, in the same file, of the declaration this one sits inside; -1 at the top.
	Parent int `json:"parent"`
	// Owner is the type a Go method is declared on, which may live in another file of its package.
	Owner      string `json:"owner"`
	Visibility string `json:"visibility"`
	Abstract   bool   `json:"abstract"`
	// Fields, Bases and Implements are only filled for a type: its members, what it extends, and
	// what it implements (for Dart, what it mixes in too).
	Fields     []Member `json:"fields"`
	Bases      []string `json:"bases"`
	Implements []string `json:"implements"`
}

// Import is one import as the file writes it.
type Import struct {
	Spec string `json:"spec"`
	Line int    `json:"line"`
}

// Ref is a name a file mentions, and the first line it does.
type Ref struct {
	Name string `json:"name"`
	Line int    `json:"line"`
}

// Facts is everything read from one file. It depends only on the file's content and language, which
// is what lets the cache key it by content.
type Facts struct {
	Lang  Lang `json:"lang"`
	Lines int  `json:"lines"`
	// Parsed is false when the language has no grammar here, or the build has none at all.
	Parsed bool `json:"parsed"`
	// Package is the file's own package or namespace, when its language declares one (Go, Java).
	Package string   `json:"package"`
	Decls   []Decl   `json:"decls"`
	Imports []Import `json:"imports"`
	Refs    []Ref    `json:"refs"`
}

// Lang is a source language, by the key the map stores.
type Lang string

// The languages the map knows. The first six are parsed; the rest appear as files.
const (
	LangTypeScript Lang = "typescript"
	LangTSX        Lang = "tsx"
	LangJavaScript Lang = "javascript"
	LangGo         Lang = "go"
	LangPython     Lang = "python"
	LangJava       Lang = "java"
	LangDart       Lang = "dart"
	LangKotlin     Lang = "kotlin"
	LangCSharp     Lang = "csharp"
	LangRust       Lang = "rust"
	LangPHP        Lang = "php"
	LangRuby       Lang = "ruby"
	LangSwift      Lang = "swift"
	LangC          Lang = "c"
	LangCPP        Lang = "cpp"
	LangScala      Lang = "scala"
	LangVue        Lang = "vue"
	LangSvelte     Lang = "svelte"
	LangSQL        Lang = "sql"
	LangShell      Lang = "shell"
)

// byExtension maps a file extension, lower case and with its dot, to its language.
var byExtension = map[string]Lang{
	".ts": LangTypeScript, ".mts": LangTypeScript, ".cts": LangTypeScript,
	".tsx": LangTSX,
	".js":  LangJavaScript, ".jsx": LangJavaScript, ".mjs": LangJavaScript, ".cjs": LangJavaScript,
	".go": LangGo,
	".py": LangPython, ".pyi": LangPython,
	".java": LangJava,
	".dart": LangDart,
	".kt":   LangKotlin, ".kts": LangKotlin,
	".cs":    LangCSharp,
	".rs":    LangRust,
	".php":   LangPHP,
	".rb":    LangRuby,
	".swift": LangSwift,
	".c":     LangC, ".h": LangC,
	".cc": LangCPP, ".cpp": LangCPP, ".cxx": LangCPP, ".hpp": LangCPP, ".hh": LangCPP,
	".scala":  LangScala,
	".vue":    LangVue,
	".svelte": LangSvelte,
	".sql":    LangSQL,
	".sh":     LangShell, ".bash": LangShell, ".zsh": LangShell,
}

// languageNames are the names a person reads.
var languageNames = map[Lang]string{
	LangTypeScript: "TypeScript", LangTSX: "TypeScript", LangJavaScript: "JavaScript", LangGo: "Go",
	LangPython: "Python", LangJava: "Java", LangDart: "Dart", LangKotlin: "Kotlin", LangCSharp: "C#",
	LangRust: "Rust", LangPHP: "PHP", LangRuby: "Ruby", LangSwift: "Swift", LangC: "C", LangCPP: "C++",
	LangScala: "Scala", LangVue: "Vue", LangSvelte: "Svelte", LangSQL: "SQL", LangShell: "Shell",
}

// languageOf answers a path's language, and false for a file that is not code.
func languageOf(path string) (Lang, bool) {
	dot := strings.LastIndexByte(path, '.')
	if dot < 0 || dot < strings.LastIndexByte(path, '/') {
		return "", false
	}
	lang, ok := byExtension[strings.ToLower(path[dot:])]
	return lang, ok
}

// Name is the language's name as a person reads it.
func (l Lang) Name() string {
	if name, ok := languageNames[l]; ok {
		return name
	}
	return string(l)
}

// countLines is how many lines a file has: a last line without a newline still counts.
func countLines(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	n := strings.Count(string(content), "\n")
	if content[len(content)-1] != '\n' {
		n++
	}
	return n
}
