//go:build cgo

package repomap

import (
	"cmp"
	"embed"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	tsdart "github.com/UserNobody14/tree-sitter-dart/bindings/go"
	ts "github.com/tree-sitter/go-tree-sitter"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tsjava "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tsjs "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tspy "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// Reading a file with tree-sitter (REPOMAP-003). The grammars are the official ones, linked into
// the binary; what is read from each tree is decided by this package's own queries, one folder per
// language under queries/: the declarations, the imports, and every name mentioned. What a query
// cannot say well — a type's members, what it extends, who can see it — is read from the
// declaration's node by the language's own reader below.

// GrammarsLinked says whether this build parses any language.
const GrammarsLinked = true

// extractorID names what produced a cache's facts — the grammars and the queries' revision. Bump
// the revision whenever a query or a reader changes what it reads, so a kept map is read afresh.
const extractorID = "treesitter/3 ts-0.23.2 js-0.25.0 go-0.25.0 py-0.25.0 java-0.23.5 dart-be07cf7"

//go:embed queries
var queryFiles embed.FS

// grammar is how one language is read.
type grammar struct {
	queries  string
	language func() *ts.Language
	members  func(r *reader, node *ts.Node, d *Decl)
}

var grammars = map[Lang]grammar{
	LangTypeScript: {"typescript", func() *ts.Language { return ts.NewLanguage(tsts.LanguageTypescript()) }, tsMembers},
	LangTSX:        {"typescript", func() *ts.Language { return ts.NewLanguage(tsts.LanguageTSX()) }, tsMembers},
	LangJavaScript: {"javascript", func() *ts.Language { return ts.NewLanguage(tsjs.Language()) }, jsMembers},
	LangGo:         {"go", func() *ts.Language { return ts.NewLanguage(tsgo.Language()) }, goMembers},
	LangPython:     {"python", func() *ts.Language { return ts.NewLanguage(tspy.Language()) }, pythonMembers},
	LangJava:       {"java", func() *ts.Language { return ts.NewLanguage(tsjava.Language()) }, javaMembers},
	LangDart:       {"dart", func() *ts.Language { return ts.NewLanguage(tsdart.Language()) }, dartMembers},
}

// compiled is a language's three queries, ready to run.
type compiled struct {
	language             *ts.Language
	decls, imports, refs *ts.Query
}

func (c *compiled) close() {
	for _, q := range []*ts.Query{c.decls, c.imports, c.refs} {
		if q != nil {
			q.Close()
		}
	}
}

// compile builds a language's queries. A query that does not compile is a programming error the
// tests catch; at run time the language is then read as unparsed rather than failing the map.
func compile(g grammar) (*compiled, error) {
	c := &compiled{language: g.language()}
	for _, part := range []struct {
		file string
		into **ts.Query
	}{{"decls.scm", &c.decls}, {"imports.scm", &c.imports}, {"refs.scm", &c.refs}} {
		source, err := queryFiles.ReadFile("queries/" + g.queries + "/" + part.file)
		if err != nil {
			c.close()
			return nil, fmt.Errorf("reading the %s query %s: %w", g.queries, part.file, err)
		}
		query, qerr := ts.NewQuery(c.language, string(source))
		if qerr != nil {
			c.close()
			return nil, fmt.Errorf("compiling the %s query %s: %s", g.queries, part.file, qerr.Message) //nolint:err113 // tree-sitter's own words
		}
		*part.into = query
	}
	return c, nil
}

// treeSitterParser is one worker's parser, query cursor and compiled queries. None of them may be
// shared between goroutines, so each worker has its own.
type treeSitterParser struct {
	parser   *ts.Parser
	cursor   *ts.QueryCursor
	compiled map[Lang]*compiled
	broken   map[Lang]bool
}

func newParser() parser {
	return &treeSitterParser{
		parser:   ts.NewParser(),
		cursor:   ts.NewQueryCursor(),
		compiled: make(map[Lang]*compiled),
		broken:   make(map[Lang]bool),
	}
}

func (p *treeSitterParser) close() {
	for _, c := range p.compiled {
		c.close()
	}
	p.cursor.Close()
	p.parser.Close()
}

func (p *treeSitterParser) queriesFor(lang Lang) *compiled {
	if c, ok := p.compiled[lang]; ok {
		return c
	}
	g, ok := grammars[lang]
	if !ok || p.broken[lang] {
		return nil
	}
	c, err := compile(g)
	if err != nil {
		p.broken[lang] = true
		return nil
	}
	p.compiled[lang] = c
	return c
}

func (p *treeSitterParser) parse(lang Lang, content []byte, facts *Facts) {
	queries := p.queriesFor(lang)
	if queries == nil {
		return
	}
	if err := p.parser.SetLanguage(queries.language); err != nil {
		return
	}
	tree := p.parser.Parse(content, nil)
	if tree == nil {
		return
	}
	defer tree.Close()
	root := tree.RootNode()
	r := &reader{src: content, lang: lang, facts: facts, members: grammars[lang].members}
	r.readDecls(p.cursor, queries.decls, root)
	r.readImports(p.cursor, queries.imports, root)
	r.readRefs(p.cursor, queries.refs, root)
	facts.Parsed = true
}

// reader reads one file's tree into its facts.
type reader struct {
	src     []byte
	lang    Lang
	facts   *Facts
	members func(r *reader, node *ts.Node, d *Decl)
}

func (r *reader) text(n *ts.Node) string {
	if n == nil {
		return ""
	}
	return n.Utf8Text(r.src)
}

func line(n *ts.Node) int { return int(n.StartPosition().Row) + 1 }

// ---- declarations --------------------------------------------------------------------------------

type foundDecl struct {
	node    ts.Node
	name    string
	kind    Kind
	pattern uint
}

func (r *reader) readDecls(cursor *ts.QueryCursor, q *ts.Query, root *ts.Node) {
	names := q.CaptureNames()
	byRange := make(map[[2]uint]foundDecl)
	matches := cursor.Matches(q, root, r.src)
	for m := matches.Next(); m != nil; m = matches.Next() {
		var found foundDecl
		var hasNode bool
		for _, c := range m.Captures {
			if names[c.Index] == "name" {
				found.name = c.Node.Utf8Text(r.src)
				continue
			}
			found.node, found.kind, hasNode = c.Node, Kind(names[c.Index]), true
		}
		if !hasNode || found.name == "" {
			continue
		}
		found.pattern = m.PatternIndex
		key := [2]uint{found.node.StartByte(), found.node.EndByte()}
		if prev, seen := byRange[key]; seen && prev.pattern <= found.pattern {
			continue // an earlier pattern already said what this node is
		}
		byRange[key] = found
	}

	ordered := make([]foundDecl, 0, len(byRange))
	for _, f := range byRange {
		ordered = append(ordered, f)
	}
	// Document order, the outer of two declarations starting at one byte first: nesting below
	// reads each one's parent off a stack.
	slices.SortFunc(ordered, func(a, b foundDecl) int {
		return cmp.Or(cmp.Compare(a.node.StartByte(), b.node.StartByte()), cmp.Compare(b.node.EndByte(), a.node.EndByte()))
	})

	decls := make([]Decl, 0, len(ordered))
	ends := make([]uint, 0, len(ordered)) // where each kept declaration ends, body included
	open := make([]int, 0, 8)             // the kept declarations still open, innermost last
	for _, f := range ordered {
		node := f.node
		for len(open) > 0 && ends[open[len(open)-1]] <= node.StartByte() {
			open = open[:len(open)-1]
		}
		parent := -1
		if len(open) > 0 {
			parent = open[len(open)-1]
		}
		if f.kind == KindConst && r.lang == LangPython && !upperCase(f.name) {
			continue // a module-level variable, not a constant
		}
		d := Decl{
			Name:       f.name,
			Kind:       f.kind,
			Line:       line(r.startOf(&node)),
			EndLine:    int(r.endOf(&node).EndPosition().Row) + 1,
			Signature:  r.signature(&node),
			Parent:     parent,
			Visibility: "public",
			Fields:     []Member{},
			Bases:      []string{},
			Implements: []string{},
		}
		if parent >= 0 && d.Kind == KindFunction && decls[parent].Kind.IsType() {
			d.Kind = KindMethod // a Python def or a Dart function inside a class
		}
		r.members(r, &node, &d)
		decls = append(decls, d)
		ends = append(ends, r.endOf(&node).EndByte())
		open = append(open, len(decls)-1)
	}
	r.facts.Decls = decls
	if r.lang == LangGo {
		attachGoMethods(decls)
	}
}

// startOf is the node a declaration's lines start at: its decorators, or the statement that
// declares a variable. A spec inside a grouped Go block — `const (`, `var (`, `type (` on a line of
// its own — starts at itself: the block is several declarations, and its first line is none of them.
func (r *reader) startOf(node *ts.Node) *ts.Node {
	parent := node.Parent()
	if parent == nil {
		return node
	}
	switch parent.Kind() {
	case "decorated_definition":
		return parent
	case "lexical_declaration", "variable_declaration", "type_declaration", "const_declaration", "var_declaration":
		if grouped(node, parent) {
			return node
		}
		if grand := parent.Parent(); grand != nil && grand.Kind() == "export_statement" {
			return grand
		}
		return parent
	case "export_statement":
		return parent
	}
	return node
}

// goGroupKeyword is what a spec of a grouped Go block is written after.
var goGroupKeyword = map[string]string{"const_declaration": "const ", "var_declaration": "var ", "type_declaration": "type "}

// grouped says whether a spec sits in a parenthesised Go block rather than on its statement's line.
func grouped(spec, statement *ts.Node) bool {
	_, isGo := goGroupKeyword[statement.Kind()]
	return isGo && spec.StartPosition().Row != statement.StartPosition().Row
}

// endOf is the node a declaration's lines end at. A Dart function's body is the node after its
// signature — or after the method_signature or declaration wrapping it.
func (r *reader) endOf(node *ts.Node) *ts.Node {
	if r.lang != LangDart {
		return node
	}
	at := node
	if parent := node.Parent(); parent != nil && (parent.Kind() == "method_signature" || parent.Kind() == "declaration") {
		at = parent
	}
	if next := at.NextNamedSibling(); next != nil && next.Kind() == "function_body" {
		return next
	}
	return node
}

// maxSignature bounds a signature, in characters.
const maxSignature = 160

// signature is a declaration's head on one line: from where it starts to where its body does.
func (r *reader) signature(node *ts.Node) string {
	start := r.startOf(node).StartByte()
	end := node.EndByte()
	if body := node.ChildByFieldName("body"); body != nil {
		end = body.StartByte()
	} else if value := node.ChildByFieldName("value"); value != nil {
		if body := value.ChildByFieldName("body"); body != nil {
			end = body.StartByte()
		}
	}
	text := string(r.src[start:max(start, end)])
	if brace := strings.IndexByte(text, '{'); brace >= 0 {
		text = text[:brace]
	}
	text = strings.Join(strings.Fields(text), " ")
	text = strings.TrimRight(strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(text, " ;"), "=>"), ":"), " =")
	if parent := node.Parent(); parent != nil && grouped(node, parent) {
		text = goGroupKeyword[parent.Kind()] + text // `maxRetries = 2` alone does not say it is a constant
	}
	if utf8.RuneCountInString(text) > maxSignature {
		runes := []rune(text)
		text = string(runes[:maxSignature-1]) + "…"
	}
	return text
}

