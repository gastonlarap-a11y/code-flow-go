import { describe, expect, test } from "vitest";
import {
  CASCADE_STEP,
  DUPLICATE_OFFSET,
  addNode,
  connect,
  constrainSize,
  containerAt,
  duplicate,
  moveNodes,
  removeSelection,
  reparent,
  resizeNode,
  setFill,
} from "./edits";
import { absolutePosition, newDocument, nodeById, type DiagramDocument } from "./model";

/** A container holding one task, plus a loose shape — what most of these rules are about. */
function nested(): DiagramDocument {
  return {
    ...newDocument("Nested"),
    nodes: [
      { id: "pool", kind: "subgraph", x: 100, y: 50, width: 620, height: 300, text: "Sales", fill: "none", parent: null },
      { id: "task", kind: "rounded", x: 40, y: 60, width: 160, height: 76, text: "Quote", fill: "accent", parent: "pool" },
      { id: "loose", kind: "rect", x: 900, y: 50, width: 160, height: 72, text: "Ship", fill: "neutral", parent: null },
    ],
    edges: [{ id: "e1", from: "task", to: "loose", kind: "arrow", label: "" }],
  };
}

describe("adding a shape", () => {
  test("lands centred on the point it was dropped at", () => {
    const { doc, id } = addNode(newDocument("x"), "rect", { x: 300, y: 200 });
    const node = nodeById(doc, id)!;

    expect(node.x + node.width / 2).toBe(300);
    expect(node.y + node.height / 2).toBe(200);
  });

  // The palette places into the middle of the view, so without this three clicks in a row put
  // three shapes exactly on top of each other and the second and third look like they did nothing.
  test("cascades rather than landing on top of what is already there", () => {
    const first = addNode(newDocument("x"), "rect", { x: 300, y: 200 });
    const second = addNode(first.doc, "rect", { x: 300, y: 200 });
    const third = addNode(second.doc, "diam", { x: 300, y: 200 });

    const centres = third.doc.nodes.map((node) => ({
      x: node.x + node.width / 2,
      y: node.y + node.height / 2,
    }));

    expect(centres[0]).toEqual({ x: 300, y: 200 });
    expect(centres[1]).toEqual({ x: 300 + CASCADE_STEP, y: 200 + CASCADE_STEP });
    expect(centres[2]).toEqual({ x: 300 + CASCADE_STEP * 2, y: 200 + CASCADE_STEP * 2 });
  });

  test("a spot that is free is used as it is", () => {
    const first = addNode(newDocument("x"), "rect", { x: 300, y: 200 });
    const away = addNode(first.doc, "rect", { x: 900, y: 700 });
    const placed = away.doc.nodes[1]!;

    expect({ x: placed.x + placed.width / 2, y: placed.y + placed.height / 2 }).toEqual({ x: 900, y: 700 });
  });

  // Inside a container the position is relative to it, and the cascade would be comparing two
  // different frames — so a child lands where it was dropped.
  test("a shape placed inside a container is not cascaded", () => {
    const first = addNode(nested(), "rounded", { x: 300, y: 150 }, "pool");
    const second = addNode(first.doc, "rounded", { x: 300, y: 150 }, "pool");

    const a = nodeById(second.doc, first.id)!;
    const b = nodeById(second.doc, second.id)!;

    // Both at the same place: a child's coordinates are relative to its container, so cascading
    // them against the absolute positions of everything else would compare two different frames.
    expect({ x: b.x, y: b.y }).toEqual({ x: a.x, y: a.y });
  });

  test("takes the stencil's own size and fill", () => {
    const { doc, id } = addNode(newDocument("x"), "diam", { x: 0, y: 0 });

    expect(nodeById(doc, id)).toMatchObject({ width: 150, height: 110, fill: "warning", text: "" });
  });

  // Paint order is document order, so a pool appended last would cover everything already drawn.
  test("a container goes behind what is already on the canvas", () => {
    const withProcess = addNode(newDocument("x"), "rect", { x: 0, y: 0 }).doc;
    const { doc, id } = addNode(withProcess, "subgraph", { x: 0, y: 0 });

    expect(doc.nodes[0]?.id).toBe(id);
  });
});

