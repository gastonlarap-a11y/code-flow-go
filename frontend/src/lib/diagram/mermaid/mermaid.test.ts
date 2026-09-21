import { describe, expect, test } from "vitest";
import { newDocument, type DiagramDocument, type DiagramNode } from "../model";
import { emitMermaid } from "./emit";
import { parseMermaid } from "./parse";

/**
 * The invariant this whole format rests on: **what is written can be read back, unchanged**.
 *
 * The editor regenerates the file from the model on every save, and parses the file into the model
 * on every keystroke in the text pane. If those two disagree anywhere, a diagram quietly changes
 * shape as somebody works on it. Every test that produces text here reads it back.
 */
function roundTrip(doc: DiagramDocument): DiagramDocument {
  const text = emitMermaid(doc);
  const parsed = parseMermaid(text);
  if (!parsed.ok) throw new Error(`emitted text did not parse (${parsed.reason}):\n${text}`);
  expect(parsed.value.dropped, `emitted text lost something:\n${text}`).toBe(0);
  return parsed.value.doc;
}

function flow(): DiagramDocument {
  return {
    ...newDocument("Checkout"),
    nodes: [
      { id: "n1", kind: "stadium", x: 40, y: 40, width: 150, height: 60, text: "Inicio", fill: "accent", parent: null },
      { id: "n2", kind: "rect", x: 40, y: 160, width: 160, height: 72, text: "Validar RUT", fill: "neutral", parent: null },
      { id: "n3", kind: "diam", x: 40, y: 300, width: 150, height: 110, text: "¿Aprobado?", fill: "warning", parent: null },
    ],
    edges: [
      { id: "e1", from: "n1", to: "n2", kind: "arrow", label: "" },
      { id: "e2", from: "n2", to: "n3", kind: "arrow", label: "sí" },
    ],
  };
}

test("a document survives being written and read back", () => {
  const doc = flow();

  expect(roundTrip(doc)).toEqual(doc);
});

test("what is written is a flowchart anybody can render", () => {
  const text = emitMermaid(flow());

  expect(text.split("\n")[0]).toBe("flowchart TD");
  expect(text).toContain('n2@{ shape: rect, label: "Validar RUT" }');
  expect(text).toContain("n1 --> n2");
  expect(text).toContain('n2 -->|"sí"| n3');
});

test("the same document always produces the same bytes", () => {
  expect(emitMermaid(flow())).toBe(emitMermaid(flow()));
});

describe("what Mermaid cannot carry", () => {
  test("positions ride in comments, after the diagram", () => {
    const text = emitMermaid(flow());
    const body = text.slice(0, text.indexOf("%%"));

    expect(text).toContain("%% codeflow: v1");
    expect(text).toContain("%% codeflow: pos n2 40 160 160 72");
    // The Mermaid itself reads as one block, with none of our metadata in the middle of it.
    expect(body).not.toContain("%%");
  });

  // The file a model writes has no `pos` comments in it at all, and that is the case this format
  // exists to serve — so it has to arrive as a drawing, not as a heap of boxes on the origin.
  test("a file with no positions is laid out rather than stacked", () => {
    const parsed = parseMermaid("flowchart TD\n  a[One] --> b[Two]\n");
    if (!parsed.ok) throw new Error(parsed.reason);

    const [a, b] = parsed.value.doc.nodes as [DiagramNode, DiagramNode];
    expect(parsed.value.doc.nodes.map((n) => n.id)).toEqual(["a", "b"]);
    expect(a).toMatchObject({ kind: "rect", text: "One" });
    expect(b.y).toBeGreaterThan(a.y + a.height);
  });
});

/**
 * A whole flowchart of the kind somebody actually pastes in: written by hand or by a model, four
 * spaces of indent, labelled branches, and a declaration on either side of an arrow. Every node in
 * it has to survive, or the format does not do the one job it was changed for.
 */
