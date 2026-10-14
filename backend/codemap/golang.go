package codemap

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	goversion "go/version"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode"
)

// The Go half of the map (CODEMAP-003, CODEMAP-004).

// Package suffixes recognised by import path, so a test fixture can stand in small stubs.
const (
	// wailsAppPkgSuffix is Wails v3's application package: its NewService binds a type's exported
	// methods to TypeScript.
	wailsAppPkgSuffix = "/wails/v3/pkg/application"
	// bunPkgSuffix is uptrace/bun, whose query builders name their table through a model type.
	bunPkgSuffix = "/bun"
	// gormPkgSuffix is gorm.io/gorm, whose finishers take the model they read or write.
	gormPkgSuffix = "/gorm"
)

// gormVerbs are GORM's finishers whose first argument is the model: what they read into or write
// from.
var gormVerbs = map[string]EdgeKind{
	"Find": EdgeRead, "First": EdgeRead, "Last": EdgeRead, "Take": EdgeRead, "FindInBatches": EdgeRead,
	"Create": EdgeWrite, "CreateInBatches": EdgeWrite, "Save": EdgeWrite, "Delete": EdgeWrite,
}

// wailsLifecycle are the methods Wails never binds: a service's lifecycle hooks.
var wailsLifecycle = map[string]bool{
	"ServiceName": true, "ServiceStartup": true, "ServiceShutdown": true, "ServeHTTP": true,
}

// goHalf is the Go half of the map, kept between rebuilds so that a TypeScript-only change does not
// re-run the go command.
type goHalf struct {
	nodes   []*Node
	edges   []Edge
	strings []codeString
	// registrations are handlers registered under a string key, matched against TypeScript later.
	registrations []registration
	// guessedTables are table names derived from a naming convention rather than written down:
	// they count only if the schema really has such a table.
	guessedTables map[string]bool
	// bound indexes the Wails-bound methods: (package path, lower-cased type, method) → node ID.
	bound map[boundKey]string
	notes []string
	files int
}

// codeString is a string a symbol uses that may be SQL or a table name. It is classified once the
// whole table set is known.
type codeString struct {
	from string
	text string
	at   Pos
}

// registration is `r.Add("name", handler)`: a Go function registered under a string key.
type registration struct {
	key     string
	handler string
	at      Pos
}

type boundKey struct{ pkg, typ, method string }

// goIndexer carries the state of one module's extraction.
type goIndexer struct {
	root string
	half *goHalf
	ids  map[string]*Node
	// modelTables maps "pkgpath.Type" to the table of an ORM model struct.
	modelTables map[string]string
	// modelParams are parameters typed `any` that a function hands to bun's Model.
	modelParams map[string][]modelParam
	callSites   []callSite
	// done holds the files already walked in the current pass: a test variant repeats them.
	done map[string]bool
}

type modelParam struct {
	index int
	kind  EdgeKind
}

type callSite struct {
	from, to string
	at       Pos
	// argTables[i] is the model table of argument i, "" when it has none.
	argTables []string
}

// indexGo maps the Go modules of a repository.
func indexGo(ctx context.Context, root string, modules []string, lister goLister) (*goHalf, error) {
	if lister.binary == "" {
		return nil, errNoGo
	}
	half := newGoHalf()
	for _, dir := range modules {
		packages, err := loadModule(ctx, dir, lister)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			half.notes = append(half.notes, err.Error())
			continue
		}
		newGoIndexer(root, half).index(packages)
	}
	return half, nil
}

func newGoHalf() *goHalf {
	return &goHalf{bound: map[boundKey]string{}, guessedTables: map[string]bool{}}
}

// newGoIndexer starts the extraction of one module into half.
func newGoIndexer(root string, half *goHalf) *goIndexer {
	return &goIndexer{
		root:        root,
		half:        half,
		ids:         map[string]*Node{},
		modelTables: map[string]string{},
		modelParams: map[string][]modelParam{},
		done:        map[string]bool{},
	}
}

