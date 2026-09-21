import { describe, expect, it } from "vitest";
import { documentName, documentTitle, normalizeDocumentPath } from "./documentPath";

const DBML = ".dbml";
const DIAGRAM = ".diagram.json";

/** The happy path unwrapped, so a case that unexpectedly fails reports its reason. */
function pathOf(input: string, extension = DBML): string {
  const result = normalizeDocumentPath(input, extension);
  if (!result.ok) throw new Error(`expected a path, got reason "${result.reason}"`);
  return result.relPath;
}

describe("normalizeDocumentPath", () => {
  it("appends the extension when it is missing", () => {
    // The sidecar's walk matches on `.dbml`, so a document saved without it would vanish from the
    // picker the next time it reloaded.
    expect(pathOf("orders")).toBe("orders.dbml");
  });

  it("leaves an extension already there, whatever case it was typed in", () => {
    expect(pathOf("orders.dbml")).toBe("orders.dbml");
    expect(pathOf("orders.DBML")).toBe("orders.DBML");
  });

  it("normalises backslashes, so one document is one layout key", () => {
    expect(pathOf("db\\schema\\orders")).toBe("db/schema/orders.dbml");
  });

  it("collapses repeated and trailing separators", () => {
    expect(pathOf("db//orders/")).toBe("db/orders.dbml");
  });

  it("trims what the user typed", () => {
    expect(pathOf("  orders  ")).toBe("orders.dbml");
  });

  it("refuses a name that would leave the project folder", () => {
    // Refused here rather than only at the boundary: `create_file` guards its own path, but its
    // refusal reaches the user as a raw error string instead of a labelled field.
    for (const input of ["../outside", "db/../../outside", ".."]) {
      expect(normalizeDocumentPath(input, DBML)).toEqual({ ok: false, reason: "escapes" });
    }
  });

  it("refuses an absolute path", () => {
    // A lone "/" is reported as absolute rather than empty: that is what the user typed, and it is
    // the more useful of the two things to be told.
    for (const input of ["/etc/schema", "/"]) {
      expect(normalizeDocumentPath(input, DBML)).toEqual({ ok: false, reason: "absolute" });
    }
  });

  it("refuses characters a file name cannot hold", () => {
    // `:` is in here, which is also what rejects a Windows drive letter like `C:/schema`.
    for (const input of ["a<b", "a>b", 'a"b', "a|b", "a?b", "a*b", "C:/schema"]) {
      expect(normalizeDocumentPath(input, DBML)).toEqual({ ok: false, reason: "invalidChar" });
    }
  });

  it("refuses control characters, which no file name holds", () => {
    // Checked by code point rather than in the pattern, so this is its own branch to cover.
    for (const input of [`a${String.fromCharCode(0)}b`, `a${String.fromCharCode(0x1f)}b`, `a${String.fromCharCode(0x7f)}b`]) {
      expect(normalizeDocumentPath(input, DBML)).toEqual({ ok: false, reason: "invalidChar" });
    }
  });

  it("refuses an empty name, including one that is only the extension", () => {
    for (const input of ["", "   ", ".dbml"]) {
      expect(normalizeDocumentPath(input, DBML)).toEqual({ ok: false, reason: "empty" });
    }
  });

  it("refuses a bare dot rather than reading it as the folder itself", () => {
    expect(normalizeDocumentPath(".", DBML)).toEqual({ ok: false, reason: "escapes" });
  });
});

// The extension became a parameter when a second tool started creating documents this way, and a
// multi-dot one is the case that a naive `endsWith` on the last dot would get wrong.
describe("an extension with more than one dot", () => {
  it("is appended whole", () => {
    expect(pathOf("checkout", DIAGRAM)).toBe("checkout.diagram.json");
  });

  it("is not doubled when it is already there", () => {
    expect(pathOf("checkout.diagram.json", DIAGRAM)).toBe("checkout.diagram.json");
    expect(pathOf("Checkout.DIAGRAM.JSON", DIAGRAM)).toBe("Checkout.DIAGRAM.JSON");
  });

  it("does not treat a plain .json as already named", () => {
    // `package.json` is not a diagram, and naming one that would make a file the walk skips.
    expect(pathOf("package.json", DIAGRAM)).toBe("package.json.diagram.json");
  });

  it("refuses a name that is only the extension", () => {
    expect(normalizeDocumentPath(".diagram.json", DIAGRAM)).toEqual({ ok: false, reason: "empty" });
  });

  it("keeps working in a subfolder", () => {
    expect(pathOf("flows/onboarding", DIAGRAM)).toBe("flows/onboarding.diagram.json");
  });
});

describe("documentName", () => {
  it("is the last segment", () => {
    expect(documentName("db/nested/orders.dbml")).toBe("orders.dbml");
    expect(documentName("orders.dbml")).toBe("orders.dbml");
  });
});

describe("documentTitle", () => {
  it("drops the extension, so a heading does not shout the file format", () => {
    expect(documentTitle("flows/onboarding.diagram.json", DIAGRAM)).toBe("onboarding");
    expect(documentTitle("orders.dbml", DBML)).toBe("orders");
  });

  it("leaves a name that does not end in the extension alone", () => {
    expect(documentTitle("README", DIAGRAM)).toBe("README");
  });
});