describe("resizing", () => {
  test("keeps a circle round", () => {
    expect(constrainSize("circle", 120, 48)).toEqual({ width: 120, height: 120 });
    expect(constrainSize("sm-circ", 40, 90)).toEqual({ width: 90, height: 90 });
  });

  test("lets a rectangle be any shape", () => {
    expect(constrainSize("rect", 300, 40)).toEqual({ width: 300, height: 40 });
  });

  test("never lets anything shrink out of reach", () => {
    const doc = resizeNode(nested(), "loose", { x: 0, y: 0, width: 0, height: -10 });

    expect(nodeById(doc, "loose")).toMatchObject({ width: 16, height: 16 });
  });
});

describe("deleting", () => {
  test("a shape takes its connectors with it", () => {
    const doc = removeSelection(nested(), ["loose"], []);

    expect(doc.edges).toEqual([]);
    expect(doc.nodes.map((n) => n.id)).toEqual(["pool", "task"]);
  });

  test("a container takes its contents with it", () => {
    const doc = removeSelection(nested(), ["pool"], []);

    expect(doc.nodes.map((n) => n.id)).toEqual(["loose"]);
    expect(doc.edges).toEqual([]);
  });

  test("a connector can go on its own, leaving both shapes", () => {
    const doc = removeSelection(nested(), [], ["e1"]);

    expect(doc.nodes).toHaveLength(3);
    expect(doc.edges).toEqual([]);
  });
});

describe("connecting", () => {
  test("joins two shapes", () => {
    const doc = connect(nested(), "loose", "pool");

    expect(doc.edges).toHaveLength(2);
    expect(doc.edges[1]).toMatchObject({ from: "loose", to: "pool", kind: "arrow", label: "" });
  });

  // There are no sides to tell two connectors apart by — `routing.chooseSides` picks them from
  // where the shapes are — so the pair is the whole identity.
  test("the same pair twice is one connector", () => {
    const once = connect(nested(), "loose", "pool");

    expect(connect(once, "loose", "pool")).toBe(once);
  });

  test("the other direction is a different connector", () => {
    const doc = connect(connect(nested(), "loose", "pool"), "pool", "loose");

    expect(doc.edges).toHaveLength(3);
  });

  test("a shape may be joined to itself", () => {
    expect(connect(nested(), "loose", "loose").edges).toHaveLength(2);
  });

  test("a shape that is not there cannot be connected", () => {
    const doc = nested();

    expect(connect(doc, "loose", "ghost")).toBe(doc);
  });
});

describe("dropping a shape into a container", () => {
  test("keeps it exactly where it was on screen", () => {
    const before = nested();
    const wasAt = absolutePosition(before, nodeById(before, "loose")!);

    const after = reparent(before, "loose", "pool");
    const isAt = absolutePosition(after, nodeById(after, "loose")!);

    expect(isAt).toEqual(wasAt);
    expect(nodeById(after, "loose")?.parent).toBe("pool");
    // Stored relative to the pool, which is what makes dragging the pool carry it along.
    expect(nodeById(after, "loose")?.x).toBe(800);
  });

  test("taking it back out keeps it where it was too", () => {
    const before = nested();
    const wasAt = absolutePosition(before, nodeById(before, "task")!);

    const after = reparent(before, "task", null);

    expect(absolutePosition(after, nodeById(after, "task")!)).toEqual(wasAt);
    expect(nodeById(after, "task")?.parent).toBeNull();
  });

  test("a shape that holds nothing cannot be a container", () => {
    const doc = nested();

    expect(reparent(doc, "task", "loose")).toBe(doc);
  });

  test("nothing can be put inside itself or inside what it holds", () => {
    const doc = reparent(nested(), "task", "pool");

    expect(reparent(doc, "pool", "pool")).toBe(doc);
    expect(reparent(doc, "pool", "task")).toBe(doc);
  });
});