// loadModule lists and type-checks one module. Export data is used when the user's toolchain is
// not newer than the one this binary was built with — older formats stay readable — and source
// otherwise, or as soon as reading export data fails on its format.
func loadModule(ctx context.Context, dir string, lister goLister) ([]*goPackage, error) {
	mode := exportMode
	if toolchain, err := lister.version(ctx, dir); err != nil || goversion.Compare(toolchain, runtime.Version()) > 0 {
		mode = sourceMode
	}
	listed, err := lister.list(ctx, dir, mode == exportMode)
	if err != nil {
		return nil, err
	}
	packages, err := newGoLoader(listed, mode).checkModule(ctx, listed)
	if errors.Is(err, errExportFormat) {
		packages, err = newGoLoader(listed, sourceMode).checkModule(ctx, listed)
	}
	return packages, err
}

func (ix *goIndexer) index(packages []*goPackage) {
	packages = ix.usable(packages)
	for _, pkg := range packages {
		ix.declare(pkg)
	}
	ix.half.files += len(ix.done)
	ix.dispatch(packages)
	for _, pkg := range packages {
		ix.markBound(pkg)
	}
	clear(ix.done)
	for _, pkg := range packages {
		ix.walkBodies(pkg)
	}
	ix.resolveModelParams()
}

// maxPackageNotes bounds how many broken packages are named one by one.
const maxPackageNotes = 5

// usable notes the packages that did not type-check cleanly and drops the ones with no type
// information at all. A package with errors is still mapped as far as its types go.
func (ix *goIndexer) usable(packages []*goPackage) []*goPackage {
	out := make([]*goPackage, 0, len(packages))
	broken := 0
	for _, pkg := range packages {
		if len(pkg.Errors) > 0 && !pkg.Variant {
			broken++
			if broken <= maxPackageNotes {
				ix.half.notes = append(ix.half.notes, fmt.Sprintf("Go: %s: %s", pkg.PkgPath, firstLine(pkg.Errors[0].Error())))
			}
		}
		if pkg.Types == nil || pkg.Info == nil {
			continue
		}
		out = append(out, pkg)
	}
	if broken > maxPackageNotes {
		ix.half.notes = append(ix.half.notes, fmt.Sprintf("Go: %d more packages did not type-check cleanly", broken-maxPackageNotes))
	}
	return out
}

// files yields each syntax tree of pkg that lies inside the repository and has not been visited in
// the current pass, with its repository-relative path.
func (ix *goIndexer) files(pkg *goPackage, yield func(file *ast.File, rel string)) {
	for _, file := range pkg.Syntax {
		abs := pkg.Fset.Position(file.Package).Filename
		if ix.done[abs] {
			continue
		}
		rel, err := relPath(ix.root, abs)
		if err != nil || strings.HasPrefix(rel, "../") {
			continue
		}
		ix.done[abs] = true
		yield(file, rel)
	}
}

func (ix *goIndexer) pos(fset *token.FileSet, from, to token.Pos) Pos {
	start, end := fset.Position(from), fset.Position(to)
	rel, err := relPath(ix.root, start.Filename)
	if err != nil {
		return Pos{}
	}
	return Pos{File: rel, Line: start.Line, EndLine: end.Line}
}

func (ix *goIndexer) addNode(n *Node) {
	if _, duplicate := ix.ids[n.ID]; duplicate {
		return
	}
	ix.ids[n.ID] = n
	ix.half.nodes = append(ix.half.nodes, n)
}

func (ix *goIndexer) edge(from, to string, kind EdgeKind, at Pos) {
	ix.half.edges = append(ix.half.edges, Edge{From: from, To: to, Kind: kind, At: at})
}

