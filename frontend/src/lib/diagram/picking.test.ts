import { describe, expect, test } from "vitest";
import { newDocument, type DiagramDocument, type DiagramNode } from "./model";
import {
  EDGE_TOLERANCE,
  attachPoint,
  boxOf,
  distanceToSegment,
  edgeAt,
  edgePath,
  edgesIn,
  nodeAt,
  nodesIn,
  normalizeRect,
} from "./picking";
import { stencilById, type StencilId } from "./stencils";

function node(
  id: string,
  x: number,
  y: number,
  kind: StencilId = "rect",
  parent: string | null = null,
): DiagramNode {
  const stencil = stencilById(kind);
  return { id, kind, x, y, width: 100, height: 60, text: id, fill: stencil.defaultFill, parent };
}

/** Two boxes far enough apart to be routed between, and one arrow joining them. */
function pair(): DiagramDocument {
  return {
    ...newDocument("Pick"),
    nodes: [node("a", 0, 0), node("b", 400, 0)],
    edges: [{ id: "e1", from: "a", to: "b", kind: "arrow", label: "" }],
  };
}

describe("where a node is", () => {
  test("a child's box is its place on the page, not its offset", () => {
    const doc: DiagramDocument = {
      ...newDocument("Nested"),
      nodes: [node("pool", 100, 200, "subgraph"), node("task", 40, 60, "rect", "pool")],
      edges: [],
    };

    expect(boxOf(doc, doc.nodes[1]!)).toEqual({ x: 140, y: 260, width: 100, height: 60 });
  });
});

describe("the shape under a point", () => {
  test("answers the one the point is in, and null off the page", () => {
    const doc = pair();

    expect(nodeAt(doc, { x: 50, y: 30 })?.id).toBe("a");
    expect(nodeAt(doc, { x: 450, y: 30 })?.id).toBe("b");
    expect(nodeAt(doc, { x: 250, y: 500 })).toBeNull();
  });

  test("the last one drawn wins where two overlap", () => {
    const doc: DiagramDocument = {
      ...newDocument("Stack"),
      nodes: [node("under", 0, 0), node("over", 20, 20)],
      edges: [],
    };

    expect(nodeAt(doc, { x: 50, y: 50 })?.id).toBe("over");
  });

  // Dropping a task on a pool has to select the task, whatever the document order says.
  test("a container never wins over what is on top of it", () => {
    const doc: DiagramDocument = {
      ...newDocument("Pool"),
      nodes: [node("task", 40, 40), { ...node("pool", 0, 0, "subgraph"), width: 600, height: 300 }],
      edges: [],
    };

    expect(nodeAt(doc, { x: 60, y: 60 })?.id).toBe("task");
    expect(nodeAt(doc, { x: 500, y: 250 })?.id).toBe("pool");
  });
});

describe("the connector under a point", () => {
  test("found on its line and not beside it", () => {
    const doc = pair();
    const { at } = edgePath(boxOf(doc, doc.nodes[0]!), boxOf(doc, doc.nodes[1]!));

    expect(edgeAt(doc, at)).toBe("e1");
    expect(edgeAt(doc, { x: at.x, y: at.y + EDGE_TOLERANCE * 4 })).toBeNull();
  });

  test("a connector whose ends are gone is not picked instead of crashing", () => {
    const doc: DiagramDocument = {
      ...newDocument("Orphan"),
      nodes: [],
      edges: [{ id: "e1", from: "a", to: "b", kind: "arrow", label: "" }],
    };

    expect(edgeAt(doc, { x: 0, y: 0 })).toBeNull();
    expect(edgesIn(doc, { x: -999, y: -999, width: 9999, height: 9999 })).toEqual([]);
  });
});

describe("distance to a segment", () => {
  test("is measured to the nearest point on it, not to the infinite line", () => {
    const a = { x: 0, y: 0 };
    const b = { x: 100, y: 0 };

    expect(distanceToSegment({ x: 50, y: 10 }, a, b)).toBe(10);
    // Past the end: the distance is to the endpoint, so 200 away is 100, not 0.
    expect(distanceToSegment({ x: 200, y: 0 }, a, b)).toBe(100);
  });

  test("a segment of no length is just the point", () => {
    expect(distanceToSegment({ x: 3, y: 4 }, { x: 0, y: 0 }, { x: 0, y: 0 })).toBe(5);
  });
});

describe("the marquee", () => {
  test("a rectangle is the same whichever way it was dragged", () => {
    const forward = normalizeRect({ x: 10, y: 20 }, { x: 110, y: 220 });

    expect(forward).toEqual({ x: 10, y: 20, width: 100, height: 200 });
    expect(normalizeRect({ x: 110, y: 220 }, { x: 10, y: 20 })).toEqual(forward);
  });

  // Requiring containment means a marquee over a pool selects nothing unless it swallows the pool.
  test("touching is enough", () => {
    const doc = pair();

    expect(nodesIn(doc, { x: 90, y: 30, width: 20, height: 20 })).toEqual(["a"]);
  });

  test("nothing outside it is caught", () => {
    expect(nodesIn(pair(), { x: 200, y: 0, width: 100, height: 60 })).toEqual([]);
  });

  test("a box dragged in the empty middle of a container selects what is in it, not the container", () => {
    const doc: DiagramDocument = {
      ...newDocument("Pool"),
      nodes: [
        { ...node("pool", 0, 0, "subgraph"), width: 600, height: 300 },
        node("task", 40, 40, "rect", "pool"),
      ],
      edges: [],
    };

    expect(nodesIn(doc, { x: 20, y: 20, width: 200, height: 120 })).toEqual(["task"]);
    // Dragged from outside, the pool itself is the thing being caught.
    expect(nodesIn(doc, { x: -50, y: -50, width: 300, height: 300 })).toContain("pool");
  });

  test("a connector is caught by its midpoint, which is where its label is", () => {
    const doc = pair();
    const { at } = edgePath(boxOf(doc, doc.nodes[0]!), boxOf(doc, doc.nodes[1]!));

    expect(edgesIn(doc, { x: at.x - 5, y: at.y - 5, width: 10, height: 10 })).toEqual(["e1"]);
    expect(edgesIn(doc, { x: at.x + 200, y: at.y, width: 10, height: 10 })).toEqual([]);
  });
});

describe("the drawn path", () => {
  test("starts on one box and ends on the other", () => {
    const doc = pair();
    const { d, at } = edgePath(boxOf(doc, doc.nodes[0]!), boxOf(doc, doc.nodes[1]!));

    expect(d.startsWith("M")).toBe(true);
    expect(at.x).toBeGreaterThan(100);
    expect(at.x).toBeLessThan(400);
  });

  test("a port sits on the side it names", () => {
    const box = { x: 0, y: 0, width: 100, height: 60 };

    expect(attachPoint(box, "right")).toEqual({ x: 100, y: 30 });
    expect(attachPoint(box, "top")).toEqual({ x: 50, y: 0 });
  });
});
