package archify

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grid lays boxes out the way Archify's banded grid does: every column 200 wide with a 128 gap,
// every row 120 tall with an 84 gap.
func grid(cells map[string][2]int) []placedBox {
	boxes := make([]placedBox, 0, len(cells))
	for id, rc := range cells {
		boxes = append(boxes, placedBox{ID: id, X: float64(rc[1]) * 328, Y: float64(rc[0]) * 204, Width: 200, Height: 120, Row: rc[0], Col: float64(rc[1])})
	}
	return boxes
}

func anchor(b placedBox, side string) [2]float64 {
	switch side {
	case "top":
		return [2]float64{b.X + b.Width/2, b.Y}
	case "bottom":
		return [2]float64{b.X + b.Width/2, b.Y + b.Height}
	case "left":
		return [2]float64{b.X, b.Y + b.Height/2}
	}
	return [2]float64{b.X + b.Width, b.Y + b.Height/2}
}

// crosses says whether the segment a→b passes through the inside of box.
func crosses(a, b [2]float64, box placedBox) bool {
	if a[0] == b[0] { // vertical
		lo, hi := math.Min(a[1], b[1]), math.Max(a[1], b[1])
		return a[0] > box.X && a[0] < box.X+box.Width && hi > box.Y && lo < box.Y+box.Height
	}
	lo, hi := math.Min(a[0], b[0]), math.Max(a[0], b[0])
	return a[1] > box.Y && a[1] < box.Y+box.Height && hi > box.X && lo < box.X+box.Width
}

func TestARouteRunsOnlyAlongTheGridsGaps(t *testing.T) {
	boxes := grid(map[string][2]int{
		"Top": {0, 1},
		"L1":  {1, 0}, "M1": {1, 1}, "R1": {1, 2},
		"L2": {2, 0}, "Bottom": {2, 1}, "R2": {2, 2},
		"Far": {3, 2},
	})
	rels := []classRelationship{
		{From: "Bottom", To: "Top", Kind: "composition"}, // straight up would run through M1
		{From: "Far", To: "L1", Kind: "dependency"},      // across two rows and two columns
		{From: "L2", To: "R2", Kind: "dependency"},       // one row, not side by side
		{From: "L1", To: "M1", Kind: "dependency"},       // side by side
		{From: "Top", To: "R1", Kind: "dependency"},      // neighbouring rows
	}
	routeRelationships(boxes, rels)

	byID := map[string]placedBox{}
	for _, b := range boxes {
		byID[b.ID] = b
	}
	for _, r := range rels {
		t.Run(r.From+"→"+r.To, func(t *testing.T) {
			from, to := byID[r.From], byID[r.To]
			points := append([][2]float64{anchor(from, r.FromSide)}, r.Via...)
			points = append(points, anchor(to, r.ToSide))
			for i := 1; i < len(points); i++ {
				a, b := points[i-1], points[i]
				assert.True(t, a[0] == b[0] || a[1] == b[1], "segment %d %v→%v is not orthogonal", i, a, b)
				for _, box := range boxes {
					if box.ID != r.From && box.ID != r.To {
						assert.False(t, crosses(a, b, box), "segment %d %v→%v runs through %s", i, a, b, box.ID)
					}
				}
			}
		})
	}
	assert.Equal(t, "right", rels[3].FromSide, "side by side join straight")
	assert.Empty(t, rels[3].Via)
	assert.Len(t, rels[4].Via, 2, "neighbouring rows meet in the one gap between them")
	assert.Len(t, rels[1].Via, 4, "rows apart cross through a vertical gap")
}

func TestTwoRoutesAlongOneGapTakeTheirOwnLanes(t *testing.T) {
	boxes := grid(map[string][2]int{"Top": {0, 1}, "A": {1, 0}, "B": {1, 2}})
	rels := []classRelationship{{From: "A", To: "Top", Kind: "dependency"}, {From: "B", To: "Top", Kind: "dependency"}}
	routeRelationships(boxes, rels)
	assert.NotEqual(t, rels[0].Via[0][1], rels[1].Via[0][1])
	for _, r := range rels {
		y := r.Via[0][1]
		assert.Greater(t, y, 120.0)
		assert.Less(t, y, 204.0, "still inside the gap")
	}
}