// declare creates the nodes of pkg's functions, methods, named types and interface members, and
// records the table behind each ORM model struct.
func (ix *goIndexer) declare(pkg *goPackage) {
	qualifier := func(p *types.Package) string { return p.Name() }
	ix.files(pkg, func(file *ast.File, rel string) {
		isTestFile := strings.HasSuffix(rel, "_test.go")
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				fn, ok := pkg.Info.Defs[d.Name].(*types.Func)
				if !ok {
					continue
				}
				id := funcID(fn)
				if id == "" {
					continue
				}
				n := &Node{
					ID:        id,
					Name:      displayName(fn),
					Lang:      LangGo,
					Kind:      KindFunc,
					Pos:       ix.pos(pkg.Fset, d.Pos(), d.End()),
					Signature: clip(types.ObjectString(fn, qualifier), 240),
					Doc:       firstSentence(d.Doc.Text()),
				}
				if recv := fn.Signature().Recv(); recv != nil {
					n.Kind = KindMethod
					n.Parent = "go:" + fn.Pkg().Path() + "." + recvTypeName(recv.Type())
					if fn.Name() == "TableName" && d.Body != nil {
						ix.gormTable(pkg, fn, d.Body)
					}
				} else if isTestFile && isTestFunc(d.Name.Name) {
					n.Kind = KindTestFunc
				}
				ix.addNode(n)
			case *ast.GenDecl:
				ix.declareTypes(pkg, d)
			}
		}
	})
}

func (ix *goIndexer) declareTypes(pkg *goPackage, d *ast.GenDecl) {
	if d.Tok != token.TYPE {
		return
	}
	for _, spec := range d.Specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		obj, ok := pkg.Info.Defs[typeSpec.Name].(*types.TypeName)
		if !ok || obj.Pkg() == nil {
			continue
		}
		doc := typeSpec.Doc
		if doc == nil {
			doc = d.Doc
		}
		key := obj.Pkg().Path() + "." + obj.Name()
		n := &Node{
			ID:        "go:" + key,
			Name:      obj.Pkg().Name() + "." + obj.Name(),
			Lang:      LangGo,
			Kind:      KindType,
			Pos:       ix.pos(pkg.Fset, typeSpec.Pos(), typeSpec.End()),
			Signature: "type " + obj.Name(),
			Doc:       firstSentence(doc.Text()),
		}
		switch underlying := obj.Type().Underlying().(type) {
		case *types.Struct:
			n.Signature += " struct"
			if table := bunTable(underlying); table != "" {
				ix.modelTables[key] = table
			}
		case *types.Interface:
			n.Kind = KindInterface
			n.Signature += " interface"
			ix.declareInterfaceMethods(pkg, n, typeSpec)
		}
		ix.addNode(n)
	}
}

// declareInterfaceMethods makes each method of an interface a node of its own: the hub a call
// through the interface lands on.
func (ix *goIndexer) declareInterfaceMethods(pkg *goPackage, iface *Node, spec *ast.TypeSpec) {
	literal, ok := spec.Type.(*ast.InterfaceType)
	if !ok || literal.Methods == nil {
		return
	}
	for _, field := range literal.Methods.List {
		for _, name := range field.Names {
			fn, ok := pkg.Info.Defs[name].(*types.Func)
			if !ok {
				continue
			}
			ix.addNode(&Node{
				ID:        iface.ID + "." + name.Name,
				Name:      iface.Name + "." + name.Name,
				Lang:      LangGo,
				Kind:      KindInterfaceMethod,
				Pos:       ix.pos(pkg.Fset, field.Pos(), field.End()),
				Signature: clip(name.Name+strings.TrimPrefix(types.TypeString(fn.Type(), func(p *types.Package) string { return p.Name() }), "func"), 240),
				Doc:       firstSentence(field.Doc.Text()),
				Parent:    iface.ID,
			})
		}
	}
}

// bunTable reads `bun:"table:<name>,…"` from a struct's embedded bun.BaseModel.
func bunTable(s *types.Struct) string {
	for i := range s.NumFields() {
		field := s.Field(i)
		if field.Name() != "BaseModel" || !field.Embedded() {
			continue
		}
		for part := range strings.SplitSeq(reflect.StructTag(s.Tag(i)).Get("bun"), ",") {
			if table, ok := strings.CutPrefix(part, "table:"); ok {
				return strings.ToLower(strings.Trim(table, `"`))
			}
		}
	}
	return ""
}