func upperCase(name string) bool {
	return strings.ToUpper(name) == name && strings.IndexFunc(name, unicode.IsLetter) >= 0
}

// attachGoMethods gives each Go method its receiver's declaration as its parent, when the type is
// declared in the same file; Owner names it either way.
func attachGoMethods(decls []Decl) {
	types := make(map[string]int)
	for i, d := range decls {
		if d.Kind.IsType() || d.Kind == KindType {
			types[d.Name] = i
		}
	}
	for i := range decls {
		if decls[i].Kind == KindMethod && decls[i].Owner != "" {
			if t, ok := types[decls[i].Owner]; ok {
				decls[i].Parent = t
			}
		}
	}
}

// ---- imports and mentions ------------------------------------------------------------------------

func (r *reader) readImports(cursor *ts.QueryCursor, q *ts.Query, root *ts.Node) {
	names := q.CaptureNames()
	matches := cursor.Matches(q, root, r.src)
	for m := matches.Next(); m != nil; m = matches.Next() {
		for _, c := range m.Captures {
			node := c.Node
			switch names[c.Index] {
			case "package":
				if r.facts.Package == "" {
					r.facts.Package = r.text(&node)
				}
			case "import":
				for _, spec := range r.importSpecs(&node) {
					if spec != "" {
						r.facts.Imports = append(r.facts.Imports, Import{Spec: spec, Line: line(&node)})
					}
				}
			}
		}
	}
}