describe("duplicating", () => {
  test("copies a container with its contents and the connectors inside it", () => {
    const start = connect(nested(), "pool", "task");
    const { doc, ids } = duplicate(start, ["pool"]);

    expect(ids).toHaveLength(2);
    expect(doc.nodes).toHaveLength(5);
    // The pool↔task connector is copied; task→loose is not, because `loose` was not selected.
    expect(doc.edges).toHaveLength(3);
  });

  test("offsets the copy but not what is inside it", () => {
    const { doc, ids } = duplicate(nested(), ["pool"]);
    const copiedPool = nodeById(doc, ids[0]!)!;
    const copiedTask = doc.nodes.find((node) => node.parent === copiedPool.id)!;

    expect(copiedPool.x).toBe(100 + DUPLICATE_OFFSET);
    expect(copiedTask.x).toBe(40);
  });

  test("gives every copy a new id and leaves the original alone", () => {
    const before = nested();
    const { doc, ids } = duplicate(before, ["loose"]);

    expect(ids).not.toContain("loose");
    expect(nodeById(doc, "loose")).toEqual(nodeById(before, "loose"));
  });

  test("duplicating nothing changes nothing", () => {
    const doc = nested();

    expect(duplicate(doc, []).doc).toBe(doc);
  });
});

describe("finding what a shape was dropped on", () => {
  /** A pool at (0,0) 720×300 holding a lane at (0,140) 660×140. */
  function pools(): DiagramDocument {
    return {
      ...newDocument("Pools"),
      nodes: [
        { id: "pool", kind: "subgraph", x: 0, y: 0, width: 720, height: 300, text: "", fill: "none", parent: null },
        { id: "lane", kind: "subgraph", x: 0, y: 140, width: 660, height: 140, text: "", fill: "none", parent: "pool" },
        { id: "task", kind: "rounded", x: 900, y: 0, width: 160, height: 76, text: "", fill: "accent", parent: null },
      ],
      edges: [],
    };
  }

  test("nothing, out in the open", () => {
    expect(containerAt(pools(), { x: 1000, y: 1000 })).toBeNull();
  });

  test("the pool, over the part of it no lane covers", () => {
    expect(containerAt(pools(), { x: 300, y: 60 })).toBe("pool");
  });

  // The innermost wins: a task dropped on a lane belongs to the lane, not to the pool behind it.
  test("the lane, over the lane", () => {
    expect(containerAt(pools(), { x: 300, y: 200 })).toBe("lane");
  });

  test("never itself, and never what it holds", () => {
    expect(containerAt(pools(), { x: 300, y: 200 }, ["pool"])).toBeNull();
    expect(containerAt(pools(), { x: 300, y: 200 }, ["lane"])).toBe("pool");
  });

  test("a shape that holds nothing is not a drop target", () => {
    expect(containerAt(pools(), { x: 950, y: 30 })).toBeNull();
  });
});

test("moving several shapes at once touches only those", () => {
  const doc = moveNodes(nested(), [
    { id: "loose", x: 10, y: 20 },
    { id: "task", x: 1, y: 2 },
  ]);

  expect(nodeById(doc, "loose")).toMatchObject({ x: 10, y: 20 });
  expect(nodeById(doc, "task")).toMatchObject({ x: 1, y: 2 });
  expect(nodeById(doc, "pool")).toMatchObject({ x: 100, y: 50 });
});

test("a fill applies to every selected shape and nothing else", () => {
  const doc = setFill(nested(), ["task", "loose"], "danger");

  expect(doc.nodes.map((node) => node.fill)).toEqual(["none", "danger", "danger"]);
});