// gormTable reads `func (T) TableName() string { return "name" }`, GORM's way of naming a model's
// table.
func (ix *goIndexer) gormTable(pkg *goPackage, fn *types.Func, body *ast.BlockStmt) {
	if len(body.List) != 1 {
		return
	}
	ret, ok := body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return
	}
	value := pkg.Info.Types[ret.Results[0]].Value
	named := namedOf(fn.Signature().Recv().Type())
	if value == nil || value.Kind() != constant.String || named == nil {
		return
	}
	ix.modelTables[named.Obj().Pkg().Path()+"."+named.Obj().Name()] = strings.ToLower(constant.StringVal(value))
}

// dispatch links each interface method of the module to the module's methods that implement it.
// Only plain package variants are compared: types from two variants are never identical.
func (ix *goIndexer) dispatch(packages []*goPackage) {
	var interfaces, concretes []*types.TypeName
	for _, pkg := range packages {
		if pkg.Variant {
			continue
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			typeName, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || typeName.IsAlias() {
				continue
			}
			if _, isInterface := typeName.Type().Underlying().(*types.Interface); isInterface {
				interfaces = append(interfaces, typeName)
			} else {
				concretes = append(concretes, typeName)
			}
		}
	}

	for _, it := range interfaces {
		iface, ok := it.Type().Underlying().(*types.Interface)
		if !ok || iface.NumMethods() == 0 {
			continue
		}
		for _, concrete := range concretes {
			if !types.Implements(concrete.Type(), iface) && !types.Implements(types.NewPointer(concrete.Type()), iface) {
				continue
			}
			// The pointer's method set holds every method either receiver kind declares, including
			// the ones promoted from an embedded field — whose node is the embedded type's method.
			methods := types.NewMethodSet(types.NewPointer(concrete.Type()))
			for method := range iface.Methods() {
				selection := methods.Lookup(method.Pkg(), method.Name())
				if selection == nil {
					continue
				}
				implementation, ok := selection.Obj().(*types.Func)
				if !ok {
					continue
				}
				from := "go:" + it.Pkg().Path() + "." + it.Name() + "." + method.Name()
				to := funcID(implementation)
				if n := ix.ids[to]; n != nil {
					ix.edge(from, to, EdgeDispatch, n.Pos)
				}
			}
		}
	}
}

// markBound finds `application.NewService(x)` and flags the exported methods of x's type as bound
// to TypeScript, the way Wails does.
func (ix *goIndexer) markBound(pkg *goPackage) {
	info := pkg.Info
	for _, file := range pkg.Syntax {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			fn, ok := callee(info, call).(*types.Func)
			if !ok || fn.Pkg() == nil || !strings.HasSuffix(fn.Pkg().Path(), wailsAppPkgSuffix) ||
				(fn.Name() != "NewService" && fn.Name() != "NewServiceWithOptions") {
				return true
			}
			named := namedOf(info.TypeOf(call.Args[0]))
			if named == nil {
				return true
			}
			methods := types.NewMethodSet(types.NewPointer(named))
			for selection := range methods.Methods() {
				method, ok := selection.Obj().(*types.Func)
				if !ok || !method.Exported() || wailsLifecycle[method.Name()] {
					continue
				}
				id := funcID(method)
				if n := ix.ids[id]; n != nil {
					n.Bound = named.Obj().Name()
					key := boundKey{pkg: named.Obj().Pkg().Path(), typ: strings.ToLower(named.Obj().Name()), method: method.Name()}
					ix.half.bound[key] = id
				}
			}
			return true
		})
	}
}