test("a hand-written flowchart arrives whole", () => {
  const pasted = `flowchart TD
    A([Solicitud recibida]) --> B[Validar RUT]
    B --> C{¿RUT válido?}
    C -->|No| D[Rechazar solicitud]
    C -->|Sí| E[Consultar historial crediticio]
    E --> F{¿Tiene deuda?}
    F -->|Sí| G[Derivar a comité]
    F -->|No| H[Aprobar automáticamente]
    G --> I([Fin])
    H --> I
    D --> I
`;

  const parsed = parseMermaid(pasted);
  if (!parsed.ok) throw new Error(parsed.reason);
  const { doc, dropped } = parsed.value;

  expect(doc.nodes.map((node) => node.id)).toEqual([..."ABCDEFGHI"]);
  expect(doc.edges).toHaveLength(10);
  expect(dropped).toBe(0);

  const byId = new Map(doc.nodes.map((node) => [node.id, node]));
  expect(byId.get("A")).toMatchObject({ kind: "stadium", text: "Solicitud recibida" });
  expect(byId.get("C")).toMatchObject({ kind: "diam", text: "¿RUT válido?" });
  expect(byId.get("H")).toMatchObject({ kind: "rect", text: "Aprobar automáticamente" });
  expect(doc.edges.find((edge) => edge.from === "C" && edge.to === "D")?.label).toBe("No");

  // And it arrives as a drawing: nine shapes, none of them on top of another.
  for (const one of doc.nodes) {
    for (const two of doc.nodes) {
      if (one.id === two.id) continue;
      const overlaps =
        one.x < two.x + two.width &&
        two.x < one.x + one.width &&
        one.y < two.y + two.height &&
        two.y < one.y + one.height;
      expect(overlaps, `${one.id} overlaps ${two.id}`).toBe(false);
    }
  }
});

describe("fills", () => {
  test("only the ones the document uses are declared", () => {
    const text = emitMermaid(flow());

    expect(text).toContain("classDef accent");
    expect(text).toContain("classDef warning");
    expect(text).not.toContain("classDef danger");
  });

  test("shapes of one colour are classed on one line", () => {
    const doc: DiagramDocument = {
      ...newDocument("Same"),
      nodes: ["n1", "n2", "n3"].map((id, i) => ({
        id,
        kind: "rect" as const,
        x: i * 200,
        y: 0,
        width: 160,
        height: 72,
        text: id,
        fill: "accent" as const,
        parent: null,
      })),
      edges: [],
    };

    expect(emitMermaid(doc)).toContain("class n1,n2,n3 accent");
  });

  test("a colour survives the trip", () => {
    expect(roundTrip(flow()).nodes.map((n) => n.fill)).toEqual(["accent", "neutral", "warning"]);
  });
});

describe("containers", () => {
  const nested: DiagramDocument = {
    ...newDocument("Lanes"),
    nodes: [
      { id: "n1", kind: "subgraph", x: 100, y: 200, width: 620, height: 300, text: "Ventas", fill: "none", parent: null },
      { id: "n2", kind: "rounded", x: 40, y: 60, width: 160, height: 72, text: "Cotizar", fill: "accent", parent: "n1" },
    ],
    edges: [],
  };

  test("are written as a subgraph with the shapes inside it", () => {
    const text = emitMermaid(nested);

    expect(text).toContain('subgraph n1 ["Ventas"]');
    expect(text).toContain("  end");
    expect(text.indexOf("n2@{")).toBeGreaterThan(text.indexOf("subgraph n1"));
  });

  test("a child keeps its place relative to its container", () => {
    const back = roundTrip(nested);

    expect(back.nodes[1]).toMatchObject({ parent: "n1", x: 40, y: 60 });
  });

  // Positions are written absolute so the file survives somebody deleting a `subgraph` line by
  // hand: the shapes stay where they were rather than all jumping to the top-left corner.
  test("positions are absolute on disk", () => {
    expect(emitMermaid(nested)).toContain("%% codeflow: pos n2 140 260 160 72");
  });
});

