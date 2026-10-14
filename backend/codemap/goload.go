package codemap

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Type-checking a module from source (CODEMAP-003).

// loadMode is where a dependency's types come from.
type loadMode uint8

const (
	// exportMode reads each dependency's compiler export data: fast, and only possible when this
	// build of CodeFlow understands the format the user's toolchain wrote.
	exportMode loadMode = iota + 1
	// sourceMode type-checks each dependency's declarations from its source, ignoring function
	// bodies. Slower, and independent of the toolchain's version.
	sourceMode
)

// errExportFormat is export data this build cannot read: the user's toolchain is newer than the one
// CodeFlow was built with. The load starts over in source mode.
var errExportFormat = errors.New("export data written by a newer Go toolchain")

// goPackage is one type-checked package of the module, or one of its test variants.
type goPackage struct {
	// ID is the package's identity in `go list`: "p", or "p [p.test]" for a test variant.
	ID      string
	PkgPath string
	Fset    *token.FileSet
	Syntax  []*ast.File
	Types   *types.Package
	Info    *types.Info
	Errors  []error
	Variant bool
}

// goLoader type-checks one module's packages, one at a time.
//
// One at a time on purpose. go/packages checks packages concurrently, and go/types on Go 1.27 races
// when two checkers share an imported named type — `go test -race` failed at random until the
// checks were serialised.
type goLoader struct {
	mode    loadMode
	fset    *token.FileSet
	sizes   types.Sizes
	byID    map[string]*listedPackage
	module  map[string]bool // IDs checked from source, with bodies
	checked map[string]*goPackage
	// deps holds the dependencies checked from source (sourceMode), by ID. A nil entry is one being
	// checked, or one that failed.
	deps map[string]*types.Package
	// exports maps a dependency's import path to its export data file (exportMode).
	exports map[string]string
	// gc reads export data. One instance per load, so a type means the same *types.Package wherever
	// it is imported from.
	gc        types.Importer
	formatErr error
}

func newGoLoader(listed []*listedPackage, mode loadMode) *goLoader {
	l := &goLoader{
		mode:    mode,
		fset:    token.NewFileSet(),
		sizes:   types.SizesFor("gc", runtime.GOARCH),
		byID:    make(map[string]*listedPackage, len(listed)),
		module:  map[string]bool{},
		checked: map[string]*goPackage{},
		deps:    map[string]*types.Package{},
		exports: map[string]string{},
	}
	for _, p := range listed {
		l.byID[p.ImportPath] = p
		switch {
		case p.Standard || p.DepOnly:
			if p.Export != "" && p.ForTest == "" {
				l.exports[p.ImportPath] = p.Export
			}
		case !p.isTestMain():
			l.module[p.ImportPath] = true
		}
	}
	l.gc = importer.ForCompiler(l.fset, "gc", func(path string) (io.ReadCloser, error) {
		file, ok := l.exports[path]
		if !ok {
			return nil, fmt.Errorf("no export data for %q", path) //nolint:err113 // names the package
		}
		return os.Open(file) //nolint:gosec // G304: a path the go command reported for its own build cache
	})
	return l
}

// checkModule type-checks every package of the module in `go list` order — dependencies first. In
// export mode it answers errExportFormat as soon as one dependency's export data cannot be read, so
// the caller can start over from source rather than map a module whose imports all failed.
func (l *goLoader) checkModule(ctx context.Context, listed []*listedPackage) ([]*goPackage, error) {
	out := make([]*goPackage, 0, len(l.module))
	for _, p := range listed {
		if !l.module[p.ImportPath] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, l.check(p))
		if l.formatErr != nil {
			return nil, l.formatErr
		}
	}
	return out, nil
}

