import { describe, expect, it } from "vitest";
import { dbmlIdentifier, tableKey } from "./identifiers";

describe("tableKey", () => {
  it("lower-cases and defaults the schema", () => {
    expect(tableKey(null, "Users")).toBe("public.users");
    expect(tableKey("Sales", "Orders")).toBe("sales.orders");
  });
});

describe("dbmlIdentifier", () => {
  it("leaves a name that can be written bare alone", () => {
    expect(dbmlIdentifier("order_items")).toBe("order_items");
    expect(dbmlIdentifier("_private2")).toBe("_private2");
  });

  it("quotes a name that cannot", () => {
    expect(dbmlIdentifier("order details")).toBe('"order details"');
    // A leading digit is not an identifier in any SQL dialect DBML targets.
    expect(dbmlIdentifier("2024_sales")).toBe('"2024_sales"');
    expect(dbmlIdentifier('say "hi"')).toBe('"say \\"hi\\""');
  });
});
