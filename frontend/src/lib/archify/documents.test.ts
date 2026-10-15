import { describe, expect, it } from "vitest";
import { archifyDocumentPath, archifyExportName, archifySlug, isArchifyDocument } from "./documents";

describe("Archify documents", () => {
  it("are recognised by their one suffix only", () => {
    expect(isArchifyDocument("codemap/shop-class.archify.json")).toBe(true);
    expect(isArchifyDocument("codemap/SHOP.ARCHIFY.JSON")).toBe(true);
    expect(isArchifyDocument("n8n.workflow.json")).toBe(false);
    expect(isArchifyDocument("checkout.mmd")).toBe(false);
  });

  it("are named after their title and type, in the code map's folder", () => {
    expect(archifySlug("Ciclo de vida del pedido — Órdenes")).toBe("ciclo-de-vida-del-pedido-ordenes");
    expect(archifySlug("   ")).toBe("diagram");
    expect(archifyDocumentPath("shop", "class")).toBe("codemap/shop-class.archify.json");
    expect(archifyDocumentPath("shop", "class", 3)).toBe("codemap/shop-class-3.archify.json");
    expect(archifyExportName("codemap/shop-class.archify.json")).toBe("shop-class");
  });
});
