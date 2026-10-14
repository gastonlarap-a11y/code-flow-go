import { describe, expect, test } from "vitest";
import { translations } from "../i18n/translations";
import type { CodemapEdgeKind, CodemapKind } from "../../types/domain";
import { edgeLabel, KIND_ICONS, kindLabel, location, qualifier, shortName, stateTone } from "./labels";

const KINDS: CodemapKind[] = [
  "func",
  "method",
  "type",
  "interface",
  "interface_method",
  "component",
  "hook",
  "test",
  "test_file",
  "module",
  "table",
  "endpoint",
];
const EDGES: CodemapEdgeKind[] = ["call", "ref", "render", "dispatch", "cross", "read", "write", "mention"];

describe("the code map's vocabulary", () => {
  test("every kind the backend sends has a label in both languages and an icon", () => {
    for (const kind of KINDS) {
      expect(translations.en[kindLabel(kind)]).toBeTruthy();
      expect(translations.es[kindLabel(kind)]).toBeTruthy();
      expect(KIND_ICONS[kind]).toBeTruthy();
    }
  });

  test("every edge has a label", () => {
    for (const edge of EDGES) expect(translations.es[edgeLabel(edge)]).toBeTruthy();
  });

  test("a row shows the last segment, and the rest beneath it", () => {
    expect(shortName("finance.FinanceService.CreateExpense")).toBe("CreateExpense");
    expect(qualifier("finance.FinanceService.CreateExpense")).toBe("finance.FinanceService");
    expect(shortName("frontend/src/App.tsx")).toBe("App.tsx");
    expect(shortName('diagram.Register("diagram_list_documents")')).toBe('Register("diagram_list_documents")');
    expect(qualifier('diagram.Register("a.b")')).toBe("diagram");
    expect(shortName("items")).toBe("items");
    expect(qualifier("items")).toBe("");
  });

  test("a location without a line is just the file", () => {
    expect(location("a.go", 12)).toBe("a.go:12");
    expect(location("a.go", 0)).toBe("a.go");
  });

  test("a missing toolchain warns rather than fails", () => {
    expect(stateTone("mapped")).toBe("success");
    expect(stateTone("missing_tool")).toBe("warning");
    expect(stateTone("failed")).toBe("danger");
  });
});
