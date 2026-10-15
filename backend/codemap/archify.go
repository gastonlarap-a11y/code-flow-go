package codemap

import (
	"cmp"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/archify"
)

// The map as Archify diagrams (CODEMAP-017): an architecture overview and a flow's sequence, as
// Archify 3.x documents (schema_version 1) rendered by the user's own Archify install. Positions,
// labels and citations come from the map, never from a model; Archify validates the document —
// including that every cited line exists at the pinned commit — before it writes the HTML.

// Archify diagram types this package writes.
const (
	ArchifyArchitecture = "architecture"
	ArchifySequence     = "sequence"
)

// ErrNoSequence is a flow that never leaves one file or package: a sequence of it has no arrows.
var ErrNoSequence = errors.New("this flow stays inside one file or package, so there is no sequence to draw") //nolint:staticcheck // ST1005: shown to the user as a sentence

type archifyMeta struct {
	Title          string              `json:"title"`
	Subtitle       string              `json:"subtitle,omitempty"`
	Output         string              `json:"output"`
	Locale         string              `json:"locale,omitempty"`
	QualityProfile string              `json:"quality_profile,omitempty"`
	Repository     *archify.Repository `json:"repository,omitempty"`
	Legend         *archifyLegend      `json:"legend,omitempty"`
}

type archifyLegend struct {
	Entries map[string]archifyLegendEntry `json:"entries"`
}

type archifyLegendEntry struct {
	Label string `json:"label"`
}

type archifyCard struct {
	Dot   string   `json:"dot"`
	Title string   `json:"title"`
	Items []string `json:"items"`
}

type archifyArchitectureDoc struct {
	SchemaVersion int                 `json:"schema_version"`
	DiagramType   string              `json:"diagram_type"`
	Meta          archifyMeta         `json:"meta"`
	Components    []archifyComponent  `json:"components"`
	Boundaries    []archifyBoundary   `json:"boundaries,omitempty"`
	Connections   []archifyConnection `json:"connections,omitempty"`
	Cards         []archifyCard       `json:"cards,omitempty"`
}

type archifyComponent struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Label    string           `json:"label"`
	Sublabel string           `json:"sublabel,omitempty"`
	Sources  []archify.Source `json:"sources,omitempty"`
	Pos      [2]float64       `json:"pos"`
	Size     [2]float64       `json:"size"`
}

type archifyBoundary struct {
	Kind  string   `json:"kind"`
	Label string   `json:"label"`
	Wraps []string `json:"wraps"`
}

type archifyConnection struct {
	ID      string `json:"id,omitempty"`
	From    string `json:"from"`
	To      string `json:"to"`
	Label   string `json:"label,omitempty"`
	Variant string `json:"variant,omitempty"`
}

type archifySequenceDoc struct {
	SchemaVersion int                  `json:"schema_version"`
	DiagramType   string               `json:"diagram_type"`
	Meta          archifyMeta          `json:"meta"`
	Participants  []archifyParticipant `json:"participants"`
	Messages      []archifyMessage     `json:"messages"`
	Cards         []archifyCard        `json:"cards,omitempty"`
}

type archifyParticipant struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Label    string           `json:"label"`
	Sublabel string           `json:"sublabel,omitempty"`
	Sources  []archify.Source `json:"sources,omitempty"`
}

type archifyMessage struct {
	From    string  `json:"from"`
	To      string  `json:"to"`
	Y       float64 `json:"y"`
	Label   string  `json:"label"`
	Variant string  `json:"variant,omitempty"`
}

// archifyWords are the few words a diagram says, per interface language.
type archifyWords struct {
	database, frontend, backend, flow, overview, calls, reads, writes, tables, symbols, cut, notDrawn string
	legendCross, legendCall, legendRead                                                               string
}

var archifyLanguages = map[string]archifyWords{
	"en": {
		database: "Database", frontend: "Frontend", backend: "Backend (Go)", flow: "Flow of %s", overview: "Architecture",
		calls: "%d calls", reads: "reads %d", writes: "writes %d", tables: "%d tables", symbols: "%d symbols",
		cut: "%d more steps did not fit: draw the flow from a symbol further down", notDrawn: "What the diagram leaves out",
		legendCross: "into Go", legendCall: "call / write", legendRead: "table read",
	},
	"es": {
		database: "Base de datos", frontend: "Frontend", backend: "Backend (Go)", flow: "Flujo de %s", overview: "Arquitectura",
		calls: "%d llamadas", reads: "lee %d", writes: "escribe %d", tables: "%d tablas", symbols: "%d símbolos",
		cut: "%d pasos más no caben: dibuja el flujo desde un símbolo más abajo", notDrawn: "Lo que el diagrama no muestra",
		legendCross: "a Go", legendCall: "llamada / escritura", legendRead: "lectura de tabla",
	},
}

func wordsFor(language string) (archifyWords, string) {
	if strings.HasPrefix(strings.ToLower(language), "es") {
		return archifyLanguages["es"], "es"
	}
	return archifyLanguages["en"], "en"
}

// Archify's layout rules, from its authoring guide: lifelines narrow as participants are added
// (86px at seven), messages need 28px each, and a label between two boxes needs 6.5px per
// character plus 21px.
const (
	maxParticipants = 7
	maxMessages     = 40
	firstMessageY   = 170.0
	messageStep     = 28.0
	boxWidth        = 170.0
	boxHeight       = 64.0
	canvasOrigin    = 40.0
	maxLabelRunes   = 16
)

var archifyIDChars = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// archifyIDs hands out Archify IDs (^[a-zA-Z][a-zA-Z0-9_-]*$), unique per document.
type archifyIDs map[string]bool

func (seen archifyIDs) next(name string) string {
	id := strings.Trim(archifyIDChars.ReplaceAllString(name, "-"), "-")
	if id == "" || !isASCIILetter(id[0]) {
		id = "n-" + id
	}
	base := id
	for i := 2; seen[id]; i++ {
		id = base + "-" + strconv.Itoa(i)
	}
	seen[id] = true
	return id
}

func isASCIILetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// lane is a lifeline of a sequence: a TypeScript file, a Go package, or the database.
type lane struct {
	key, kind, label, sublabel string
}

func laneOf(s SymbolRef, words archifyWords) lane {
	switch {
	case s.Kind == KindTable.String():
		return lane{key: "db", kind: "database", label: words.database}
	case s.Lang == LangGo.String():
		pkg, _, _ := strings.Cut(s.Name, ".")
		return lane{key: "go:" + path.Dir(s.File), kind: "backend", label: clip(pkg, maxLabelRunes), sublabel: "Go"}
	}
	base := strings.TrimSuffix(path.Base(s.File), path.Ext(s.File))
	return lane{key: "ts:" + s.File, kind: "frontend", label: clip(base, maxLabelRunes), sublabel: clip(path.Dir(s.File), 28)}
}

// archifySequenceOf draws a flow as a sequence: an arrow each time the flow leaves one file or
// package for another, a dashed one for a table read. A hop through an endpoint is one arrow,
// labelled with the endpoint's key, from the caller to the package of the handler.
func archifySequenceOf(f Flow, repo archify.Repo, output, language string) (archifySequenceDoc, error) {
	words, locale := wordsFor(language)
	b := &sequenceBuilder{repo: repo, words: words, ids: archifyIDs{}, lanes: map[string]int{}, y: firstMessageY}
	b.walk(f.Root, laneOf(f.Root.Symbol, words))
	if len(b.messages) == 0 {
		return archifySequenceDoc{}, ErrNoSequence
	}

	cited := false
	for i := range b.participants {
		b.participants[i].Sources = cite(repo, b.sources[i])
		cited = cited || len(b.participants[i].Sources) > 0
	}
	doc := archifySequenceDoc{
		SchemaVersion: 1,
		DiagramType:   ArchifySequence,
		Meta: archifyMeta{
			Title:          clip(fmt.Sprintf(words.flow, f.Root.Symbol.Name), 80),
			Output:         output,
			Locale:         locale,
			QualityProfile: "standard",
			Repository:     repo.Pinned(cited),
			Legend: &archifyLegend{Entries: map[string]archifyLegendEntry{
				"emphasis": {Label: words.legendCross},
				"default":  {Label: words.legendCall},
				"dashed":   {Label: words.legendRead},
			}},
		},
		Participants: b.participants,
		Messages:     b.messages,
	}
	if b.dropped > 0 {
		doc.Cards = []archifyCard{{Dot: "amber", Title: words.notDrawn, Items: []string{fmt.Sprintf(words.cut, b.dropped)}}}
	}
	return doc, nil
}

type sequenceBuilder struct {
	repo         archify.Repo
	words        archifyWords
	ids          archifyIDs
	participants []archifyParticipant
	sources      [][]SymbolRef
	lanes        map[string]int
	messages     []archifyMessage
	seen         map[string]bool
	y            float64
	dropped      int
}

// lifeline answers a lane's participant ID, adding it on first use; "" when the diagram already
// has as many lifelines as Archify can draw.
func (b *sequenceBuilder) lifeline(l lane, cite SymbolRef) string {
	if i, ok := b.lanes[l.key]; ok {
		if len(b.sources[i]) < 3 && cite.File != "" {
			b.sources[i] = append(b.sources[i], cite)
		}
		return b.participants[i].ID
	}
	if len(b.participants) == maxParticipants {
		return ""
	}
	b.lanes[l.key] = len(b.participants)
	b.participants = append(b.participants, archifyParticipant{ID: b.ids.next(l.key), Type: l.kind, Label: l.label, Sublabel: l.sublabel})
	b.sources = append(b.sources, []SymbolRef{cite})
	return b.participants[len(b.participants)-1].ID
}

func (b *sequenceBuilder) message(from, to, label, variant string) {
	if from == "" || to == "" || from == to {
		if from != to {
			b.dropped++
		}
		return
	}
	if b.seen == nil {
		b.seen = map[string]bool{}
	}
	key := from + "|" + to + "|" + label
	if b.seen[key] {
		return
	}
	b.seen[key] = true
	if len(b.messages) == maxMessages {
		b.dropped++
		return
	}
	b.messages = append(b.messages, archifyMessage{From: from, To: to, Y: b.y, Label: clip(label, 40), Variant: variant})
	b.y += messageStep
}

func (b *sequenceBuilder) walk(n FlowNode, current lane) {
	from := b.lifeline(current, n.Symbol)
	for _, child := range n.Children {
		switch {
		case child.Symbol.Kind == KindEndpoint.String():
			// The endpoint names the hop; the handler behind it is where the flow lands.
			for _, handler := range child.Children {
				target := laneOf(handler.Symbol, b.words)
				b.message(from, b.lifeline(target, handler.Symbol), child.Symbol.Name, "emphasis")
				b.walk(handler, target)
			}
		case child.Symbol.Kind == KindTable.String():
			verb := "mention"
			variant := ""
			switch child.Via {
			case EdgeRead.String():
				verb, variant = "read", "dashed"
			case EdgeWrite.String():
				verb = "write"
			}
			label := child.Symbol.Name
			if verb != "mention" {
				label = verb + " " + child.Symbol.Name
			}
			b.message(from, b.lifeline(laneOf(child.Symbol, b.words), child.Symbol), label, variant)
		default:
			target := laneOf(child.Symbol, b.words)
			if target.key != current.key {
				variant := ""
				if child.Via == EdgeCross.String() {
					variant = "emphasis"
				}
				b.message(from, b.lifeline(target, child.Symbol), shortSymbol(child.Symbol.Name)+"()", variant)
			}
			b.walk(child, target)
		}
	}
}

// shortSymbol is a symbol's last segment, as a message names it.
func shortSymbol(name string) string {
	head, _, _ := strings.Cut(name, "(")
	return head[strings.LastIndexByte(head, '.')+1:]
}

// architecture: the busiest folders of each side, the tables, and what joins them.
const (
	architectureAreasPerSide = 4
	// citationCandidates is how many of an area's most used symbols are offered for its three
	// citations: some may sit in files with uncommitted changes, which are never cited.
	citationCandidates = 10
)

func archifyArchitectureOf(g *Graph, overview Overview, repo archify.Repo, output, language string) archifyArchitectureDoc {
	words, locale := wordsFor(language)
	ids := archifyIDs{}

	pick := func(lang Lang) []Area {
		areas := make([]Area, 0, architectureAreasPerSide)
		for _, area := range overview.Areas {
			if area.Lang == lang.String() && len(areas) < architectureAreasPerSide {
				areas = append(areas, area)
			}
		}
		return areas
	}
	frontend, backend := pick(LangTS), pick(LangGo)

	areaOf := func(n *Node) string {
		if n.Kind == KindTable {
			return "db"
		}
		return n.Lang.String() + ":" + path.Dir(n.Pos.File)
	}
	// A folder is named by its last segment, unless another chosen folder shares it — `lib/api` and
	// `components/api` would otherwise be two boxes both called "api".
	baseCount := map[string]int{}
	for _, area := range append(append([]Area(nil), frontend...), backend...) {
		baseCount[path.Base(area.Path)]++
	}
	areaLabel := func(area Area) string {
		base := path.Base(area.Path)
		if baseCount[base] > 1 {
			return path.Base(path.Dir(area.Path)) + "/" + base
		}
		return base
	}

	component := map[string]string{}
	components := make([]archifyComponent, 0, len(frontend)+len(backend)+1)
	citable := map[string][]*Node{}
	place := func(area Area, column int, row int, kind string) {
		key := area.Lang + ":" + area.Path
		label := areaLabel(area)
		id := ids.next(label)
		component[key] = id
		components = append(components, archifyComponent{
			ID: id, Type: kind, Label: clip(label, 24), Sublabel: clip(area.Path, 30) + " · " + fmt.Sprintf(words.symbols, area.Symbols),
			Pos:  [2]float64{canvasOrigin + float64(column)*(boxWidth+180), canvasOrigin + float64(row)*(boxHeight+40)},
			Size: [2]float64{boxWidth, boxHeight},
		})
	}
	for i, area := range frontend {
		place(area, 0, i, "frontend")
	}
	for i, area := range backend {
		place(area, 1, i, "backend")
	}
	if len(overview.Tables) > 0 {
		id := ids.next("database")
		component["db"] = id
		components = append(components, archifyComponent{
			ID: id, Type: "database", Label: words.database, Sublabel: fmt.Sprintf(words.tables, len(overview.Tables)),
			Pos:  [2]float64{canvasOrigin + 2*(boxWidth+180), canvasOrigin},
			Size: [2]float64{boxWidth, boxHeight},
		})
	}

	// What joins the areas: hops into Go, and table reads and writes, counted from the map.
	type link struct{ from, to string }
	calls := map[link]int{}
	reads, writes := map[link]map[string]bool{}, map[link]map[string]bool{}
	for id, n := range g.Nodes {
		if isTestNode(n) {
			continue
		}
		fromID, ok := component[areaOf(n)]
		if !ok {
			continue
		}
		if n.Kind != KindModule && n.Pos.File != "" {
			citable[fromID] = append(citable[fromID], n)
		}
		for _, e := range g.Out[id] {
			to := g.Nodes[e.To]
			if e.Kind == EdgeCross && to.Kind == KindEndpoint {
				for _, d := range g.Out[to.ID] {
					to = g.Nodes[d.To]
				}
			}
			toID, ok := component[areaOf(to)]
			if !ok || toID == fromID {
				continue
			}
			l := link{fromID, toID}
			switch e.Kind {
			case EdgeCross:
				calls[l]++
			case EdgeRead:
				if reads[l] == nil {
					reads[l] = map[string]bool{}
				}
				reads[l][to.ID] = true
			case EdgeWrite:
				if writes[l] == nil {
					writes[l] = map[string]bool{}
				}
				writes[l][to.ID] = true
			}
		}
	}

	connections := make([]archifyConnection, 0, len(calls)+len(reads)+len(writes))
	for l, n := range calls {
		connections = append(connections, archifyConnection{From: l.from, To: l.to, Label: fmt.Sprintf(words.calls, n), Variant: "emphasis"})
	}
	data := map[link]bool{}
	for l := range reads {
		data[l] = true
	}
	for l := range writes {
		data[l] = true
	}
	for l := range data {
		parts := make([]string, 0, 2)
		if n := len(writes[l]); n > 0 {
			parts = append(parts, fmt.Sprintf(words.writes, n))
		}
		if n := len(reads[l]); n > 0 {
			parts = append(parts, fmt.Sprintf(words.reads, n))
		}
		connections = append(connections, archifyConnection{From: l.from, To: l.to, Label: strings.Join(parts, " · ")})
	}
	slices.SortFunc(connections, func(a, b archifyConnection) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To), cmp.Compare(a.Variant, b.Variant), cmp.Compare(a.Label, b.Label))
	})
	for i := range connections {
		connections[i].ID = ids.next("link-" + connections[i].From + "-" + connections[i].To)
	}

	// An area is cited by its most used symbols, so the same map always cites the same lines.
	cited := false
	for i := range components {
		nodes := citable[components[i].ID]
		slices.SortFunc(nodes, func(a, b *Node) int {
			return cmp.Or(cmp.Compare(len(g.In[b.ID]), len(g.In[a.ID])), cmp.Compare(a.ID, b.ID))
		})
		refs := make([]SymbolRef, 0, citationCandidates)
		for _, n := range nodes[:min(len(nodes), citationCandidates)] {
			refs = append(refs, ref(n))
		}
		components[i].Sources = cite(repo, refs)
		cited = cited || len(components[i].Sources) > 0
	}
	boundaries := make([]archifyBoundary, 0, 2)
	for _, side := range []struct {
		label string
		areas []Area
	}{{words.frontend, frontend}, {words.backend, backend}} {
		wraps := make([]string, 0, len(side.areas))
		for _, area := range side.areas {
			wraps = append(wraps, component[area.Lang+":"+area.Path])
		}
		if len(wraps) > 0 {
			boundaries = append(boundaries, archifyBoundary{Kind: "region", Label: side.label, Wraps: wraps})
		}
	}

	return archifyArchitectureDoc{
		SchemaVersion: 1,
		DiagramType:   ArchifyArchitecture,
		Meta: archifyMeta{
			Title:          words.overview,
			Output:         output,
			Locale:         locale,
			QualityProfile: "standard",
			Repository:     repo.Pinned(cited),
		},
		Components:  components,
		Boundaries:  boundaries,
		Connections: connections,
	}
}
