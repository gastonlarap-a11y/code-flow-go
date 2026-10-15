//go:build cgo

package repomap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func read(t *testing.T, lang Lang, src string) *Facts {
	t.Helper()
	p := newParser()
	defer p.close()
	facts := extract(p, lang, []byte(src))
	require.True(t, facts.Parsed, "the %s grammar read the file", lang)
	return facts
}

// declNamed is the first declaration of a name.
func declNamed(t *testing.T, facts *Facts, name string) Decl {
	t.Helper()
	for _, d := range facts.Decls {
		if d.Name == name {
			return d
		}
	}
	require.Failf(t, "no declaration", "%s", name)
	return Decl{}
}

func importsOf(facts *Facts) []string {
	out := make([]string, 0, len(facts.Imports))
	for _, imp := range facts.Imports {
		out = append(out, imp.Spec)
	}
	return out
}

func fieldNames(d Decl) []string {
	out := make([]string, 0, len(d.Fields))
	for _, f := range d.Fields {
		out = append(out, f.Name)
	}
	return out
}

func TestEveryLanguagesQueriesCompile(t *testing.T) {
	for lang, g := range grammars {
		t.Run(string(lang), func(t *testing.T) {
			c, err := compile(g)
			require.NoError(t, err)
			c.close()
		})
	}
}

func TestTypeScriptDeclarationsMembersAndImports(t *testing.T) {
	facts := read(t, LangTypeScript, `import { a } from "./a";
export { c } from "./c";
const d = require("d");
const e = await import("./e");
export abstract class Cart<T> extends Base implements Shop, Other {
  private readonly items: Item[] = [];
  static count = 0;
  #secret: string;
  protected total(x: number): Price { return x; }
  handler = (e: Event) => {};
}
export interface Shop extends Store { name: string; buy(n: number): void; }
export type Id = string;
export enum Color { Red, Green }
export const Button = () => null;
`)
	cart := declNamed(t, facts, "Cart")
	assert.Equal(t, KindClass, cart.Kind)
	assert.True(t, cart.Abstract)
	assert.Equal(t, []string{"Base"}, cart.Bases)
	assert.Equal(t, []string{"Shop", "Other"}, cart.Implements)
	assert.Equal(t, "export abstract class Cart<T> extends Base implements Shop, Other", cart.Signature)
	assert.Equal(t, []Member{
		{Name: "items", Type: "Item[]", Line: 6, Visibility: "private"},
		{Name: "count", Line: 7, Visibility: "public", Static: true},
		{Name: "#secret", Type: "string", Line: 8, Visibility: "private"},
	}, cart.Fields, "a field whose value is a function is a method, not a field")

	total := declNamed(t, facts, "total")
	assert.Equal(t, KindMethod, total.Kind)
	assert.Equal(t, "protected", total.Visibility)
	assert.Equal(t, "Cart", facts.Decls[total.Parent].Name)
	assert.Equal(t, KindMethod, declNamed(t, facts, "handler").Kind)

	shop := declNamed(t, facts, "Shop")
	assert.Equal(t, KindInterface, shop.Kind)
	assert.Equal(t, []string{"Store"}, shop.Bases)
	assert.Equal(t, []string{"name"}, fieldNames(shop))
	assert.Equal(t, "Shop", facts.Decls[declNamed(t, facts, "buy").Parent].Name)

	assert.Equal(t, KindType, declNamed(t, facts, "Id").Kind)
	assert.Equal(t, "export type Id = string", declNamed(t, facts, "Id").Signature)
	assert.Equal(t, []string{"Red", "Green"}, fieldNames(declNamed(t, facts, "Color")))
	assert.Equal(t, KindFunction, declNamed(t, facts, "Button").Kind, "an arrow function bound to a name")
	assert.Equal(t, KindConst, declNamed(t, facts, "d").Kind)

	assert.Equal(t, []string{"./a", "./c", "d", "./e"}, importsOf(facts))
}

func TestJavaScriptAndJSX(t *testing.T) {
	facts := read(t, LangJavaScript, `import Thing from "./thing.js";
export class Panel extends React.Component { static title = "x"; render() { return <Thing/>; } }
export function useCart() {}
`)
	panel := declNamed(t, facts, "Panel")
	assert.Equal(t, []string{"Component"}, panel.Bases)
	assert.Equal(t, []string{"title"}, fieldNames(panel))
	assert.Equal(t, "Panel", facts.Decls[declNamed(t, facts, "render").Parent].Name)
	assert.Equal(t, []string{"./thing.js"}, importsOf(facts))
	assert.Contains(t, refNames(facts), "Thing", "a JSX tag is a mention")
}