// walkBodies records the calls, function references, handler registrations and strings inside
// every function body. A closure belongs to the function around it — except a handler registered
// under a string key, which becomes a node of its own.
func (ix *goIndexer) walkBodies(pkg *goPackage) {
	ix.files(pkg, func(file *ast.File, _ string) {
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			fn, ok := pkg.Info.Defs[fd.Name].(*types.Func)
			if !ok {
				continue
			}
			from := funcID(fn)
			if ix.ids[from] == nil {
				continue
			}
			ix.walkBody(pkg, fd.Body, from, paramIndex(pkg.Info, fd.Type))
		}
		ix.collectPackageStrings(pkg, file)
	})
}

// collectPackageStrings keeps the package-level string constants that hold DDL, so an embedded
// schema adds its tables.
func (ix *goIndexer) collectPackageStrings(pkg *goPackage, file *ast.File) {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
			continue
		}
		ast.Inspect(gen, func(node ast.Node) bool {
			lit, ok := node.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if text, err := strconv.Unquote(lit.Value); err == nil && createTableRe.MatchString(text) {
				ix.half.strings = append(ix.half.strings, codeString{text: text, at: ix.pos(pkg.Fset, lit.Pos(), lit.End())})
			}
			return true
		})
	}
}

func (ix *goIndexer) walkBody(pkg *goPackage, body ast.Node, from string, params map[types.Object]int) {
	info := pkg.Info
	callees := map[*ast.Ident]bool{}
	handlers := map[*ast.FuncLit]bool{}
	seenConst := map[*types.Const]bool{}

	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncLit:
			if handlers[n] {
				return false // walked as its own node
			}
		case *ast.CallExpr:
			if id := calleeIdent(n.Fun); id != nil {
				callees[id] = true
			}
			if literal := ix.registration(pkg, n, from); literal != nil {
				handlers[literal] = true
			}
			ix.call(pkg, n, from, params)
		case *ast.Ident:
			if callees[n] {
				return true
			}
			switch obj := info.Uses[n].(type) {
			case *types.Func:
				if to := funcID(obj); ix.ids[to] != nil {
					ix.edge(from, to, EdgeRef, ix.pos(pkg.Fset, n.Pos(), n.End()))
				}
			case *types.Const:
				if !seenConst[obj] && obj.Val().Kind() == constant.String {
					seenConst[obj] = true
					ix.keepString(from, constant.StringVal(obj.Val()), ix.pos(pkg.Fset, n.Pos(), n.End()))
				}
			}
		case *ast.BasicLit:
			if n.Kind == token.STRING {
				if text, err := strconv.Unquote(n.Value); err == nil {
					ix.keepString(from, text, ix.pos(pkg.Fset, n.Pos(), n.End()))
				}
			}
		}
		return true
	})
}

