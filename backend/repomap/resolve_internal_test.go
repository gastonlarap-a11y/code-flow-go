package repomap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// row is a file with only what the resolver reads: its path, language and package.
func row(path string, lang Lang, pkg ...string) fileRow {
	facts := &Facts{Lang: lang, Parsed: true}
	if len(pkg) > 0 {
		facts.Package = pkg[0]
	}
	return fileRow{Path: path, Facts: facts}
}

// resolvedPaths resolves spec from the file at `from` and answers the paths it named, the package
// outside, and whether the answer is known.
func resolvedPaths(t *testing.T, r *resolver, from, spec string) ([]string, string, bool) {
	t.Helper()
	i, ok := r.index[from]
	require.True(t, ok, "no file %s", from)
	res := r.resolve(i, spec)
	paths := make([]string, 0, len(res.files))
	for _, f := range res.files {
		paths = append(paths, r.files[f].Path)
	}
	return paths, res.external, res.known
}

type resolveCase struct {
	name, from, spec string
	files            []string
	external         string
	known            bool
}

func runResolve(t *testing.T, r *resolver, tests []resolveCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, external, known := resolvedPaths(t, r, tt.from, tt.spec)
			want := tt.files
			if want == nil {
				want = []string{}
			}
			assert.Equal(t, want, files)
			assert.Equal(t, tt.external, external)
			assert.Equal(t, tt.known, known)
		})
	}
}

func TestTypeScriptImportsResolveThroughTheRepositorysOwnConfig(t *testing.T) {
	r := newResolver([]fileRow{
		row("web/src/app.ts", LangTypeScript),
		row("web/src/util.ts", LangTypeScript),
		row("web/src/esm.ts", LangTypeScript),
		row("web/src/components/Button.tsx", LangTSX),
		row("web/src/lib/index.ts", LangTypeScript),
		row("packages/ui/src/button.ts", LangTypeScript),
		row("packages/ui/src/index.ts", LangTypeScript),
	}, manifests{
		"web/tsconfig.json": []byte(`{
			// TypeScript allows comments and trailing commas
			"extends": "./tsconfig.base.json",
			"compilerOptions": { "paths": { "@/*": ["*"], }, },
		}`),
		"web/tsconfig.base.json":   []byte(`{"compilerOptions": {"baseUrl": "src"}}`),
		"packages/ui/package.json": []byte(`{"name": "@acme/ui"}`),
	})
	runResolve(t, r, []resolveCase{
		{"a relative import", "web/src/app.ts", "./util", []string{"web/src/util.ts"}, "", true},
		{"ESM names the source .js", "web/src/app.ts", "./esm.js", []string{"web/src/esm.ts"}, "", true},
		{"a folder's index", "web/src/components/Button.tsx", "../lib", []string{"web/src/lib/index.ts"}, "", true},
		{"a paths alias, relative to the inherited baseUrl", "web/src/app.ts", "@/components/Button", []string{"web/src/components/Button.tsx"}, "", true},
		{"baseUrl alone", "web/src/app.ts", "lib", []string{"web/src/lib/index.ts"}, "", true},
		{"another package of the monorepo", "web/src/app.ts", "@acme/ui/button", []string{"packages/ui/src/button.ts"}, "", true},
		{"that package's own entry", "web/src/app.ts", "@acme/ui", []string{"packages/ui/src/index.ts"}, "", true},
		{"a package from npm", "web/src/app.ts", "react", nil, "react", true},
		{"a scoped package's subpath", "web/src/app.ts", "@tanstack/query/core", nil, "@tanstack/query", true},
		{"a stylesheet has nothing to follow", "web/src/app.ts", "./app.css", nil, "", true},
		{"a missing relative file is unknown", "web/src/app.ts", "./missing", nil, "", false},
	})
}

func TestGoImportsResolveByModule(t *testing.T) {
	r := newResolver([]fileRow{
		row("main.go", LangGo, "main"),
		row("internal/cart/cart.go", LangGo, "cart"),
		row("internal/cart/total.go", LangGo, "cart"),
		row("internal/cart/cart_test.go", LangGo, "cart"),
		row("tools/gen/main.go", LangGo, "main"),
	}, manifests{
		"go.mod":       []byte("module github.com/acme/shop\n\ngo 1.27\n"),
		"tools/go.mod": []byte("module github.com/acme/shop/tools\n"),
	})
	runResolve(t, r, []resolveCase{
		{"a package of the module, without its tests", "main.go", "github.com/acme/shop/internal/cart", []string{"internal/cart/cart.go", "internal/cart/total.go"}, "", true},
		{"the standard library", "main.go", "net/http", nil, "net/http", true},
		{"a module from elsewhere, by its repository", "main.go", "github.com/stretchr/testify/require", nil, "github.com/stretchr/testify", true},
		{"a missing package of the module", "main.go", "github.com/acme/shop/internal/gone", nil, "", false},
		{"a nested module wins over the one around it", "main.go", "github.com/acme/shop/tools/gen", []string{"tools/gen/main.go"}, "", true},
	})
}

