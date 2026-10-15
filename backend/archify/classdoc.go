package archify

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/repomap"
)

// A class diagram drawn from the map (ARCHIFY-006): the types of a folder or a file with their
// fields and methods, and how they relate — read from the code, never asked of a model.

// MaxClassTypes bounds a class diagram. Past it, the most used types are drawn and the subtitle says
// how many were left out: a diagram of a hundred boxes is one nobody reads.
const MaxClassTypes = 40

// maxClassColumns is how many boxes one row of the grid holds before it continues on the next.
const maxClassColumns = 6

// archifyGridLimit is the largest row or column Archify's class grid accepts.
const archifyGridLimit = 12

type classDoc struct {
	SchemaVersion int                 `json:"schema_version"`
	DiagramType   string              `json:"diagram_type"`
	Meta          classMeta           `json:"meta"`
	Layout        classLayout         `json:"layout"`
	Types         []classType         `json:"types"`
	Relationships []classRelationship `json:"relationships"`
}

type classMeta struct {
	Title      string      `json:"title"`
	Subtitle   string      `json:"subtitle,omitempty"`
	Locale     string      `json:"locale,omitempty"`
	Repository *Repository `json:"repository,omitempty"`
}

type classLayout struct {
	Mode string `json:"mode"`
}

type classType struct {
	ID         string           `json:"id"`
	Label      string           `json:"label"`
	Kind       string           `json:"kind"`
	Sublabel   string           `json:"sublabel,omitempty"`
	Row        int              `json:"row"`
	Col        float64          `json:"col"`
	Attributes []classAttribute `json:"attributes,omitempty"`
	Methods    []classMethod    `json:"methods,omitempty"`
	Sources    []Source         `json:"sources,omitempty"`
}

type classAttribute struct {
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	Visibility string `json:"visibility,omitempty"`
	Static     bool   `json:"static,omitempty"`
}

type classMethod struct {
	Name       string `json:"name"`
	Parameters string `json:"parameters,omitempty"`
	Returns    string `json:"returns,omitempty"`
	Visibility string `json:"visibility,omitempty"`
	Abstract   bool   `json:"abstract,omitempty"`
}

type classRelationship struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
	// The route through the grid's gaps (classroute.go), when Archify could say where its boxes are.
	FromSide string       `json:"fromSide,omitempty"`
	ToSide   string       `json:"toSide,omitempty"`
	Via      [][2]float64 `json:"via,omitempty"`
}

// ClassDiagram is what a class diagram was built from.
type ClassDiagram struct {
	Document []byte
	Types    int
	Omitted  int
}

// BuildClassDiagram draws the types declared under scope, its subtitle saying how many of how many
// were drawn. ErrNoTypes when there are none.
func BuildClassDiagram(snap *repomap.Snapshot, repo Repo, scope, title string, subtitle func(drawn, total int) string, locale string) (ClassDiagram, error) {
	all := snap.Types(scope)
	if len(all) == 0 {
		return ClassDiagram{}, ErrNoTypes
	}
	drawn := all[:min(MaxClassTypes, len(all))]

	types := make([]classType, 0, len(drawn))
	taken := make(map[string]bool)
	cited := false
	for _, t := range drawn {
		id := uniqueID(t.Name, taken)
		box := classType{
			ID:         id,
			Label:      t.Name,
			Kind:       classKind(t),
			Sublabel:   t.Path,
			Attributes: make([]classAttribute, 0, len(t.Fields)),
			Methods:    make([]classMethod, 0, len(t.Methods)),
			Sources:    repo.Cite([]Ref{{Path: t.Path, Line: t.Line, EndLine: t.EndLine}}),
		}
		cited = cited || len(box.Sources) > 0
		for _, f := range t.Fields {
			box.Attributes = append(box.Attributes, classAttribute{Name: f.Name, Type: f.Type, Visibility: visibility(f.Visibility), Static: f.Static})
		}
		for _, m := range t.Methods {
			params, returns := splitSignature(m.Signature, m.Name)
			box.Methods = append(box.Methods, classMethod{Name: m.Name, Parameters: params, Returns: returns, Visibility: visibility(m.Visibility), Abstract: m.Abstract})
		}
		types = append(types, box)
	}

	relationships := relate(drawn, types)
	placeOnGrid(types, relationships)

	doc := classDoc{
		SchemaVersion: 1,
		DiagramType:   "class",
		Meta:          classMeta{Title: title, Subtitle: subtitle(len(types), len(all)), Locale: locale, Repository: repo.Pinned(cited)},
		Layout:        classLayout{Mode: "grid"},
		Types:         types,
		Relationships: relationships,
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return ClassDiagram{}, fmt.Errorf("writing the class diagram: %w", err)
	}
	return ClassDiagram{Document: append(raw, '\n'), Types: len(types), Omitted: len(all) - len(types)}, nil
}

// ErrNoTypes is a scope that declares no class, interface, enum, struct, record or mixin.
var ErrNoTypes = errors.New("nothing here declares a class, an interface, an enum or a struct")