func TestAnEndIsMovedOntoThePortArchifyReported(t *testing.T) {
	rels := []classRelationship{{From: "A", To: "B", Kind: "dependency", Via: [][2]float64{{100.5, 50}, {300, 50}, {300, 400}, {420.5, 400}}}}
	diagnostic := func(endpoint, side string, from, to [2]float64) Diagnostic {
		evidence, err := json.Marshal(map[string]any{"endpoint": endpoint, "side": side, "from": from, "to": to})
		require.NoError(t, err)
		index := 0
		return Diagnostic{Code: "clean-flow/endpoint-side-direction", Severity: "error", Subject: DiagnosticSubject{Collection: "relationships", Index: &index}, Evidence: evidence}
	}
	moved := alignToPorts(rels, []Diagnostic{
		diagnostic("source", "top", [2]float64{100, 120}, [2]float64{100.5, 50}),
		diagnostic("target", "bottom", [2]float64{420.5, 400}, [2]float64{421, 520}),
		{Code: "clean-flow/edge-through-node", Severity: "error"},
	})
	assert.Equal(t, 2, moved)
	assert.Equal(t, [][2]float64{{100, 50}, {300, 50}, {300, 400}, {421, 400}}, rels[0].Via)
	assert.Zero(t, alignToPorts(rels, []Diagnostic{diagnostic("source", "top", [2]float64{100, 120}, [2]float64{100, 50})}), "nothing left to move")
}

// With a real Archify: a diagram routed along the gaps — several ends sharing one side, whose ports
// Archify spreads, so their ends are moved onto them — is accepted.
func TestARealArchifyAcceptsTheDiagramRoutedAlongTheGaps(t *testing.T) {
	script := os.Getenv("ARCHIFY_BIN")
	node, err := exec.LookPath("node")
	if script == "" || err != nil {
		t.Skip("needs ARCHIFY_BIN and node")
	}
	setHome(t, t.TempDir())
	rt := Runtime{Node: node, Path: filepath.SplitList(os.Getenv("PATH"))}
	cli, err := Find(t.TempDir())
	require.NoError(t, err)

	types := []classType{}
	place := func(id string, row int, col float64) {
		types = append(types, classType{ID: id, Label: id, Kind: "class", Row: row, Col: col,
			Attributes: []classAttribute{{Name: "id", Type: "string"}}})
	}
	place("Top", 0, 1)
	place("Left", 1, 0)
	place("Middle", 1, 1)
	place("Right", 1, 2)
	for c := range 3 {
		place(fmt.Sprintf("Bottom%d", c), 2, float64(c))
	}
	rels := []classRelationship{}
	for c := range 3 {
		rels = append(rels, classRelationship{From: fmt.Sprintf("Bottom%d", c), To: "Top", Kind: "composition"})
	}
	raw, err := json.Marshal(classDoc{SchemaVersion: 1, DiagramType: "class", Meta: classMeta{Title: "Crowded"}, Layout: classLayout{Mode: "grid"}, Types: types, Relationships: rels})
	require.NoError(t, err)

	routed, report, err := cli.RouteClassDiagram(t.Context(), rt, t.TempDir(), raw)
	require.NoError(t, err)
	assert.True(t, report.OK, report.Problems())
	var doc classDoc
	require.NoError(t, json.Unmarshal(routed, &doc))
	for _, r := range doc.Relationships {
		assert.NotEmpty(t, r.Via, "%s → %s is routed", r.From, r.To)
	}

	_, _, err = CLI{Script: filepath.Join(t.TempDir(), "bin", "archify.mjs")}.RouteClassDiagram(t.Context(), rt, t.TempDir(), raw)
	require.ErrorIs(t, err, ErrNoClassRenderer)
}