// importSpecs is what one captured import names, as the resolver reads specifiers.
func (r *reader) importSpecs(node *ts.Node) []string {
	switch r.lang {
	case LangPython:
		return r.pythonImports(node)
	case LangJava:
		text := strings.TrimSpace(r.text(node))
		text = strings.TrimSuffix(strings.TrimPrefix(text, "import"), ";")
		return []string{strings.Join(strings.Fields(text), " ")}
	case LangDart:
		return []string{strings.Trim(r.text(node), `'"`)}
	}
	return []string{r.text(node)}
}

// pythonImports turns `import a.b, c as d` and `from .x import y` into specifiers. A relative
// import of names from a package itself (`from . import models`) may name its submodules, so each
// name is offered under the package as well.
func (r *reader) pythonImports(node *ts.Node) []string {
	cursor := node.Walk()
	defer cursor.Close()
	names := node.ChildrenByFieldName("name", cursor)
	nameOf := func(n *ts.Node) string {
		if n.Kind() == "aliased_import" {
			return r.text(n.ChildByFieldName("name"))
		}
		return r.text(n)
	}
	if node.Kind() == "import_statement" {
		out := make([]string, 0, len(names))
		for i := range names {
			out = append(out, nameOf(&names[i]))
		}
		return out
	}
	module := r.text(node.ChildByFieldName("module_name"))
	out := []string{module}
	if strings.Trim(module, ".") == "" {
		for i := range names {
			out = append(out, module+nameOf(&names[i]))
		}
	}
	return out
}