// check parses and type-checks a module package, after the module packages it imports.
func (l *goLoader) check(p *listedPackage) *goPackage {
	if checked, ok := l.checked[p.ImportPath]; ok {
		return checked
	}
	pkgPath, _, variant := strings.Cut(p.ImportPath, " [")
	gp := &goPackage{ID: p.ImportPath, PkgPath: pkgPath, Fset: l.fset, Variant: variant}
	l.checked[p.ImportPath] = gp // before recursing: an import cycle must not loop
	if p.Error != nil && p.Error.Err != "" {
		gp.Errors = append(gp.Errors, fmt.Errorf("%s", p.Error.Err)) //nolint:err113 // the go command's own message
	}

	for _, path := range p.Imports {
		if l.module[path] {
			l.check(l.byID[path])
		}
	}

	var parseErrs []error
	gp.Syntax, parseErrs = l.parse(p, parser.ParseComments)
	gp.Errors = append(gp.Errors, parseErrs...)

	gp.Info = &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Instances:  map[*ast.Ident]types.Instance{},
	}
	config := types.Config{
		Importer: importerFunc(func(path string) (*types.Package, error) { return l.importFor(p, path) }),
		Sizes:    l.sizes,
		// cgo files are checked as they are written; `import "C"` resolves to a stand-in.
		FakeImportC: len(p.CgoFiles) > 0,
		// Keep going after an error: a half-typed package still maps.
		Error: func(err error) { gp.Errors = append(gp.Errors, err) },
	}
	// Every error already reached config.Error; the package is kept even when there were some.
	gp.Types, _ = config.Check(pkgPath, l.fset, gp.Syntax, gp.Info)
	return gp
}

func (l *goLoader) parse(p *listedPackage, mode parser.Mode) ([]*ast.File, []error) {
	names := append(append([]string(nil), p.GoFiles...), p.CgoFiles...)
	files := make([]*ast.File, 0, len(names))
	var errs []error
	for _, name := range names {
		file := name
		if !filepath.IsAbs(file) {
			file = filepath.Join(p.Dir, name)
		}
		syntax, err := parser.ParseFile(l.fset, file, nil, mode|parser.SkipObjectResolution)
		if err != nil {
			errs = append(errs, err)
		}
		if syntax != nil {
			files = append(files, syntax)
		}
	}
	return files, errs
}

// importFor resolves one import of p: a module package from its own check, a dependency from its
// export data or its source.
func (l *goLoader) importFor(p *listedPackage, path string) (*types.Package, error) {
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	id := path
	if mapped, ok := p.ImportMap[path]; ok {
		id = mapped
	}
	if l.module[id] {
		if checked := l.check(l.byID[id]); checked.Types != nil {
			return checked.Types, nil
		}
		return nil, fmt.Errorf("%s did not type-check", path) //nolint:err113 // names the package
	}
	if l.mode == sourceMode {
		return l.dependency(id)
	}
	return l.readExport(path)
}

// readExport reads a dependency's export data. The standard library's reader panics, rather than
// failing, on a format newer than it knows; that panic is the signal to start over from source.
func (l *goLoader) readExport(path string) (pkg *types.Package, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			l.formatErr = errExportFormat
			pkg, err = nil, fmt.Errorf("%w: %v", errExportFormat, recovered)
		}
	}()
	pkg, err = l.gc.Import(path)
	if err != nil {
		if strings.Contains(err.Error(), "version") {
			l.formatErr = errExportFormat
		}
		return nil, fmt.Errorf("importing %s: %w", path, err)
	}
	return pkg, nil
}

// dependency type-checks a dependency's declarations from source. Bodies are skipped — only the
// API a module package can call matters — and errors are ignored: a dependency that half-checks
// still answers the declarations the module uses.
func (l *goLoader) dependency(id string) (*types.Package, error) {
	if pkg, seen := l.deps[id]; seen {
		if pkg == nil {
			return nil, fmt.Errorf("%s did not type-check", id) //nolint:err113 // names the package
		}
		return pkg, nil
	}
	p := l.byID[id]
	if p == nil {
		return nil, fmt.Errorf("%s is not in the module's dependencies", id) //nolint:err113 // names the package
	}
	l.deps[id] = nil // an import cycle must not loop
	files, _ := l.parse(p, 0)
	pkgPath, _, _ := strings.Cut(p.ImportPath, " [")
	config := types.Config{
		Importer:         importerFunc(func(path string) (*types.Package, error) { return l.importFor(p, path) }),
		Sizes:            l.sizes,
		IgnoreFuncBodies: true,
		FakeImportC:      len(p.CgoFiles) > 0,
		Error:            func(error) {},
	}
	pkg, _ := config.Check(pkgPath, l.fset, files, nil)
	l.deps[id] = pkg
	return pkg, nil
}

type importerFunc func(path string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }
