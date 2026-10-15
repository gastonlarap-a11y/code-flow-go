package archify

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"

	"github.com/gastonlarap-a11y/code-flow/backend/platform"
)

// Routing a class diagram's relationships through the gaps of Archify's grid (ARCHIFY-006).
//
// Archify joins two boxes by its own orthogonal route, which does not steer around a third box, and
// its validator refuses a line drawn through an unrelated one — on a folder of forty types, several
// always are. Its grid leaves a gap after every row and every column, and no box sits in one, since
// each takes a whole cell (placeOnGrid never uses a half step). So a line that leaves its box
// straight into the gap beside it, travels only along gaps and enters its target the same way
// crosses nothing. Where the boxes are depends on their members and on Archify's text metrics, so
// it is asked of Archify's class renderer — on the document without its relationships, which do
// not move a box — rather than guessed.

// placedBox is a box as Archify's class renderer placed it.
type placedBox struct {
	ID     string  `json:"id"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Row    int     `json:"row"`
	Col    float64 `json:"col"`
}

// ErrNoClassRenderer is an install whose class renderer is not where Archify 3.1 keeps it.
var ErrNoClassRenderer = errors.New("this Archify install has no class renderer to lay the diagram out")

func (c CLI) classRenderer() string {
	return filepath.Join(filepath.Dir(c.Script), "..", "renderers", "class", "render-class.mjs")
}

// maxAlignRounds bounds how many times the ends of the routes are moved onto Archify's ports.
const maxAlignRounds = 4

// RouteClassDiagram answers the class document with every relationship routed along the grid's
// gaps, and Archify's verdict on it. ErrNoClassRenderer when the install cannot say where its boxes
// go.
//
// Where a line meets a box is Archify's to decide — it spreads the ends that share a side — and a
// route's first and last stretches must run straight along that side's direction. Archify's report
// names the port of each end that does not, so those ends are moved onto it and the document is
// checked again.
func (c CLI) RouteClassDiagram(ctx context.Context, rt Runtime, root string, document []byte) ([]byte, Report, error) {
	if _, err := os.Stat(c.classRenderer()); err != nil {
		return nil, Report{}, ErrNoClassRenderer
	}
	var doc classDoc
	if err := json.Unmarshal(document, &doc); err != nil {
		return nil, Report{}, fmt.Errorf("%w: %w", ErrNotADocument, err)
	}
	if len(doc.Relationships) > 0 {
		boxes, err := c.placeBoxes(ctx, rt, root, doc)
		if err != nil {
			return nil, Report{}, err
		}
		routeRelationships(boxes, doc.Relationships)
	}
	for round := 0; ; round++ {
		raw, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return nil, Report{}, fmt.Errorf("writing the class diagram: %w", err)
		}
		raw = append(raw, '\n')
		report, err := c.Validate(ctx, rt, root, raw)
		if err != nil || report.OK || round == maxAlignRounds || alignToPorts(doc.Relationships, report.Diagnostics) == 0 {
			return raw, report, err
		}
	}
}

// sideEvidence is what Archify measured of a route's end that leaves or enters its box the wrong
// way: the end's segment, the port being its first point for the source and its last for the target.
type sideEvidence struct {
	Endpoint string     `json:"endpoint"`
	Side     string     `json:"side"`
	From     [2]float64 `json:"from"`
	To       [2]float64 `json:"to"`
}

// alignToPorts moves each misaligned end's turning point onto the port Archify reported, and
// answers how many it moved.
func alignToPorts(relationships []classRelationship, diagnostics []Diagnostic) int {
	moved := 0
	for _, d := range diagnostics {
		if d.Code != "clean-flow/endpoint-side-direction" || d.Subject.Collection != "relationships" || d.Subject.Index == nil {
			continue
		}
		i := *d.Subject.Index
		var e sideEvidence
		if i < 0 || i >= len(relationships) || len(relationships[i].Via) == 0 || json.Unmarshal(d.Evidence, &e) != nil {
			continue
		}
		via := relationships[i].Via
		point, port := &via[0], e.From
		if e.Endpoint == "target" {
			point, port = &via[len(via)-1], e.To
		}
		axis := 0 // a top or bottom side: the stretch is vertical, so x is the port's
		if e.Side == "left" || e.Side == "right" {
			axis = 1
		}
		if point[axis] != port[axis] {
			point[axis] = port[axis]
			moved++
		}
	}
	return moved
}

// placeBoxes asks the class renderer where it puts every box of doc, drawn without relationships.
// A document that cites a commit is checked against the checkout first, as every render is.
func (c CLI) placeBoxes(ctx context.Context, rt Runtime, root string, doc classDoc) ([]placedBox, error) {
	doc.Relationships = []classRelationship{}
	staged, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("writing the document to lay out: %w", err)
	}
	dir, err := os.MkdirTemp("", "codeflow-archify-layout-")
	if err != nil {
		return nil, fmt.Errorf("creating a scratch folder: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }() // scratch: losing it costs nothing
	if err := os.WriteFile(filepath.Join(dir, "doc.json"), staged, platform.FilePerm); err != nil {
		return nil, fmt.Errorf("writing the document to lay out: %w", err)
	}
	var settings []string
	if doc.Meta.Repository != nil {
		settings = []string{"ARCHIFY_REPO_ROOT=" + root} // the renderer's --repo-root
	}
	out, stderr, err := c.runScript(ctx, rt, dir, settings, c.classRenderer(), "doc.json", "--layout-json")
	if err != nil {
		return nil, fmt.Errorf("archify could not lay out the class diagram: %s", firstLine(problem(stderr+string(out), err))) //nolint:err113 // Archify's own words are the message
	}
	var layout struct {
		Types []placedBox `json:"types"`
	}
	if err := json.Unmarshal(out, &layout); err != nil || len(layout.Types) == 0 {
		return nil, fmt.Errorf("archify answered no layout for the class diagram: %s", firstLine(string(out))) //nolint:err113 // Archify's own words are the message
	}
	return layout.Types, nil
}

// band is the extent a row or a column of boxes takes.
type band struct{ lo, hi float64 }

// gaps are the lines a route may run along: the middle of the gap before and after each row and
// column, keyed by the index of the row or column after it, and the lanes already taken on each.
type gaps struct {
	rows, cols     map[int]band
	rowKeys        []int
	colKeys        []int
	rowLane        map[int]int
	colLane        map[int]int
	outerX, outerY float64
}

// laneStep is how far apart two routes along one gap run, so that neither hides the other.
const laneStep = 9

func newGaps(boxes []placedBox) *gaps {
	g := &gaps{rows: map[int]band{}, cols: map[int]band{}, rowLane: map[int]int{}, colLane: map[int]int{}}
	widen := func(bands map[int]band, key int, lo, hi float64) {
		b, ok := bands[key]
		if !ok {
			bands[key] = band{lo, hi}
			return
		}
		bands[key] = band{math.Min(b.lo, lo), math.Max(b.hi, hi)}
	}
	for _, b := range boxes {
		widen(g.rows, b.Row, b.Y, b.Y+b.Height)
		widen(g.cols, int(b.Col), b.X, b.X+b.Width)
	}
	for k := range g.rows {
		g.rowKeys = append(g.rowKeys, k)
	}
	for k := range g.cols {
		g.colKeys = append(g.colKeys, k)
	}
	slices.Sort(g.rowKeys)
	slices.Sort(g.colKeys)
	// Past the last row or column, as far out as Archify's default gap would reach.
	g.outerY, g.outerX = 42, 64
	return g
}

// lane offsets the n-th route along a gap: 0, +step, -step, +2·step… within half the gap.
func lane(n int, half float64) float64 {
	step := float64((n+1)/2) * laneStep
	if step > half-8 {
		step = math.Mod(step, math.Max(half-8, laneStep))
	}
	if n%2 == 1 {
		return step
	}
	return -step
}

// above is the y of the gap above row r, on its next free lane.
func (g *gaps) above(r int) float64 {
	i := slices.Index(g.rowKeys, r)
	var y, half float64
	if i <= 0 {
		y, half = g.rows[r].lo-g.outerY, g.outerY
	} else {
		prev := g.rows[g.rowKeys[i-1]]
		y, half = (prev.hi+g.rows[r].lo)/2, (g.rows[r].lo-prev.hi)/2
	}
	n := g.rowLane[r]
	g.rowLane[r]++
	return y + lane(n, half)
}

// below is the y of the gap below row r — the gap above the next row, or past the last.
func (g *gaps) below(r int) float64 {
	i := slices.Index(g.rowKeys, r)
	if i >= 0 && i+1 < len(g.rowKeys) {
		return g.above(g.rowKeys[i+1])
	}
	key := r + 1<<20 // the gap past the last row has a lane count of its own
	n := g.rowLane[key]
	g.rowLane[key]++
	return g.rows[r].hi + g.outerY + lane(n, g.outerY)
}

// column is the x of the vertical gap nearest the middle of two points, on its next free lane: one
// between two columns, or past the last when there is only one column.
func (g *gaps) column(fromX, toX float64) float64 {
	type candidate struct {
		key  int
		x    float64
		half float64
	}
	options := make([]candidate, 0, len(g.colKeys))
	for i := 1; i < len(g.colKeys); i++ {
		prev, next := g.cols[g.colKeys[i-1]], g.cols[g.colKeys[i]]
		options = append(options, candidate{g.colKeys[i], (prev.hi + next.lo) / 2, (next.lo - prev.hi) / 2})
	}
	if len(options) == 0 {
		last := g.cols[g.colKeys[len(g.colKeys)-1]]
		options = append(options, candidate{1 << 20, last.hi + g.outerX, g.outerX})
	}
	best := slices.MinFunc(options, func(a, b candidate) int {
		cost := func(c candidate) float64 { return math.Abs(c.x-fromX) + math.Abs(c.x-toX) }
		return cmp.Or(cmp.Compare(cost(a), cost(b)), cmp.Compare(a.key, b.key))
	})
	n := g.colLane[best.key]
	g.colLane[best.key]++
	return best.x + lane(n, best.half)
}

// routeRelationships gives each relationship its sides and the points it turns at:
//   - two boxes side by side in one row join straight, facing each other;
//   - otherwise the line leaves its box into the gap above or below it, runs along that gap — and,
//     when rows lie between the two, up or down the vertical gap between two columns and along the
//     gap next to its target — and enters the target from the gap beside it.
func routeRelationships(boxes []placedBox, relationships []classRelationship) {
	byID := make(map[string]placedBox, len(boxes))
	for _, b := range boxes {
		byID[b.ID] = b
	}
	g := newGaps(boxes)
	// The longest first, so they take the lanes nearest the middle of each gap.
	order := make([]int, len(relationships))
	for i := range order {
		order[i] = i
	}
	span := func(r classRelationship) float64 {
		a, b := byID[r.From], byID[r.To]
		return math.Abs(float64(a.Row-b.Row))*1000 + math.Abs(a.Col-b.Col)
	}
	slices.SortStableFunc(order, func(i, j int) int { return cmp.Compare(span(relationships[j]), span(relationships[i])) })

	for _, i := range order {
		r := &relationships[i]
		from, okFrom := byID[r.From]
		to, okTo := byID[r.To]
		if !okFrom || !okTo {
			continue
		}
		fx, tx := from.X+from.Width/2, to.X+to.Width/2
		switch {
		case from.Row == to.Row && math.Abs(from.Col-to.Col) == 1:
			r.FromSide, r.ToSide = "right", "left"
			if to.Col < from.Col {
				r.FromSide, r.ToSide = "left", "right"
			}
		case from.Row == to.Row:
			y := g.above(from.Row)
			r.FromSide, r.ToSide = "top", "top"
			r.Via = [][2]float64{{fx, y}, {tx, y}}
		case to.Row < from.Row:
			r.FromSide, r.ToSide = "top", "bottom"
			r.Via = alongGaps(g, fx, g.above(from.Row), tx, to.Row, from.Row, g.below)
		default:
			r.FromSide, r.ToSide = "bottom", "top"
			r.Via = alongGaps(g, fx, g.below(from.Row), tx, to.Row, from.Row, g.above)
		}
	}
}

// alongGaps runs from the gap next to the source, at y, to the gap next to the target's row: along
// that one gap when no row lies between the two, else across through a vertical gap.
func alongGaps(g *gaps, fx, y, tx float64, toRow, fromRow int, besideTarget func(int) float64) [][2]float64 {
	if g.neighbours(toRow, fromRow) {
		return [][2]float64{{fx, y}, {tx, y}}
	}
	x := g.column(fx, tx)
	ty := besideTarget(toRow)
	return [][2]float64{{fx, y}, {x, y}, {x, ty}, {tx, ty}}
}

// neighbours says whether no row of boxes lies between two rows.
func (g *gaps) neighbours(a, b int) bool {
	i, j := slices.Index(g.rowKeys, a), slices.Index(g.rowKeys, b)
	return i >= 0 && j >= 0 && (i-j == 1 || j-i == 1)
}