// maxRefs bounds the distinct names one file records, in the order it first mentions them.
const maxRefs = 3000

func (r *reader) readRefs(cursor *ts.QueryCursor, q *ts.Query, root *ts.Node) {
	seen := make(map[string]bool)
	captures := cursor.Captures(q, root, r.src)
	for m, index := captures.Next(); m != nil; m, index = captures.Next() {
		node := m.Captures[index].Node
		name := node.Utf8Text(r.src)
		if len(name) < 2 || seen[name] {
			continue
		}
		seen[name] = true
		r.facts.Refs = append(r.facts.Refs, Ref{Name: name, Line: line(&node)})
		if len(r.facts.Refs) == maxRefs {
			break
		}
	}
	slices.SortFunc(r.facts.Refs, func(a, b Ref) int { return cmp.Compare(a.Name, b.Name) })
}

// ---- members, heritage and visibility, per language ---------------------------------------------

// baseName is a type as a class diagram links it: no generics, no pointer, no package.
func baseName(t string) string {
	t = strings.TrimSpace(strings.TrimLeft(t, "*&"))
	if i := strings.IndexAny(t, "<[("); i >= 0 {
		t = t[:i]
	}
	if i := strings.LastIndexByte(t, '.'); i >= 0 {
		t = t[i+1:]
	}
	return strings.TrimSpace(t)
}

// typeText is a type annotation on one line, without the colon TypeScript puts before it.
func typeText(s string) string {
	return strings.Join(strings.Fields(strings.TrimPrefix(strings.TrimSpace(s), ":")), " ")
}

// namedChildren are a node's named children, as values that outlive the cursor.
func namedChildren(n *ts.Node) []ts.Node {
	if n == nil {
		return nil
	}
	cursor := n.Walk()
	defer cursor.Close()
	return n.NamedChildren(cursor)
}

// hasToken says whether a node has an anonymous child token such as `static` or `abstract`.
func hasToken(n *ts.Node, token string) bool {
	for i := range n.ChildCount() {
		if child := n.Child(i); child != nil && child.Kind() == token {
			return true
		}
	}
	return false
}

func isFunctionValue(n *ts.Node) bool {
	if n == nil {
		return false
	}
	switch n.Kind() {
	case "arrow_function", "function_expression", "generator_function", "function":
		return true
	}
	return false
}