func TestPythonImportsResolveFromThePackageRoots(t *testing.T) {
	r := newResolver([]fileRow{
		row("src/shop/__init__.py", LangPython),
		row("src/shop/models.py", LangPython),
		row("src/shop/services/pay.py", LangPython),
		row("src/shop/services/__init__.py", LangPython),
	}, manifests{"pyproject.toml": []byte("[project]\nname = 'shop'\n")})
	runResolve(t, r, []resolveCase{
		{"one dot is the module's own package", "src/shop/services/pay.py", ".", []string{"src/shop/services/__init__.py"}, "", true},
		{"two dots climb one package", "src/shop/services/pay.py", "..models", []string{"src/shop/models.py"}, "", true},
		{"an absolute import from src", "src/shop/services/pay.py", "shop.models", []string{"src/shop/models.py"}, "", true},
		{"a name inside a module", "src/shop/services/pay.py", "shop.models.Order", []string{"src/shop/models.py"}, "", true},
		{"an installed package", "src/shop/services/pay.py", "requests.adapters", nil, "requests", true},
		{"a missing relative module is unknown", "src/shop/services/pay.py", ".gone", nil, "", false},
	})
}

func TestJavaImportsResolveByPackageAndTypeName(t *testing.T) {
	r := newResolver([]fileRow{
		row("app/src/main/java/com/acme/shop/Cart.java", LangJava, "com.acme.shop"),
		row("app/src/main/java/com/acme/shop/model/Item.java", LangJava, "com.acme.shop.model"),
		row("app/src/main/java/com/acme/shop/model/Price.java", LangJava, "com.acme.shop.model"),
	}, manifests{})
	from := "app/src/main/java/com/acme/shop/Cart.java"
	runResolve(t, r, []resolveCase{
		{"a type", from, "com.acme.shop.model.Item", []string{"app/src/main/java/com/acme/shop/model/Item.java"}, "", true},
		{"a whole package", from, "com.acme.shop.model.*", []string{"app/src/main/java/com/acme/shop/model/Item.java", "app/src/main/java/com/acme/shop/model/Price.java"}, "", true},
		{"a static member", from, "static com.acme.shop.model.Price.ZERO", []string{"app/src/main/java/com/acme/shop/model/Price.java"}, "", true},
		{"the JDK", from, "java.util.List", nil, "java.util", true},
		{"a missing type of our own packages", from, "com.acme.shop.Gone", nil, "", false},
	})
}

func TestDartImportsResolveThroughThePubspec(t *testing.T) {
	r := newResolver([]fileRow{
		row("app/lib/main.dart", LangDart),
		row("app/lib/src/cart.dart", LangDart),
	}, manifests{"app/pubspec.yaml": []byte("name: shop\ndescription: a shop\n")})
	runResolve(t, r, []resolveCase{
		{"package: of this pubspec", "app/lib/main.dart", "package:shop/src/cart.dart", []string{"app/lib/src/cart.dart"}, "", true},
		{"a relative import", "app/lib/main.dart", "src/cart.dart", []string{"app/lib/src/cart.dart"}, "", true},
		{"a package from pub", "app/lib/main.dart", "package:flutter/material.dart", nil, "flutter", true},
		{"the SDK", "app/lib/main.dart", "dart:async", nil, "dart:async", true},
		{"a missing file of this package", "app/lib/main.dart", "package:shop/gone.dart", nil, "", false},
	})
}

func TestATSConfigIsReadAsTypeScriptReadsIt(t *testing.T) {
	got := string(lenientJSON([]byte(`{
		"url": "http://example.com/a//b", /* a block comment */
		"list": [1, 2,],
		"escaped": "a \" // not a comment",
	}`)))
	assert.JSONEq(t, `{"url": "http://example.com/a//b", "list": [1, 2], "escaped": "a \" // not a comment"}`, got)
}
