package repomap

// Reading files into facts (REPOMAP-003).

// parser reads one file's declarations, imports and mentions into its facts. Each worker owns one:
// a tree-sitter parser keeps state between files and must not be shared.
type parser interface {
	parse(lang Lang, content []byte, facts *Facts)
	close()
}

// extract reads one file. It never fails: a file the grammar cannot read, or a language with no
// grammar, still has a language and a line count.
func extract(p parser, lang Lang, content []byte) *Facts {
	facts := &Facts{
		Lang:    lang,
		Lines:   countLines(content),
		Decls:   []Decl{},
		Imports: []Import{},
		Refs:    []Ref{},
	}
	p.parse(lang, content, facts)
	return facts
}