describe("labels", () => {
  test.each([
    ["a quote", 'Say "yes"'],
    ["a hash", "Issue #42"],
    ["a line break", "First\nSecond"],
    ["brackets", "a[b](c){d}"],
    ["a pipe", "yes|no"],
  ])("%s survives", (_name, text) => {
    const doc: DiagramDocument = {
      ...newDocument("Labels"),
      nodes: [{ id: "n1", kind: "rect", x: 0, y: 0, width: 160, height: 72, text, fill: "neutral", parent: null }],
      edges: [],
    };

    expect(roundTrip(doc).nodes[0]?.text).toBe(text);
  });
});

describe("reading what somebody else wrote", () => {
  test("the classic bracket shapes", () => {
    const parsed = parseMermaid(`
flowchart LR
  a[Process]
  b(Rounded)
  c([Pill])
  d{Decision}
  e[(Store)]
  f((Start))
  g{{Prepare}}
  h[/Input/]
  i[[Subprocess]]
`);
    if (!parsed.ok) throw new Error(parsed.reason);

    expect(parsed.value.doc.nodes.map((n) => n.kind)).toEqual([
      "rect",
      "rounded",
      "stadium",
      "diam",
      "cyl",
      "circle",
      "hex",
      "lean-r",
      "div-rect",
    ]);
  });

  test("every arrow", () => {
    const parsed = parseMermaid(`
flowchart TD
  a --> b
  b --- c
  c -.-> d
  d ==> e
`);
    if (!parsed.ok) throw new Error(parsed.reason);

    expect(parsed.value.doc.edges.map((e) => e.kind)).toEqual(["arrow", "line", "dotted", "thick"]);
  });

  test("an edge introduces the nodes it names", () => {
    const parsed = parseMermaid("flowchart TD\n  start --> finish\n");
    if (!parsed.ok) throw new Error(parsed.reason);

    expect(parsed.value.doc.nodes.map((n) => n.id)).toEqual(["start", "finish"]);
  });

  test("a shape outside this catalogue is drawn as a rectangle, and counted", () => {
    const parsed = parseMermaid('flowchart TD\n  a@{ shape: bolt, label: "Zap" }\n');
    if (!parsed.ok) throw new Error(parsed.reason);

    expect(parsed.value.doc.nodes[0]).toMatchObject({ kind: "rect", text: "Zap" });
    expect(parsed.value.dropped).toBe(1);
  });

  test("a subgraph written by hand nests what is in it", () => {
    const parsed = parseMermaid(`
flowchart TD
  subgraph pool [Sales]
    task[Quote]
  end
  outside[Ship]
`);
    if (!parsed.ok) throw new Error(parsed.reason);

    const byId = new Map(parsed.value.doc.nodes.map((n) => [n.id, n]));
    expect(byId.get("task")?.parent).toBe("pool");
    expect(byId.get("outside")?.parent).toBeNull();
    expect(byId.get("pool")?.text).toBe("Sales");
  });

  test("`graph` is read like `flowchart`, because half the world still writes it", () => {
    const parsed = parseMermaid("graph LR\n  a --> b\n");

    expect(parsed.ok).toBe(true);
  });
});

describe("a file that is not a flowchart", () => {
  test.each([
    ["a sequence diagram", "sequenceDiagram\n  Alice->>Bob: Hi\n", "notFlowchart"],
    ["a gantt", "gantt\n  title X\n", "notFlowchart"],
    ["prose", "This is just a note I wrote.\n", "notFlowchart"],
    ["nothing", "\n\n", "notMermaid"],
  ])("%s is refused by reason", (_name, text, reason) => {
    const parsed = parseMermaid(text);

    expect(parsed.ok).toBe(false);
    if (!parsed.ok) expect(parsed.reason).toBe(reason);
  });
});

test("an edge to a node that was never declared is dropped and counted", () => {
  const text = "flowchart TD\n  a[One]\n%% codeflow: pos a 0 0 160 72\n";
  const parsed = parseMermaid(text);
  if (!parsed.ok) throw new Error(parsed.reason);

  expect(parsed.value.doc.edges).toEqual([]);
  expect(parsed.value.dropped).toBe(0);
});

test("an empty document is still a flowchart", () => {
  const text = emitMermaid(newDocument("Empty"));

  expect(text.startsWith("flowchart TD")).toBe(true);
  expect(parseMermaid(text).ok).toBe(true);
});