var (
	// routeKeyRe is an HTTP route, with or without its method: "/api/items", "GET /api/items/{id}".
	routeKeyRe = regexp.MustCompile(`^(?:[A-Z]+ )?/[^\s]*$`)
	// commandKeyRe is a compound command name: "diagram_list_documents", "repo:fs-changed".
	commandKeyRe = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[_:][a-z0-9-]+)+$`)
)

// registration recognises `x.Add("key", handler)` — a string key and a function, which is how HTTP
// routers, RPC registries and command buses all register a handler — and answers the handler's
// literal when it is one, so the caller does not walk it twice.
//
// Only keys shaped like a route or a compound command name count: a subtest name or a goroutine
// label passed beside a function is not an endpoint.
func (ix *goIndexer) registration(pkg *goPackage, call *ast.CallExpr, from string) *ast.FuncLit {
	if len(call.Args) < 2 {
		return nil
	}
	key := pkg.Info.Types[call.Args[0]].Value
	if key == nil || key.Kind() != constant.String {
		return nil
	}
	name := constant.StringVal(key)
	if !routeKeyRe.MatchString(name) && !commandKeyRe.MatchString(name) {
		return nil
	}
	handlerExpr := ast.Unparen(call.Args[len(call.Args)-1])
	at := ix.pos(pkg.Fset, call.Pos(), call.End())

	switch handler := handlerExpr.(type) {
	case *ast.FuncLit:
		id := from + "$" + name
		ix.addNode(&Node{
			ID:        id,
			Name:      ix.ids[from].Name + "(" + strconv.Quote(name) + ")",
			Lang:      LangGo,
			Kind:      KindFunc,
			Pos:       ix.pos(pkg.Fset, handler.Pos(), handler.End()),
			Signature: clip("func"+strings.TrimPrefix(types.TypeString(pkg.Info.TypeOf(handler), func(p *types.Package) string { return p.Name() }), "func"), 240),
			Parent:    from,
		})
		ix.edge(from, id, EdgeRef, at)
		ix.half.registrations = append(ix.half.registrations, registration{key: name, handler: id, at: at})
		ix.walkBody(pkg, handler.Body, id, nil)
		return handler
	default:
		if fn, ok := usedFunc(pkg.Info, handlerExpr); ok {
			if id := funcID(fn); ix.ids[id] != nil {
				ix.half.registrations = append(ix.half.registrations, registration{key: name, handler: id, at: at})
			}
		}
	}
	return nil
}

// call handles one call expression: a call edge — to the interface method when the call goes
// through an interface — and, for bun's Model / TableExpr, a table access.
func (ix *goIndexer) call(pkg *goPackage, call *ast.CallExpr, from string, params map[types.Object]int) {
	info := pkg.Info
	fn, ok := callee(info, call).(*types.Func)
	if !ok {
		return
	}
	at := ix.pos(pkg.Fset, call.Pos(), call.End())
	if kind, isBun := bunVerb(fn); isBun {
		ix.bunCall(info, call, fn, kind, from, at, params)
		return
	}
	if kind, isGorm := gormVerb(fn); isGorm {
		if len(call.Args) > 0 {
			if table := ix.gormModelTable(info.TypeOf(call.Args[0])); table != "" {
				ix.edge(from, tableID(table), kind, at)
			}
		}
		return
	}
	to := funcID(fn)
	if ix.ids[to] == nil {
		return
	}
	ix.edge(from, to, EdgeCall, at)
	site := callSite{from: from, to: to, at: at, argTables: make([]string, len(call.Args))}
	for i, arg := range call.Args {
		site.argTables[i] = ix.modelTable(info.TypeOf(arg))
	}
	ix.callSites = append(ix.callSites, site)
}

// bunVerb reports whether fn is bun's Model or TableExpr on a query builder, and whether that query
// reads or writes.
func bunVerb(fn *types.Func) (EdgeKind, bool) {
	if fn.Name() != "Model" && fn.Name() != "TableExpr" {
		return 0, false
	}
	recv := fn.Signature().Recv()
	if recv == nil || fn.Pkg() == nil || !strings.HasSuffix(fn.Pkg().Path(), bunPkgSuffix) {
		return 0, false
	}
	switch recvTypeName(recv.Type()) {
	case "SelectQuery":
		return EdgeRead, true
	case "InsertQuery", "UpdateQuery", "DeleteQuery":
		return EdgeWrite, true
	}
	return 0, false
}

// gormVerb reports whether fn is one of GORM's model-taking finishers on *gorm.DB.
func gormVerb(fn *types.Func) (EdgeKind, bool) {
	kind, known := gormVerbs[fn.Name()]
	recv := fn.Signature().Recv()
	if !known || recv == nil || fn.Pkg() == nil || !strings.HasSuffix(fn.Pkg().Path(), gormPkgSuffix) ||
		recvTypeName(recv.Type()) != "DB" {
		return 0, false
	}
	return kind, true
}

// gormModelTable is a GORM model's table: the one its TableName method names, or else GORM's
// convention — snake_case, plural — which is only a guess until the schema confirms it.
func (ix *goIndexer) gormModelTable(t types.Type) string {
	named := namedOf(t)
	if named == nil || named.Obj().Pkg() == nil {
		return ""
	}
	if table, ok := ix.modelTables[named.Obj().Pkg().Path()+"."+named.Obj().Name()]; ok {
		return table
	}
	if _, isStruct := named.Underlying().(*types.Struct); !isStruct {
		return ""
	}
	guess := pluralize(snakeCase(named.Obj().Name()))
	ix.half.guessedTables[guess] = true
	return guess
}

// snakeCase is GORM's column and table casing: OrderItem → order_item, HTTPLog → http_log.
func snakeCase(name string) string {
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) &&
			(unicode.IsLower(runes[i-1]) || (i+1 < len(runes) && unicode.IsLower(runes[i+1]))) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// pluralize covers the regular English plurals GORM's inflector produces for most model names.
func pluralize(word string) string {
	switch {
	case strings.HasSuffix(word, "y") && len(word) > 1 && !strings.ContainsRune("aeiou", rune(word[len(word)-2])):
		return word[:len(word)-1] + "ies"
	case strings.HasSuffix(word, "s"), strings.HasSuffix(word, "x"), strings.HasSuffix(word, "ch"), strings.HasSuffix(word, "sh"):
		return word + "es"
	}
	return word + "s"
}

func (ix *goIndexer) bunCall(info *types.Info, call *ast.CallExpr, fn *types.Func, kind EdgeKind,
	from string, at Pos, params map[types.Object]int,
) {
	if len(call.Args) == 0 {
		return
	}
	arg := call.Args[0]
	if fn.Name() == "TableExpr" {
		if tv, ok := info.Types[arg]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
			// "installments AS i": the table is the first word.
			if words := strings.Fields(constant.StringVal(tv.Value)); len(words) > 0 {
				ix.edge(from, tableID(strings.ToLower(words[0])), kind, at)
				return
			}
		}
		ix.unresolved(from, at)
		return
	}
	if table := ix.modelTable(info.TypeOf(arg)); table != "" {
		ix.edge(from, tableID(table), kind, at)
		return
	}
	// A model handed in as `any`: the callers' argument types decide, one hop up.
	if ident, ok := arg.(*ast.Ident); ok {
		if i, isParam := params[info.Uses[ident]]; isParam {
			ix.modelParams[from] = append(ix.modelParams[from], modelParam{index: i, kind: kind})
			return
		}
	}
	ix.unresolved(from, at)
}

func (ix *goIndexer) unresolved(from string, at Pos) {
	if n := ix.ids[from]; n != nil {
		n.Unresolved = append(n.Unresolved, at)
	}
}

// resolveModelParams attributes each model passed into a generic helper — softDelete(ctx,
// (*Card)(nil), …) — to the caller.
func (ix *goIndexer) resolveModelParams() {
	for _, site := range ix.callSites {
		for _, param := range ix.modelParams[site.to] {
			if param.index < len(site.argTables) && site.argTables[param.index] != "" {
				ix.edge(site.from, tableID(site.argTables[param.index]), param.kind, site.at)
			}
		}
	}
}

// modelTable is the ORM table of a model type: *T, []T, *[]T … of a tagged struct.
func (ix *goIndexer) modelTable(t types.Type) string {
	named := namedOf(t)
	if named == nil || named.Obj().Pkg() == nil {
		return ""
	}
	return ix.modelTables[named.Obj().Pkg().Path()+"."+named.Obj().Name()]
}

// keepString holds on to a string that may be SQL or a table name. What it is decides only once
// every table is known, at assembly.
func (ix *goIndexer) keepString(from, text string, at Pos) {
	if !mayBeSQL(text) {
		return
	}
	ix.half.strings = append(ix.half.strings, codeString{from: from, text: text, at: at})
}

var tableNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)

// mayBeSQL is the cheap filter run before anything is kept: a statement keyword, or a string that
// could be a table name on its own.
func mayBeSQL(text string) bool {
	if len(text) > 64*1024 {
		return false
	}
	return tableNameRe.MatchString(text) || sqlVerbAnyCaseRe.MatchString(text) || createTableRe.MatchString(text)
}

// ---------- helpers ----------

// funcID is the node ID of a function or method; "" for a builtin or a method of an unnamed type.
func funcID(fn *types.Func) string {
	fn = fn.Origin()
	if fn.Pkg() == nil {
		return ""
	}
	if recv := fn.Signature().Recv(); recv != nil {
		name := recvTypeName(recv.Type())
		if name == "" {
			return ""
		}
		return "go:" + fn.Pkg().Path() + "." + name + "." + fn.Name()
	}
	return "go:" + fn.Pkg().Path() + "." + fn.Name()
}

// displayName is "pkg.Recv.Name" or "pkg.Name".
func displayName(fn *types.Func) string {
	if recv := fn.Signature().Recv(); recv != nil {
		return fn.Pkg().Name() + "." + recvTypeName(recv.Type()) + "." + fn.Name()
	}
	return fn.Pkg().Name() + "." + fn.Name()
}

func recvTypeName(t types.Type) string {
	if named := namedOf(t); named != nil {
		return named.Obj().Name()
	}
	return ""
}

// namedOf unwraps pointers, slices and arrays down to a named type.
func namedOf(t types.Type) *types.Named {
	for t != nil {
		switch u := types.Unalias(t).(type) {
		case *types.Named:
			return u.Origin()
		case *types.Pointer:
			t = u.Elem()
		case *types.Slice:
			t = u.Elem()
		case *types.Array:
			t = u.Elem()
		default:
			return nil
		}
	}
	return nil
}

// callee is the function or method a call statically invokes, nil for a call through a function
// value or a conversion. A call through an interface answers the interface's method.
func callee(info *types.Info, call *ast.CallExpr) types.Object {
	fun := ast.Unparen(call.Fun)
	switch f := fun.(type) {
	case *ast.IndexExpr:
		fun = ast.Unparen(f.X)
	case *ast.IndexListExpr:
		fun = ast.Unparen(f.X)
	}
	var obj types.Object
	switch f := fun.(type) {
	case *ast.Ident:
		obj = info.Uses[f]
	case *ast.SelectorExpr:
		if selection, ok := info.Selections[f]; ok {
			obj = selection.Obj()
		} else {
			obj = info.Uses[f.Sel] // a qualified identifier: pkg.F
		}
	}
	if _, ok := obj.(*types.Func); !ok {
		return nil
	}
	return obj
}

// usedFunc is the function an expression names when it is used as a value: f, pkg.F, x.Method.
func usedFunc(info *types.Info, expr ast.Expr) (*types.Func, bool) {
	var obj types.Object
	switch e := ast.Unparen(expr).(type) {
	case *ast.Ident:
		obj = info.Uses[e]
	case *ast.SelectorExpr:
		if selection, ok := info.Selections[e]; ok {
			obj = selection.Obj()
		} else {
			obj = info.Uses[e.Sel]
		}
	}
	fn, ok := obj.(*types.Func)
	return fn, ok
}

// calleeIdent is the identifier naming a call's function (f, x.f, f[T]).
func calleeIdent(fun ast.Expr) *ast.Ident {
	switch f := ast.Unparen(fun).(type) {
	case *ast.Ident:
		return f
	case *ast.SelectorExpr:
		return f.Sel
	case *ast.IndexExpr:
		return calleeIdent(f.X)
	case *ast.IndexListExpr:
		return calleeIdent(f.X)
	}
	return nil
}

// paramIndex maps each parameter object of a function type to its position.
func paramIndex(info *types.Info, fn *ast.FuncType) map[types.Object]int {
	index := map[types.Object]int{}
	i := 0
	for _, field := range fn.Params.List {
		if len(field.Names) == 0 {
			i++
			continue
		}
		for _, name := range field.Names {
			if obj := info.Defs[name]; obj != nil {
				index[obj] = i
			}
			i++
		}
	}
	return index
}

func isTestFunc(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// firstSentence trims a doc comment to its first sentence.
func firstSentence(doc string) string {
	doc = strings.Join(strings.Fields(doc), " ")
	if i := strings.Index(doc, ". "); i >= 0 {
		doc = doc[:i+1]
	}
	return clip(doc, 240)
}

// clip shortens s to at most n runes, marking the cut.
func clip(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
