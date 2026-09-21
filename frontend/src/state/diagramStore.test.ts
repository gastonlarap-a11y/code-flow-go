import { beforeEach, describe, expect, test, vi } from "vitest";
import { DIAGRAM_SCHEMA } from "../lib/diagram/model";

/** A promise the test releases by hand, to hold one command in flight while another runs. */
function deferred<T>(): { promise: Promise<T>; release: (value: T) => void } {
  let release: (value: T) => void = () => {};
  const promise = new Promise<T>((resolve) => {
    release = resolve;
  });
  return { promise, release };
}

vi.mock("../lib/ipc/commands", () => ({
  diagramListDocuments: vi.fn(() => Promise.resolve<string[]>([])),
  readFileText: vi.fn(() => Promise.resolve("")),
  writeFileText: vi.fn(() => Promise.resolve(undefined)),
  createFile: vi.fn(() => Promise.resolve(undefined)),
}));

const toasts: string[] = [];
vi.mock("./toastStore", () => ({
  pushErrorToast: (message: string) => toasts.push(message),
  useToastStore: { getState: () => ({ pushToast: () => {} }) },
}));

const api = vi.mocked(await import("../lib/ipc/commands"));
const { useDiagramStore } = await import("./diagramStore");
const { addNode, setNodeText } = await import("../lib/diagram/edits");

const initial = useDiagramStore.getState();

/** A saved document with one shape in it, as it is written to disk: Mermaid. */
const ONE_SHAPE = `flowchart TD
  classDef neutral fill:#eceef3,stroke:#8b8ba3,color:#1a1a24

  a@{ shape: rect, label: "Charge" }

  class a neutral

%% codeflow: v1
%% codeflow: title Checkout
%% codeflow: pos a 0 0 160 72
`;

beforeEach(() => {
  toasts.length = 0;
  vi.resetAllMocks();
  useDiagramStore.setState(initial, true);
});

/** Opens a document so the tests that need one are not each five lines of setup. */
async function open(): Promise<void> {
  api.readFileText.mockResolvedValue(ONE_SHAPE);
  await useDiagramStore.getState().openDocument("/project", "checkout.mmd");
}

describe("loading the document list", () => {
  test("holds what the backend found", async () => {
    api.diagramListDocuments.mockResolvedValue(["flows/a.mmd", "b.mmd"]);

    await useDiagramStore.getState().loadDocuments("/project");

    expect(api.diagramListDocuments).toHaveBeenCalledWith("/project");
    expect(useDiagramStore.getState().documents).toEqual(["flows/a.mmd", "b.mmd"]);
    expect(useDiagramStore.getState().documentsLoading).toBe(false);
  });

  test("a failure is reported and clears the loading flag", async () => {
    api.diagramListDocuments.mockRejectedValue(new Error("no such folder"));

    await useDiagramStore.getState().loadDocuments("/project");

    expect(toasts).toHaveLength(1);
    expect(useDiagramStore.getState().documentsLoading).toBe(false);
  });
});