func refNames(facts *Facts) []string {
	out := make([]string, 0, len(facts.Refs))
	for _, r := range facts.Refs {
		out = append(out, r.Name)
	}
	return out
}

func TestGoTypesMethodsAndEmbedding(t *testing.T) {
	facts := read(t, LangGo, `package cart

import (
	"fmt"
	alias "github.com/acme/shop/price"
)

type Cart struct {
	Items []Item
	price.Calculator
	*Base
	total, tax int
}

type Store interface {
	io.Reader
	Save(c *Cart) error
}

func (c *Cart) Total() int { return 0 }
func (s Elsewhere) Do() {}
func New() *Cart { return nil }
`)
	assert.Equal(t, "cart", facts.Package)
	cart := declNamed(t, facts, "Cart")
	assert.Equal(t, KindStruct, cart.Kind)
	assert.Equal(t, "type Cart struct", cart.Signature)
	assert.Equal(t, []string{"Calculator", "Base"}, cart.Bases, "embedded types")
	assert.Equal(t, []Member{
		{Name: "Items", Type: "[]Item", Line: 9, Visibility: "public"},
		{Name: "total", Type: "int", Line: 12, Visibility: "private"},
		{Name: "tax", Type: "int", Line: 12, Visibility: "private"},
	}, cart.Fields)

	store := declNamed(t, facts, "Store")
	assert.Equal(t, KindInterface, store.Kind)
	assert.Equal(t, []string{"Reader"}, store.Bases)

	total := declNamed(t, facts, "Total")
	assert.Equal(t, "Cart", total.Owner)
	assert.Equal(t, "Cart", facts.Decls[total.Parent].Name, "a method sits under its receiver declared in the file")
	do := declNamed(t, facts, "Do")
	assert.Equal(t, "Elsewhere", do.Owner)
	assert.Equal(t, -1, do.Parent, "a receiver declared in another file of the package")
	assert.Equal(t, "public", do.Visibility, "exported")
	assert.Equal(t, "private", cart.Fields[1].Visibility, "unexported")
	assert.Equal(t, []string{"fmt", "github.com/acme/shop/price"}, importsOf(facts))
}

// A grouped block is several declarations: each starts on its own line and reads as itself, while a
// single one starts at its keyword.
func TestGoGroupedDeclarationsStartAtTheirOwnLine(t *testing.T) {
	facts := read(t, LangGo, `package cart

const (
	// maxItems bounds a cart.
	maxItems = 40
	minItems = 1
)

var Default = New()

type (
	Item struct{ Name string }
	ID   string
)
`)
	tests := []struct {
		name      string
		line      int
		signature string
	}{
		{"maxItems", 5, "const maxItems = 40"},
		{"minItems", 6, "const minItems = 1"},
		{"Default", 9, "var Default = New()"},
		{"Item", 12, "type Item struct"},
		{"ID", 13, "type ID string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := declNamed(t, facts, tt.name)
			assert.Equal(t, tt.line, d.Line)
			assert.Equal(t, tt.signature, d.Signature)
		})
	}
	assert.Equal(t, KindConst, declNamed(t, facts, "maxItems").Kind)
	assert.Equal(t, KindVar, declNamed(t, facts, "Default").Kind, "a var is not a constant")
}

func TestALetIsAVariableAndAConstAConstant(t *testing.T) {
	for _, lang := range []Lang{LangTypeScript, LangJavaScript} {
		t.Run(string(lang), func(t *testing.T) {
			facts := read(t, lang, "export const LIMIT = 3\nlet count = 0\nexport let current = null\nconst helper = () => 1\n")
			assert.Equal(t, KindConst, declNamed(t, facts, "LIMIT").Kind)
			assert.Equal(t, KindVar, declNamed(t, facts, "count").Kind)
			assert.Equal(t, KindVar, declNamed(t, facts, "current").Kind)
			assert.Equal(t, KindFunction, declNamed(t, facts, "helper").Kind, "an arrow function is still a function")
		})
	}
}