func tsMembers(r *reader, node *ts.Node, d *Decl) {
	if modifier := firstChildOfKind(node, "accessibility_modifier"); modifier != nil {
		d.Visibility = r.text(modifier)
	}
	if name := node.ChildByFieldName("name"); name != nil && name.Kind() == "private_property_identifier" {
		d.Visibility = "private"
	}
	d.Abstract = node.Kind() == "abstract_class_declaration" || node.Kind() == "abstract_method_signature" || hasToken(node, "abstract")

	switch node.Kind() {
	case "class_declaration", "abstract_class_declaration", "class":
		if heritage := firstChildOfKind(node, "class_heritage"); heritage != nil {
			for _, clause := range namedChildren(heritage) {
				switch clause.Kind() {
				case "extends_clause":
					if value := clause.ChildByFieldName("value"); value != nil {
						d.Bases = append(d.Bases, baseName(r.text(value)))
					}
				case "implements_clause":
					for _, t := range namedChildren(&clause) {
						d.Implements = append(d.Implements, baseName(r.text(&t)))
					}
				}
			}
		}
		for _, member := range namedChildren(node.ChildByFieldName("body")) {
			if member.Kind() != "public_field_definition" || isFunctionValue(member.ChildByFieldName("value")) {
				continue
			}
			field := Member{Name: r.text(member.ChildByFieldName("name")), Type: typeText(r.text(member.ChildByFieldName("type"))), Line: line(&member), Visibility: "public", Static: hasToken(&member, "static")}
			if modifier := firstChildOfKind(&member, "accessibility_modifier"); modifier != nil {
				field.Visibility = r.text(modifier)
			}
			if name := member.ChildByFieldName("name"); name != nil && name.Kind() == "private_property_identifier" {
				field.Visibility = "private"
			}
			d.Fields = append(d.Fields, field)
		}
	case "interface_declaration":
		if extends := firstChildOfKind(node, "extends_type_clause"); extends != nil {
			for _, t := range namedChildren(extends) {
				d.Bases = append(d.Bases, baseName(r.text(&t)))
			}
		}
		for _, member := range namedChildren(node.ChildByFieldName("body")) {
			if member.Kind() == "property_signature" {
				d.Fields = append(d.Fields, Member{Name: r.text(member.ChildByFieldName("name")), Type: typeText(r.text(member.ChildByFieldName("type"))), Line: line(&member), Visibility: "public"})
			}
		}
	case "enum_declaration":
		for _, member := range namedChildren(node.ChildByFieldName("body")) {
			name := member
			if member.Kind() == "enum_assignment" {
				if n := member.ChildByFieldName("name"); n != nil {
					name = *n
				}
			}
			d.Fields = append(d.Fields, Member{Name: r.text(&name), Line: line(&member), Visibility: "public", Static: true})
		}
	}
}

func jsMembers(r *reader, node *ts.Node, d *Decl) {
	if name := node.ChildByFieldName("name"); name != nil && name.Kind() == "private_property_identifier" {
		d.Visibility = "private"
	}
	if node.Kind() != "class_declaration" && node.Kind() != "class" {
		return
	}
	if heritage := firstChildOfKind(node, "class_heritage"); heritage != nil {
		for _, base := range namedChildren(heritage) {
			d.Bases = append(d.Bases, baseName(r.text(&base)))
		}
	}
	for _, member := range namedChildren(node.ChildByFieldName("body")) {
		if member.Kind() != "field_definition" || isFunctionValue(member.ChildByFieldName("value")) {
			continue
		}
		field := Member{Name: r.text(member.ChildByFieldName("property")), Line: line(&member), Visibility: "public", Static: hasToken(&member, "static")}
		if strings.HasPrefix(field.Name, "#") {
			field.Visibility = "private"
		}
		d.Fields = append(d.Fields, field)
	}
}

func goVisibility(name string) string {
	if r, _ := utf8.DecodeRuneInString(name); unicode.IsUpper(r) {
		return "public"
	}
	return "private"
}

func goMembers(r *reader, node *ts.Node, d *Decl) {
	d.Visibility = goVisibility(d.Name)
	switch node.Kind() {
	case "method_declaration":
		if receiver := node.ChildByFieldName("receiver"); receiver != nil {
			for _, param := range namedChildren(receiver) {
				d.Owner = baseName(r.text(param.ChildByFieldName("type")))
			}
		}
	case "type_spec":
		body := node.ChildByFieldName("type")
		if body == nil {
			return
		}
		switch body.Kind() {
		case "struct_type":
			for _, list := range namedChildren(body) {
				for _, field := range namedChildren(&list) {
					if field.Kind() != "field_declaration" {
						continue
					}
					fieldType := typeText(r.text(field.ChildByFieldName("type")))
					cursor := field.Walk()
					names := field.ChildrenByFieldName("name", cursor)
					cursor.Close()
					if len(names) == 0 {
						d.Bases = append(d.Bases, baseName(fieldType)) // embedded
						continue
					}
					for i := range names {
						name := r.text(&names[i])
						d.Fields = append(d.Fields, Member{Name: name, Type: fieldType, Line: line(&field), Visibility: goVisibility(name)})
					}
				}
			}
		case "interface_type":
			for _, elem := range namedChildren(body) {
				if elem.Kind() == "type_elem" {
					d.Bases = append(d.Bases, baseName(r.text(&elem)))
				}
			}
		}
	}
}