describe("opening a document", () => {
  test("parses it onto the canvas", async () => {
    await open();

    const state = useDiagramStore.getState();
    expect(state.activePath).toBe("checkout.mmd");
    expect(state.doc().nodes).toHaveLength(1);
    expect(state.doc().title).toBe("Checkout");
    expect(state.dirty).toBe(false);
    expect(state.docLoading).toBe(false);
  });

  test("a file that is not a flowchart is refused by reason, and nothing is left open", async () => {
    api.readFileText.mockResolvedValue("sequenceDiagram\n  Alice->>Bob: Hi\n");

    await useDiagramStore.getState().openDocument("/project", "chat.mmd");

    expect(toasts).toEqual(["diagram.error.notFlowchart"]);
    expect(useDiagramStore.getState().activePath).toBeNull();
  });

  test("what could not be read is counted, so the view can say so before it is saved over", async () => {
    api.readFileText.mockResolvedValue('flowchart TD\n  a@{ shape: bolt, label: "Zap" }\n');

    await useDiagramStore.getState().openDocument("/project", "newer.mmd");

    expect(useDiagramStore.getState().dropped).toBe(1);
  });

  // Switching documents while a read is in flight: the newer one owns the canvas, and the older
  // response has to be dropped rather than overwriting it.
  test("a response that arrives after the user moved on is discarded", async () => {
    const slow = deferred<string>();
    api.readFileText.mockReturnValueOnce(slow.promise).mockResolvedValue(ONE_SHAPE);

    const first = useDiagramStore.getState().openDocument("/project", "first.mmd");
    await useDiagramStore.getState().openDocument("/project", "second.mmd");

    slow.release("flowchart TD\n%% codeflow: title Stale\n");
    await first;

    expect(useDiagramStore.getState().activePath).toBe("second.mmd");
    expect(useDiagramStore.getState().doc().title).toBe("Checkout");
  });

  test("opening starts a fresh history, so undo cannot reach the previous document", async () => {
    await open();
    useDiagramStore.getState().edit((doc) => setNodeText(doc, "a", "Edited"));
    expect(useDiagramStore.getState().canUndo()).toBe(true);

    await open();

    expect(useDiagramStore.getState().canUndo()).toBe(false);
  });
});

describe("editing", () => {
  test("marks the document dirty and pushes a step", async () => {
    await open();

    useDiagramStore.getState().edit((doc) => setNodeText(doc, "a", "Charge the card"));

    const state = useDiagramStore.getState();
    expect(state.dirty).toBe(true);
    expect(state.canUndo()).toBe(true);
    expect(state.doc().nodes[0]?.text).toBe("Charge the card");
  });

  test("an amended edit changes the document without becoming a step", async () => {
    await open();

    useDiagramStore.getState().edit((doc) => setNodeText(doc, "a", "A"), { amend: true });
    useDiagramStore.getState().edit((doc) => setNodeText(doc, "a", "AB"), { amend: true });

    expect(useDiagramStore.getState().doc().nodes[0]?.text).toBe("AB");
    expect(useDiagramStore.getState().canUndo()).toBe(false);
  });

  // A stray keystroke on the empty state must not build a history there is no document for.
  test("nothing is editable before a document is open", () => {
    useDiagramStore.getState().edit((doc) => addNode(doc, "rect", { x: 0, y: 0 }).doc);

    expect(useDiagramStore.getState().doc().nodes).toEqual([]);
    expect(useDiagramStore.getState().dirty).toBe(false);
  });

  test("an edit that changes nothing is not a step", async () => {
    await open();

    useDiagramStore.getState().edit((doc) => doc);

    expect(useDiagramStore.getState().dirty).toBe(false);
    expect(useDiagramStore.getState().canUndo()).toBe(false);
  });
});

describe("undo", () => {
  test("walks back and forward again", async () => {
    await open();
    useDiagramStore.getState().edit((doc) => setNodeText(doc, "a", "Edited"));

    useDiagramStore.getState().undo();
    expect(useDiagramStore.getState().doc().nodes[0]?.text).toBe("Charge");

    useDiagramStore.getState().redo();
    expect(useDiagramStore.getState().doc().nodes[0]?.text).toBe("Edited");
  });

  // Undoing back to what is on disk does not reconcile the file: the write never happened, and
  // claiming the document is clean would lose the work at the next document switch.
  test("leaves the document dirty even when it lands back where it started", async () => {
    await open();
    useDiagramStore.getState().edit((doc) => setNodeText(doc, "a", "Edited"));

    useDiagramStore.getState().undo();

    expect(useDiagramStore.getState().dirty).toBe(true);
  });
});

