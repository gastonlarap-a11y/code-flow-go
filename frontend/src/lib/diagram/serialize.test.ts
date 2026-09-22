import { describe, expect, test } from "vitest";
import { DIAGRAM_SCHEMA, newDocument, type DiagramDocument } from "./model";
import { MIN_SIZE, parseDocument, serializeDocument } from "./serialize";

/**
 * JSON is an export format now, and the way back in from the version before Mermaid.
 * `mermaid/emit.ts` writes what a document *is*; this reads and writes what it can be handed as.
 */
function parsed(text: string): DiagramDocument {
  const result = parseDocument(text);
  if (!result.ok) throw new Error(`expected a document, got ${result.reason}`);
  return result.value.doc;
}

function droppedBy(text: string): number {
  const result = parseDocument(text);
  if (!result.ok) throw new Error(`expected a document, got ${result.reason}`);
  return result.value.dropped;
}

describe("a file that is not a diagram is refused by reason", () => {
  test.each([
    ["not JSON at all", "{ this is not json", "notJson"],
    ["an empty file", "", "notJson"],
    ["a JSON array", "[]", "notDiagram"],
    ["someone else's JSON", '{"name":"package","version":"1.0.0"}', "notDiagram"],
    ["a format from the future", '{"schema":"codeflow.diagram/2","nodes":[]}', "unknownSchema"],
  ])("%s", (_name, text, reason) => {
    const result = parseDocument(text);

    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.reason).toBe(reason);
  });
});

test("a document round-trips through JSON unchanged", () => {
  const doc: DiagramDocument = {
    ...newDocument("Checkout"),
    nodes: [
      { id: "n1", kind: "stadium", x: 0, y: 0, width: 150, height: 60, text: "Start", fill: "accent", parent: null },
      { id: "n2", kind: "diam", x: 200, y: 0, width: 150, height: 110, text: "Paid?", fill: "warning", parent: null },
    ],
    edges: [{ id: "e1", from: "n1", to: "n2", kind: "arrow", label: "yes" }],
  };

  expect(parsed(serializeDocument(doc))).toEqual(doc);
});

test("what is written is indented, newline-terminated and ordered", () => {
  const text = serializeDocument(newDocument("Flow"));

  expect(text.endsWith("\n")).toBe(true);
  expect(text).toContain(`"schema": "${DIAGRAM_SCHEMA}"`);
  expect(text.indexOf('"schema"')).toBeLessThan(text.indexOf('"title"'));
});

/**
 * The migration path.
 *
 * Every shape and connector of the pre-Mermaid catalogue has somewhere to land, and the ones that
 * land on a shared figure are counted — a diagram that arrives saying "exclusive gateway" and
 * opens as a plain diamond should say so before it is written back that way.
 */
describe("reading a document from before Mermaid", () => {
  function legacy(kind: string, edgeKind = "flow"): string {
    return JSON.stringify({
      schema: DIAGRAM_SCHEMA,
      title: "Old",
      nodes: [
        { id: "a", kind, x: 0, y: 0, width: 160, height: 72, text: "One", fill: "neutral", parent: null },
        { id: "b", kind: "process", x: 300, y: 0, width: 160, height: 72, text: "Two", fill: "neutral", parent: null },
      ],
      edges: [
        { id: "e1", from: "a", fromPort: "right", to: "b", toPort: "left", kind: edgeKind, label: "" },
      ],
    });
  }

  test.each([
    ["process", "rect"],
    ["terminator", "stadium"],
    ["decision", "diam"],
    ["bpmn.exclusive", "diam"],
    ["bpmn.parallel", "diam"],
    ["bpmn.task", "rounded"],
    ["uml.actor", "person"],
    ["uml.usecase", "stadium"],
    ["bpmn.pool", "subgraph"],
    ["cylinder", "cyl"],
    ["note", "text"],
    ["subprocess", "fr-rect"],
    ["connector", "sm-circ"],
  ])("%s becomes %s", (before, after) => {
    expect(parsed(legacy(before)).nodes[0]?.kind).toBe(after);
  });

  test.each([
    ["flow", "arrow"],
    ["association", "line"],
    ["message", "dotted"],
    ["include", "dotted"],
    ["generalization", "arrow"],
  ])("a %s connector becomes %s", (before, after) => {
    expect(parsed(legacy("process", before)).edges[0]?.kind).toBe(after);
  });

  test("the ports it used to store are read and thrown away", () => {
    expect(parsed(legacy("process")).edges[0]).not.toHaveProperty("fromPort");
  });

  // `process` is just a renamed `rect`; `bpmn.exclusive` is a figure that no longer exists.
  test("only a shape that lost something is counted", () => {
    expect(droppedBy(legacy("process"))).toBe(0);
    expect(droppedBy(legacy("bpmn.exclusive"))).toBe(1);
  });

  test("a shape from neither catalogue is dropped rather than guessed at", () => {
    const text = JSON.stringify({
      schema: DIAGRAM_SCHEMA,
      nodes: [
        { id: "a", kind: "rect", x: 0, y: 0, width: 160, height: 72, text: "Kept", fill: "neutral", parent: null },
        { id: "b", kind: "from.the.future", x: 0, y: 0, width: 48, height: 48, text: "Lost", fill: "none", parent: null },
      ],
      edges: [],
    });

    expect(parsed(text).nodes.map((n) => n.id)).toEqual(["a"]);
    expect(droppedBy(text)).toBe(1);
  });
});

