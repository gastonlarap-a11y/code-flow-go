import { describe, expect, it } from "vitest";
import { carriedPosition, inlineEdit, planEdit, relationsTouching } from "./cardEdit";
import { parseDbmlModel } from "./parse";
import { emptyModel } from "./model";

const SHOP = parseDbmlModel(`Table users { id int [pk] }
Table orders {
  id int [pk]
  user_id int [ref: > users.id]
}
Table tree {
  id int [pk]
  parent_id int [ref: > tree.id]
}
`);

describe("planEdit", () => {
  it("applies an edit that parses", () => {
    expect(planEdit(SHOP, "Table a { id int }\n")).toEqual({
      kind: "apply",
      source: "Table a { id int }\n",
    });
  });

  it("refuses to act at all while the buffer does not parse", () => {
    const broken = parseDbmlModel("Table {{{");
    expect(broken.ok).toBe(false);

    // Even though the replacement itself is perfectly good: the card that asked for it is drawing
    // the last model that parsed, not what the document says now.
    expect(planEdit(broken, "Table a { id int }\n")).toEqual({ kind: "stale" });
  });

  it("reports a target the text operations could not find", () => {
    expect(planEdit(SHOP, null)).toEqual({ kind: "missing" });
  });

  it("refuses a result the parser rejects, and quotes its first line", () => {
    const outcome = planEdit(SHOP, "Table a { id int }\nTable a { id int }\n");

    expect(outcome.kind).toBe("refused");
    if (outcome.kind !== "refused") return;
    expect(outcome.error).not.toContain("\n");
    expect(outcome.error.length).toBeGreaterThan(0);
  });

  it("puts the stale gate before the missing one: a broken buffer is the first thing to say", () => {
    expect(planEdit(parseDbmlModel("Table {{{"), null)).toEqual({ kind: "stale" });
  });
});

describe("inlineEdit", () => {
  it("commits a trimmed value", () => {
    expect(inlineEdit("  order_items  ", "orders")).toEqual({ kind: "commit", value: "order_items" });
  });

  it("cancels an empty one rather than deleting the name", () => {
    expect(inlineEdit("", "orders")).toEqual({ kind: "cancel" });
    expect(inlineEdit("   ", "orders")).toEqual({ kind: "cancel" });
  });

  it("cancels a value that did not change, whitespace included", () => {
    expect(inlineEdit("orders", "orders")).toEqual({ kind: "cancel" });
    expect(inlineEdit(" orders ", "orders")).toEqual({ kind: "cancel" });
  });
});

describe("relationsTouching", () => {
  it("counts both directions", () => {
    expect(SHOP.ok && relationsTouching(SHOP.model, "public.users")).toBe(1);
    expect(SHOP.ok && relationsTouching(SHOP.model, "public.orders")).toBe(1);
  });

  it("counts a table that references itself once", () => {
    expect(SHOP.ok && relationsTouching(SHOP.model, "public.tree")).toBe(1);
  });

  it("answers zero for a table nothing names", () => {
    expect(relationsTouching(emptyModel(), "public.users")).toBe(0);
  });
});

describe("carriedPosition", () => {
  const table = { key: "public.orders", schema: "public" };

  it("moves the position to the renamed table's key", () => {
    expect(carriedPosition({ "public.orders": { x: 40, y: 80 } }, table, " purchases ")).toEqual({
      key: "public.purchases",
      point: { x: 40, y: 80 },
    });
  });

  it("has nothing to move when the table was never placed by hand", () => {
    expect(carriedPosition({}, table, "purchases")).toBeNull();
  });

  it("has nothing to move when only the case changed, because the key did not", () => {
    expect(carriedPosition({ "public.orders": { x: 1, y: 2 } }, table, "Orders")).toBeNull();
  });

  it("keeps the table's schema", () => {
    expect(
      carriedPosition({ "sales.orders": { x: 1, y: 2 } }, { key: "sales.orders", schema: "sales" }, "invoices"),
    ).toEqual({ key: "sales.invoices", point: { x: 1, y: 2 } });
  });
});
