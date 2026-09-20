package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

// Reading the Go side: every behaviour this repository actually pins.
//
// Through `go/ast` rather than a regex, for one reason that matters here: a great many of these
// tests are table-driven with the case name as a map key, so the sentences live in composite
// literals rather than in function names. A regex that only found `func Test…` would miss them and
// report the port as far less covered than it is.

// goBehaviour is one thing the Go tests assert, wherever its name was written.
type goBehaviour struct {
	// name is the sentence: a test function's name, a subtest string, or a table case's key.
	name string
	// pkg is the directory it lives in, which is what maps back to a C# folder.
	pkg string
	// file is for the report, so a match can be looked at.
	file string
	// sig is the name's evidence-bearing words, computed once.
	sig map[string]bool
}

// collectGoBehaviours walks a directory tree and reads every `_test.go` in it.
func collectGoBehaviours(root string) ([]goBehaviour, error) {
	var found []goBehaviour

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}

		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}

		pkg := filepath.ToSlash(filepath.Dir(path))
		for _, name := range behavioursIn(parsed) {
			found = append(found, goBehaviour{
				name: name,
				pkg:  pkg,
				file: filepath.ToSlash(path),
				sig:  signature(name),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// behavioursIn pulls every named behaviour out of one parsed test file.
//
// Three places a name can be written, and all three are read:
//
//   - the test function itself, `func TestAnAddedFileHasNoOldPath`;
//   - a subtest's first argument, `t.Run("a rename keeps both paths", …)`;
//   - a table case's key, `for name, tc := range map[string]struct{…}{"a rename …": {…}}`.
//
// The third is the one a regex would miss, and it is where most of this repository's table-driven
// cases put their sentences.
func behavioursIn(file *ast.File) []string {
	var names []string

	for _, decl := range file.Decls {
		fn, isFunc := decl.(*ast.FuncDecl)
		if !isFunc || fn.Name == nil || fn.Body == nil {
			continue
		}
		if !strings.HasPrefix(fn.Name.Name, "Test") && !strings.HasPrefix(fn.Name.Name, "Benchmark") {
			continue
		}
		names = append(names, fn.Name.Name)

		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.CallExpr:
				if name, ok := subtestName(typed); ok {
					names = append(names, name)
				}
			case *ast.CompositeLit:
				names = append(names, mapKeysOf(typed)...)
			}
			return true
		})
	}
	return names
}

// subtestName reads the first argument of a `t.Run("…", …)` call.
func subtestName(call *ast.CallExpr) (string, bool) {
	selector, isSelector := call.Fun.(*ast.SelectorExpr)
	if !isSelector || selector.Sel.Name != "Run" || len(call.Args) == 0 {
		return "", false
	}
	return stringLiteral(call.Args[0])
}

// mapKeysOf reads the keys of a `map[string]…` literal.
//
// Only string-keyed maps, and only literal keys: a table's case names are written out, and
// anything computed is not a sentence anybody wrote.
func mapKeysOf(literal *ast.CompositeLit) []string {
	mapType, isMap := literal.Type.(*ast.MapType)
	if !isMap {
		return nil
	}
	if ident, ok := mapType.Key.(*ast.Ident); !ok || ident.Name != "string" {
		return nil
	}

	var keys []string
	for _, element := range literal.Elts {
		pair, isPair := element.(*ast.KeyValueExpr)
		if !isPair {
			continue
		}
		if key, ok := stringLiteral(pair.Key); ok {
			keys = append(keys, key)
		}
	}
	return keys
}

func stringLiteral(expr ast.Expr) (string, bool) {
	literal, isLiteral := expr.(*ast.BasicLit)
	if !isLiteral || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil || strings.TrimSpace(value) == "" {
		return "", false
	}
	return value, true
}