func pythonVisibility(name string) string {
	switch {
	case strings.HasPrefix(name, "__") && !strings.HasSuffix(name, "__"):
		return "private"
	case strings.HasPrefix(name, "_"):
		return "protected"
	}
	return "public"
}

func pythonMembers(r *reader, node *ts.Node, d *Decl) {
	d.Visibility = pythonVisibility(d.Name)
	if node.Kind() != "class_definition" {
		return
	}
	for _, arg := range namedChildren(node.ChildByFieldName("superclasses")) {
		if arg.Kind() != "keyword_argument" {
			d.Bases = append(d.Bases, baseName(r.text(&arg)))
		}
	}
	seen := make(map[string]bool)
	addField := func(name, fieldType string, at *ts.Node) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		d.Fields = append(d.Fields, Member{Name: name, Type: fieldType, Line: line(at), Visibility: pythonVisibility(name)})
	}
	for _, statement := range namedChildren(node.ChildByFieldName("body")) {
		switch statement.Kind() {
		case "expression_statement":
			for _, assignment := range namedChildren(&statement) {
				if assignment.Kind() == "assignment" {
					if left := assignment.ChildByFieldName("left"); left != nil && left.Kind() == "identifier" {
						addField(r.text(left), typeText(r.text(assignment.ChildByFieldName("type"))), &assignment)
					}
				}
			}
		case "function_definition", "decorated_definition":
			function := statement
			if statement.Kind() == "decorated_definition" {
				if def := statement.ChildByFieldName("definition"); def != nil {
					function = *def
				}
			}
			if r.text(function.ChildByFieldName("name")) == "__init__" {
				r.selfAssignments(function.ChildByFieldName("body"), addField)
			}
		}
	}
}

// selfAssignments finds `self.x = …` in a constructor's body, at any depth.
func (r *reader) selfAssignments(n *ts.Node, add func(name, fieldType string, at *ts.Node)) {
	if n == nil {
		return
	}
	for _, child := range namedChildren(n) {
		if child.Kind() == "assignment" {
			if left := child.ChildByFieldName("left"); left != nil && left.Kind() == "attribute" && r.text(left.ChildByFieldName("object")) == "self" {
				add(r.text(left.ChildByFieldName("attribute")), typeText(r.text(child.ChildByFieldName("type"))), &child)
			}
		}
		r.selfAssignments(&child, add)
	}
}

// javaModifiers reads visibility, static and abstract from a declaration's modifiers.
func javaModifiers(r *reader, node *ts.Node) (visibility string, static, abstract bool) {
	visibility = "package"
	modifiers := firstChildOfKind(node, "modifiers")
	if modifiers == nil {
		return visibility, false, false
	}
	for word := range strings.FieldsSeq(r.text(modifiers)) {
		switch word {
		case "public", "private", "protected":
			visibility = word
		case "static":
			static = true
		case "abstract":
			abstract = true
		}
	}
	return visibility, static, abstract
}

func javaMembers(r *reader, node *ts.Node, d *Decl) {
	d.Visibility, _, d.Abstract = javaModifiers(r, node)
	if parent := node.Parent(); parent != nil && parent.Kind() == "interface_body" && d.Visibility == "package" {
		d.Visibility = "public" // an interface's members are public unless they say otherwise
	}
	typeList := func(field string) []string {
		out := make([]string, 0, 2)
		for _, clause := range namedChildren(node.ChildByFieldName(field)) {
			if clause.Kind() == "type_list" {
				for _, t := range namedChildren(&clause) {
					out = append(out, baseName(r.text(&t)))
				}
				continue
			}
			out = append(out, baseName(r.text(&clause)))
		}
		return out
	}
	switch node.Kind() {
	case "class_declaration":
		d.Bases = append(d.Bases, typeList("superclass")...)
		d.Implements = append(d.Implements, typeList("interfaces")...)
	case "enum_declaration", "record_declaration":
		d.Implements = append(d.Implements, typeList("interfaces")...)
	case "interface_declaration":
		if extends := firstChildOfKind(node, "extends_interfaces"); extends != nil {
			for _, list := range namedChildren(extends) {
				for _, t := range namedChildren(&list) {
					d.Bases = append(d.Bases, baseName(r.text(&t)))
				}
			}
		}
	}
	if node.Kind() == "record_declaration" {
		for _, param := range namedChildren(node.ChildByFieldName("parameters")) {
			d.Fields = append(d.Fields, Member{Name: r.text(param.ChildByFieldName("name")), Type: typeText(r.text(param.ChildByFieldName("type"))), Line: line(&param), Visibility: "private"})
		}
	}
	for _, member := range namedChildren(node.ChildByFieldName("body")) {
		if member.Kind() != "field_declaration" {
			continue
		}
		visibility, static, _ := javaModifiers(r, &member)
		fieldType := typeText(r.text(member.ChildByFieldName("type")))
		cursor := member.Walk()
		declarators := member.ChildrenByFieldName("declarator", cursor)
		cursor.Close()
		for i := range declarators {
			d.Fields = append(d.Fields, Member{Name: r.text(declarators[i].ChildByFieldName("name")), Type: fieldType, Line: line(&member), Visibility: visibility, Static: static})
		}
	}
}

func dartVisibility(name string) string {
	if strings.HasPrefix(name, "_") {
		return "private"
	}
	return "public"
}

func dartMembers(r *reader, node *ts.Node, d *Decl) {
	d.Visibility = dartVisibility(d.Name)
	switch node.Kind() {
	case "class_definition":
		d.Abstract = firstChildOfKind(node, "abstract") != nil || hasToken(node, "abstract")
		if superclass := node.ChildByFieldName("superclass"); superclass != nil {
			for _, part := range namedChildren(superclass) {
				switch part.Kind() {
				case "mixins":
					for _, t := range namedChildren(&part) {
						d.Implements = append(d.Implements, baseName(r.text(&t)))
					}
				default:
					if strings.HasSuffix(part.Kind(), "type_identifier") || part.Kind() == "type_identifier" {
						d.Bases = append(d.Bases, baseName(r.text(&part)))
					}
				}
			}
		}
		for _, t := range namedChildren(node.ChildByFieldName("interfaces")) {
			d.Implements = append(d.Implements, baseName(r.text(&t)))
		}
		dartFields(r, node.ChildByFieldName("body"), d)
	case "mixin_declaration":
		for _, part := range namedChildren(node) {
			switch part.Kind() {
			case "type_identifier":
				d.Bases = append(d.Bases, baseName(r.text(&part))) // `on Base`
			case "class_body":
				dartFields(r, &part, d)
			}
		}
	case "enum_declaration":
		for _, constant := range namedChildren(node.ChildByFieldName("body")) {
			if constant.Kind() == "enum_constant" {
				d.Fields = append(d.Fields, Member{Name: r.text(constant.ChildByFieldName("name")), Line: line(&constant), Visibility: "public", Static: true})
			}
		}
	}
}

// dartFields reads a class body's field declarations: a type, then one or more initialized names.
func dartFields(r *reader, body *ts.Node, d *Decl) {
	for _, member := range namedChildren(body) {
		if member.Kind() != "declaration" {
			continue
		}
		list := firstChildOfKind(&member, "initialized_identifier_list")
		if list == nil {
			continue
		}
		fieldType := ""
		for _, part := range namedChildren(&member) {
			if part.Kind() == "type_identifier" {
				fieldType = r.text(&part)
				if next := part.NextNamedSibling(); next != nil && next.Kind() == "type_arguments" {
					fieldType += r.text(next)
				}
				break
			}
		}
		static := hasToken(&member, "static")
		for _, item := range namedChildren(list) {
			name := r.text(firstChildOfKind(&item, "identifier"))
			d.Fields = append(d.Fields, Member{Name: name, Type: fieldType, Line: line(&item), Visibility: dartVisibility(name), Static: static})
		}
	}
}

// firstChildOfKind is a node's first child of a kind, named or not, or nil.
func firstChildOfKind(n *ts.Node, kind string) *ts.Node {
	if n == nil {
		return nil
	}
	for i := range n.ChildCount() {
		if child := n.Child(i); child != nil && child.Kind() == kind {
			return child
		}
	}
	return nil
}