func TestPythonClassesFieldsAndRelativeImports(t *testing.T) {
	facts := read(t, LangPython, `import os, sys as system
from . import models
from ..core import Base
from .x import Y as Z

class Cart(Base, metaclass=Meta):
    count: int = 0
    def __init__(self):
        self.items = []
        self._private = 1
    @property
    def total(self) -> int:
        return 0

def helper(a: int) -> str:
    pass

MAX = 3
logger = None
`)
	cart := declNamed(t, facts, "Cart")
	assert.Equal(t, []string{"Base"}, cart.Bases, "a keyword argument is not a base")
	assert.Equal(t, []Member{
		{Name: "count", Type: "int", Line: 7, Visibility: "public"},
		{Name: "items", Line: 9, Visibility: "public"},
		{Name: "_private", Line: 10, Visibility: "protected"},
	}, cart.Fields)
	total := declNamed(t, facts, "total")
	assert.Equal(t, KindMethod, total.Kind, "a def inside a class")
	assert.Equal(t, 11, total.Line, "its decorator is where it starts")
	assert.Equal(t, KindFunction, declNamed(t, facts, "helper").Kind)
	assert.Equal(t, KindConst, declNamed(t, facts, "MAX").Kind)
	for _, d := range facts.Decls {
		assert.NotEqual(t, "logger", d.Name, "a lower-case module variable is not a constant")
	}
	assert.Equal(t, []string{"os", "sys", ".", ".models", "..core", ".x"}, importsOf(facts))
}

func TestJavaTypesModifiersAndImports(t *testing.T) {
	facts := read(t, LangJava, `package com.acme.shop;
import java.util.List;
import static com.acme.Price.ZERO;
import com.acme.model.*;
public abstract class Cart<T> extends Base implements Shop, Other {
  private final List<Item> items = new ArrayList<>();
  public static int count, other;
  public int total(int x) { return x; }
  interface Inner extends Shop { void buy(); }
  record Point(int x, int y) implements Shape {}
}
`)
	assert.Equal(t, "com.acme.shop", facts.Package)
	cart := declNamed(t, facts, "Cart")
	assert.True(t, cart.Abstract)
	assert.Equal(t, []string{"Base"}, cart.Bases)
	assert.Equal(t, []string{"Shop", "Other"}, cart.Implements)
	assert.Equal(t, []Member{
		{Name: "items", Type: "List<Item>", Line: 6, Visibility: "private"},
		{Name: "count", Type: "int", Line: 7, Visibility: "public", Static: true},
		{Name: "other", Type: "int", Line: 7, Visibility: "public", Static: true},
	}, cart.Fields)
	assert.Equal(t, []string{"Shop"}, declNamed(t, facts, "Inner").Bases)
	assert.Equal(t, "public", declNamed(t, facts, "buy").Visibility, "an interface's methods are public")
	point := declNamed(t, facts, "Point")
	assert.Equal(t, KindRecord, point.Kind)
	assert.Equal(t, []string{"x", "y"}, fieldNames(point))
	assert.Equal(t, []string{"java.util.List", "static com.acme.Price.ZERO", "com.acme.model.*"}, importsOf(facts))
}

func TestDartClassesMixinsAndDirectives(t *testing.T) {
	facts := read(t, LangDart, `import 'package:flutter/material.dart';
export 'src/price.dart';
part 'cart.g.dart';
abstract class Cart extends Base with Mixin implements Shop {
  final List<Item> items = [];
  static int count = 0;
  String _secret;
  void add(Item item) {
    print(item);
  }
}
mixin Mixin on Base { void m() {} }
void main() {}
`)
	cart := declNamed(t, facts, "Cart")
	assert.True(t, cart.Abstract)
	assert.Equal(t, []string{"Base"}, cart.Bases)
	assert.Equal(t, []string{"Mixin", "Shop"}, cart.Implements, "a mixin is realised like an interface")
	assert.Equal(t, []Member{
		{Name: "items", Type: "List<Item>", Line: 5, Visibility: "public"},
		{Name: "count", Type: "int", Line: 6, Visibility: "public", Static: true},
		{Name: "_secret", Type: "String", Line: 7, Visibility: "private"},
	}, cart.Fields)
	add := declNamed(t, facts, "add")
	assert.Equal(t, KindMethod, add.Kind)
	assert.Equal(t, 8, add.Line)
	assert.Equal(t, 10, add.EndLine, "a Dart body is the node after its signature")
	assert.Equal(t, []string{"Base"}, declNamed(t, facts, "Mixin").Bases)
	assert.Equal(t, KindFunction, declNamed(t, facts, "main").Kind)
	assert.Equal(t, []string{"package:flutter/material.dart", "src/price.dart", "cart.g.dart"}, importsOf(facts))
}

func TestBrokenCodeStillYieldsWhatParses(t *testing.T) {
	facts := read(t, LangTypeScript, "export function ok() {}\nclass {{{ broken\nexport function alsoOk() {}\n")
	names := make([]string, 0, len(facts.Decls))
	for _, d := range facts.Decls {
		names = append(names, d.Name)
	}
	assert.Contains(t, names, "ok")
}