func classKind(t repomap.TypeInfo) string {
	switch t.Kind {
	case repomap.KindInterface:
		return "interface"
	case repomap.KindEnum:
		return "enum"
	case repomap.KindRecord:
		return "record"
	case repomap.KindMixin:
		return "abstract" // a mixin carries an implementation, like an abstract class
	}
	if t.Abstract {
		return "abstract"
	}
	return "class"
}

// visibility is what Archify's schema accepts, "" when the language says nothing.
func visibility(v string) string {
	switch v {
	case "public", "protected", "package", "private":
		return v
	}
	return ""
}

var idUnsafe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func isLetter(c byte) bool { return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }

// uniqueID is a type's name as an Archify id: a letter first, then letters, digits, `_` and `-`.
func uniqueID(name string, taken map[string]bool) string {
	id := strings.Trim(idUnsafe.ReplaceAllString(name, "_"), "_-")
	if id == "" || !isLetter(id[0]) {
		id = "t_" + id
	}
	base := id
	for n := 2; taken[id]; n++ {
		id = fmt.Sprintf("%s_%d", base, n)
	}
	taken[id] = true
	return id
}

// qualifiedName is a type name as code writes it, with the package or namespace before it when there
// is one: `Item`, `storage.Store`, `com.acme.Cart`.
var qualifiedName = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*`)

// names are the type names a piece of code writes, each split into its qualifier — the segment just
// before it, "" for none — and the name itself.
func names(code string) [][2]string {
	out := make([][2]string, 0, 4)
	for _, written := range qualifiedName.FindAllString(code, -1) {
		parts := strings.Split(written, ".")
		qualifier := ""
		if len(parts) > 1 {
			qualifier = parts[len(parts)-2]
		}
		out = append(out, [2]string{qualifier, parts[len(parts)-1]})
	}
	return out
}

// resolver finds the drawn type a name written in another drawn type means. A folder holds several
// packages, and they reuse names — every one of them may have a `Deps` and a `Service` — so the first
// type of a name is not good enough.
type resolver struct {
	drawn  []repomap.TypeInfo
	byName map[string][]int
}

func newResolver(drawn []repomap.TypeInfo) resolver {
	r := resolver{drawn: drawn, byName: make(map[string][]int, len(drawn))}
	for i, t := range drawn {
		r.byName[t.Name] = append(r.byName[t.Name], i)
	}
	return r
}

// resolve answers the index of the type that `qualifier.name`, written in the type at from, names:
// - with a qualifier, the one whose package is that qualifier, or none;
// - without, in Go the one of its own package, since Go names another package's type only qualified;
// - without, elsewhere the one in its own folder, else the only one of that name, else none.
func (r resolver) resolve(from int, qualifier, name string) (int, bool) {
	candidates := r.byName[name]
	here := r.drawn[from]
	dir := path.Dir(here.Path)
	pick := func(accept func(t repomap.TypeInfo) bool) (int, bool) {
		found := -1
		for _, c := range candidates {
			if accept(r.drawn[c]) {
				if found >= 0 {
					return 0, false // two that fit is no answer
				}
				found = c
			}
		}
		return found, found >= 0
	}
	if qualifier != "" {
		return pick(func(t repomap.TypeInfo) bool {
			pkg := t.Package
			if dot := strings.LastIndexByte(pkg, '.'); dot >= 0 {
				pkg = pkg[dot+1:] // Java's `com.acme` is written `acme.Cart`
			}
			return pkg == qualifier || (pkg == "" && path.Base(path.Dir(t.Path)) == qualifier)
		})
	}
	if found, ok := pick(func(t repomap.TypeInfo) bool { return path.Dir(t.Path) == dir }); ok || here.Lang == repomap.LangGo {
		return found, ok
	}
	if len(candidates) == 1 {
		return candidates[0], true
	}
	return 0, false
}

// relate draws what the code says between the drawn types: what each extends and implements, the
// types its fields hold, and the types its methods take or return. Between two types only the
// strongest of those is drawn.
func relate(drawn []repomap.TypeInfo, types []classType) []classRelationship {
	strength := map[string]int{"inheritance": 4, "realization": 3, "composition": 2, "dependency": 1}
	best := make(map[[2]string]string)
	r := newResolver(drawn)
	add := func(from int, qualifier, name, kind string) {
		to, ok := r.resolve(from, qualifier, name)
		if !ok || to == from {
			return
		}
		key := [2]string{types[from].ID, types[to].ID}
		if strength[kind] > strength[best[key]] {
			best[key] = kind
		}
	}
	for i, t := range drawn {
		for _, base := range t.Bases {
			// A Go struct's embedded struct is held, not inherited; an embedded interface is its base.
			if t.Lang == repomap.LangGo && t.Kind == repomap.KindStruct {
				add(i, "", base, "composition")
				continue
			}
			add(i, "", base, "inheritance")
		}
		for _, iface := range t.Implements {
			add(i, "", iface, "realization")
		}
		for _, f := range t.Fields {
			for _, n := range names(f.Type) {
				add(i, n[0], n[1], "composition")
			}
		}
		for _, m := range t.Methods {
			params, returns := splitSignature(m.Signature, m.Name)
			for _, n := range names(params + " " + returns) {
				add(i, n[0], n[1], "dependency")
			}
		}
	}
	out := make([]classRelationship, 0, len(best))
	for key, kind := range best {
		out = append(out, classRelationship{From: key[0], To: key[1], Kind: kind})
	}
	slices.SortFunc(out, func(a, b classRelationship) int { return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To)) })
	return out
}

// splitSignature reads a method's parameters and return type off its one-line signature, whichever
// side of the name the language writes the return on: `total(x: number): Price`,
// `def total(self) -> int`, `func (c *Cart) Total() int`, `public int total(int x)`.
func splitSignature(signature, name string) (params, returns string) {
	at := strings.Index(signature, name+"(")
	if at < 0 {
		return "", ""
	}
	open := at + len(name)
	depth, end := 0, -1
	for i := open; i < len(signature); i++ {
		switch signature[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return "", ""
	}
	params = strings.TrimSpace(signature[open+1 : end])
	after := strings.TrimSpace(signature[end+1:])
	switch {
	case strings.HasPrefix(after, "->"):
		returns = strings.TrimSpace(strings.TrimPrefix(after, "->"))
	case strings.HasPrefix(after, ":"):
		returns = strings.TrimSpace(strings.TrimPrefix(after, ":"))
	case after != "" && !strings.HasPrefix(after, "{") && !strings.HasPrefix(after, "throws"):
		returns = after // Go writes the result after the parameters
	default:
		returns = typeBefore(signature[:at])
	}
	return params, strings.TrimSuffix(returns, ";")
}

// modifiers are the words before a C-like method's name that are not its return type.
var modifiers = map[string]bool{
	"public": true, "private": true, "protected": true, "static": true, "final": true, "abstract": true,
	"synchronized": true, "native": true, "default": true, "async": true, "override": true, "external": true,
	"get": true, "set": true, "factory": true, "const": true, "def": true, "function": true, "func": true,
}

// typeBefore is the return type a C-like language writes before the method's name, or "".
func typeBefore(head string) string {
	for _, word := range slices.Backward(strings.Fields(strings.TrimSpace(head))) {
		if !modifiers[word] && !strings.HasPrefix(word, "@") {
			return word
		}
	}
	return ""
}

// placeOnGrid gives every box a row and a column: what a type extends or implements sits in a row
// above it, the types of one level are ordered under the ones they come from, and a level wider
// than a row continues on the next. Archify's class grid needs a place for every box.
func placeOnGrid(types []classType, relationships []classRelationship) {
	index := make(map[string]int, len(types))
	for i, t := range types {
		index[t.ID] = i
	}
	parents := make(map[int][]int)
	for _, r := range relationships {
		if r.Kind == "inheritance" || r.Kind == "realization" {
			parents[index[r.From]] = append(parents[index[r.From]], index[r.To])
		}
	}

	level := make([]int, len(types))
	for range types { // relaxed until it settles; a cycle is cut by the bound
		changed := false
		for child, ps := range parents {
			for _, p := range ps {
				if level[p]+1 > level[child] && level[p]+1 <= archifyGridLimit {
					level[child], changed = level[p]+1, true
				}
			}
		}
		if !changed {
			break
		}
	}

	columns := maxClassColumns
	for rowsNeeded(level, columns) > archifyGridLimit+1 && columns < archifyGridLimit+1 {
		columns++
	}

	position := make(map[int]float64, len(types))
	row := 0
	for depth := 0; depth <= slices.Max(level); depth++ {
		members := make([]int, 0, 8)
		for i, l := range level {
			if l == depth {
				members = append(members, i)
			}
		}
		// Under the ones they come from: by the mean column of their parents, then by name.
		slices.SortStableFunc(members, func(a, b int) int {
			return cmp.Or(cmp.Compare(meanColumn(parents[a], position), meanColumn(parents[b], position)), cmp.Compare(types[a].Label, types[b].Label))
		})
		for start := 0; start < len(members); start += columns {
			chunk := members[start:min(start+columns, len(members))]
			// Whole columns only. Archify takes a half step, but a box on one spans two cells and
			// fills the gap between them — the gap the relationships are routed along.
			offset := (columns - len(chunk)) / 2
			for c, i := range chunk {
				types[i].Row = min(row, archifyGridLimit)
				types[i].Col = float64(min(offset+c, archifyGridLimit))
				position[i] = types[i].Col
			}
			row++
		}
	}
}

func rowsNeeded(level []int, columns int) int {
	if len(level) == 0 {
		return 0
	}
	counts := make(map[int]int)
	for _, l := range level {
		counts[l]++
	}
	rows := 0
	for _, n := range counts {
		rows += (n + columns - 1) / columns
	}
	return rows
}

func meanColumn(parents []int, position map[int]float64) float64 {
	if len(parents) == 0 {
		return 1e9 // a type nothing above it explains goes after the ones that are
	}
	sum := 0.0
	for _, p := range parents {
		sum += position[p]
	}
	return sum / float64(len(parents))
}
