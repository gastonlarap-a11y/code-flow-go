import { describe, expect, it } from "vitest";
import { translations } from "../i18n/translations";
import type { RepoMapKind, RepoMapNode } from "../../types/domain";
import { GROUP_PALETTE, declKindLabel, drawnFirst, groupColors, parentOf } from "./labels";

const KINDS: RepoMapKind[] = ["class", "interface", "enum", "struct", "record", "mixin", "type", "function", "method", "const", "var"];

function node(id: string, group: string, files: number, folder = true, importedBy = 0): RepoMapNode {
  return { id, label: id, folder, files, declarations: 0, lines: 0, language: "Go", imported_by: importedBy, group };
}

describe("the drawn map's vocabulary", () => {
  it("names every kind of declaration in both languages", () => {
    for (const kind of KINDS) {
      expect(translations.en[declKindLabel(kind)]).toBeTruthy();
      expect(translations.es[declKindLabel(kind)]).toBeTruthy();
    }
  });

  it("gives the biggest top-level folder the first colour, and the root none", () => {
    const colors = groupColors([node("web/a", "web", 3), node("backend", "backend", 9), node("main.go", "", 1, false), node("web/b", "web", 3)]);
    expect(colors.get("backend")).toBe(GROUP_PALETTE[0]);
    expect(colors.get("web")).toBe(GROUP_PALETTE[1]);
    expect(colors.has("")).toBe(false);
  });

  it("draws folders and the most imported first, and leaves the rest for the panel", () => {
    const { drawn, hidden } = drawnFirst(
      [node("a.ts", "", 1, false, 1), node("lib", "lib", 2), node("b.ts", "", 1, false, 7)],
      2,
    );
    expect(drawn.map((n) => n.id)).toEqual(["lib", "b.ts"]);
    expect(hidden.map((n) => n.id)).toEqual(["a.ts"]);
  });

  it("writes a node's folder for the line above its name", () => {
    expect(parentOf("src/lib/cart.ts")).toBe("src/lib/");
    expect(parentOf("main.go")).toBe("");
  });
});