describe("saving", () => {
  test("writes the document and clears the dirty flag", async () => {
    await open();
    useDiagramStore.getState().edit((doc) => setNodeText(doc, "a", "Edited"));

    await useDiagramStore.getState().save("/project");

    expect(api.writeFileText).toHaveBeenCalledOnce();
    const [, relPath, contents] = api.writeFileText.mock.calls[0]!;
    expect(relPath).toBe("checkout.mmd");
    expect(contents).toContain('a@{ shape: rect, label: "Edited" }');
    expect(useDiagramStore.getState().dirty).toBe(false);
  });

  test("a clean document is not written at all", async () => {
    await open();

    await useDiagramStore.getState().save("/project");

    expect(api.writeFileText).not.toHaveBeenCalled();
  });

  // An edit made while the write is in flight must stay dirty, or it is silently lost.
  test("an edit during the write leaves the document dirty", async () => {
    await open();
    useDiagramStore.getState().edit((doc) => setNodeText(doc, "a", "First"));

    const slow = deferred<void>();
    api.writeFileText.mockReturnValue(slow.promise);
    const saving = useDiagramStore.getState().save("/project");

    useDiagramStore.getState().edit((doc) => setNodeText(doc, "a", "Second"));
    slow.release(undefined);
    await saving;

    expect(useDiagramStore.getState().dirty).toBe(true);
  });

  test("a failed write is reported and the document stays dirty", async () => {
    await open();
    useDiagramStore.getState().edit((doc) => setNodeText(doc, "a", "Edited"));
    api.writeFileText.mockRejectedValue(new Error("read-only file system"));

    await useDiagramStore.getState().save("/project");

    expect(toasts).toHaveLength(1);
    expect(useDiagramStore.getState().dirty).toBe(true);
    expect(useDiagramStore.getState().saving).toBe(false);
  });
});

describe("creating a document", () => {
  test("writes an empty diagram and opens it", async () => {
    api.diagramListDocuments.mockResolvedValue(["checkout.mmd"]);

    const failure = await useDiagramStore.getState().createDocument("/project", "checkout");

    expect(failure).toBeNull();
    expect(api.createFile).toHaveBeenCalledWith("/project", "checkout.mmd");
    expect(useDiagramStore.getState().activePath).toBe("checkout.mmd");
    expect(useDiagramStore.getState().doc().title).toBe("checkout");
    expect(useDiagramStore.getState().dirty).toBe(false);
  });

  test("the extension is appended when it is missing", async () => {
    await useDiagramStore.getState().createDocument("/project", "flows/onboarding");

    expect(api.createFile).toHaveBeenCalledWith("/project", "flows/onboarding.mmd");
  });

  test("a name that is already taken is refused, case-insensitively", async () => {
    useDiagramStore.setState({ documents: ["Checkout.mmd"] });

    expect(await useDiagramStore.getState().createDocument("/project", "checkout")).toBe("exists");
    expect(api.createFile).not.toHaveBeenCalled();
  });

  test("a name that is not a path is refused with its reason", async () => {
    expect(await useDiagramStore.getState().createDocument("/project", "../outside")).toBe("escapes");
    expect(await useDiagramStore.getState().createDocument("/project", "  ")).toBe("empty");
  });

  // What an import hands over. It creates a document; it never overwrites the open one.
  test("imported contents are written rather than an empty canvas", async () => {
    await open();
    const imported = { schema: DIAGRAM_SCHEMA, title: "Imported", nodes: [], edges: [] } as const;

    await useDiagramStore.getState().createDocument("/project", "copy", imported);

    const [, , contents] = api.writeFileText.mock.calls[0]!;
    expect(contents).toContain("%% codeflow: title Imported");
    expect(useDiagramStore.getState().activePath).toBe("copy.mmd");
  });
});

test("a project switch drops everything", async () => {
  await open();
  useDiagramStore.getState().edit((doc) => setNodeText(doc, "a", "Edited"));

  useDiagramStore.getState().reset();

  const state = useDiagramStore.getState();
  expect(state.activePath).toBeNull();
  expect(state.documents).toEqual([]);
  expect(state.doc().nodes).toEqual([]);
  expect(state.dirty).toBe(false);
  expect(state.canUndo()).toBe(false);
});