describe("a damaged document opens with what survives", () => {
  test("an edge whose endpoint is gone goes with it", () => {
    const text = JSON.stringify({
      schema: DIAGRAM_SCHEMA,
      nodes: [{ id: "a", kind: "rect", x: 0, y: 0, width: 160, height: 72, text: "", fill: "neutral", parent: null }],
      edges: [{ id: "e1", from: "a", to: "ghost", kind: "arrow", label: "" }],
    });

    expect(parsed(text).edges).toEqual([]);
    expect(droppedBy(text)).toBe(1);
  });

  test("a duplicated id keeps the first and counts the rest", () => {
    const node = { kind: "rect", x: 0, y: 0, width: 160, height: 72, fill: "neutral", parent: null };
    const text = JSON.stringify({
      schema: DIAGRAM_SCHEMA,
      nodes: [
        { ...node, id: "a", text: "first" },
        { ...node, id: "a", text: "second" },
      ],
      edges: [],
    });

    expect(parsed(text).nodes.map((n) => n.text)).toEqual(["first"]);
    expect(droppedBy(text)).toBe(1);
  });

  test("a parent that is missing or holds nothing releases its child rather than hiding it", () => {
    const text = JSON.stringify({
      schema: DIAGRAM_SCHEMA,
      nodes: [
        { id: "a", kind: "rounded", x: 10, y: 10, width: 160, height: 76, text: "", fill: "accent", parent: "gone" },
        { id: "b", kind: "rect", x: 0, y: 0, width: 160, height: 72, text: "", fill: "neutral", parent: null },
        { id: "c", kind: "rect", x: 0, y: 0, width: 160, height: 72, text: "", fill: "neutral", parent: "b" },
      ],
      edges: [],
    });

    expect(parsed(text).nodes.every((node) => node.parent === null)).toBe(true);
    expect(parsed(text).nodes).toHaveLength(3);
  });

  test("nonsense field values land on the stencil's own defaults", () => {
    const text = JSON.stringify({
      schema: DIAGRAM_SCHEMA,
      nodes: [{ id: "a", kind: "diam", x: null, y: "40", width: Number.NaN, text: 42, fill: "chartreuse", parent: 7 }],
      edges: [],
    });

    expect(parsed(text).nodes[0]).toEqual({
      id: "a",
      kind: "diam",
      x: 0,
      y: 0,
      width: 150,
      height: 110,
      text: "",
      fill: "warning",
      parent: null,
    });
  });

  test("a shape dragged to nothing still has a grabbable size", () => {
    const text = JSON.stringify({
      schema: DIAGRAM_SCHEMA,
      nodes: [{ id: "a", kind: "rect", x: 0, y: 0, width: 0, height: -40, text: "", fill: "neutral", parent: null }],
      edges: [],
    });

    expect(parsed(text).nodes[0]).toMatchObject({ width: MIN_SIZE, height: MIN_SIZE });
  });

  test("missing lists are empty ones, not a refusal", () => {
    expect(parsed(JSON.stringify({ schema: DIAGRAM_SCHEMA }))).toEqual({
      schema: DIAGRAM_SCHEMA,
      title: "",
      nodes: [],
      edges: [],
    });
  });
});
