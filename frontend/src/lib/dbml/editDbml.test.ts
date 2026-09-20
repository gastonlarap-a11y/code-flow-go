import { describe, expect, it } from "vitest";
import {
  lineAndColumn,
  removeTable,
  renameColumn,
  renameTable,
  retypeColumn,
  scanDocument,
  tableNameSpan,
} from "./editDbml";
import { parseDbmlModel } from "./parse";

/**
 * Every case that produces a document re-parses it, the way `emitDbml.test.ts` does. Text surgery
 * that yields something `@dbml/core` refuses is the failure this module exists to avoid, and only
 * the parser can say whether it did.
 */
function reparsed(source: string | null): ReturnType<typeof parseDbmlModel> {
  expect(source).not.toBeNull();
  const parsed = parseDbmlModel(source!);
  expect(parsed.ok ? null : parsed.error).toBeNull();
  return parsed;
}

const SHOP = `// the catalogue
Table users as U {
  id integer [pk]
  email varchar(120) [unique, note: 'lower case']
  Note: 'people who buy'
}

Table orders {
  id integer [pk]
  user_id integer [not null, ref: > U.id]
  total decimal(10, 2)

  Indexes {
    (user_id, id) [name: 'by_user']
  }
}

Table sales.invoices {
  id integer [pk]
  order_id integer
}

Ref: sales.invoices.order_id > orders.id
`;

describe("scanDocument", () => {
  it("finds every table, with the schema and the alias each one was written with", () => {
    const spans = scanDocument(SHOP);

    expect(spans.tables.map((table) => table.key)).toEqual([
      "public.users",
      "public.orders",
      "sales.invoices",
    ]);
    expect(spans.tables[0]?.alias).toBe("U");
    expect(spans.tables[2]?.schema).toBe("sales");
  });

  it("reads a column's type with its arguments, and leaves the settings out of it", () => {
    const [users, orders] = scanDocument(SHOP).tables;

    const email = users?.columns.find((column) => column.name === "email");
    expect(SHOP.slice(email!.typeSpan.start, email!.typeSpan.end)).toBe("varchar(120)");

    const total = orders?.columns.find((column) => column.name === "total");
    expect(SHOP.slice(total!.typeSpan.start, total!.typeSpan.end)).toBe("decimal(10, 2)");
  });

  it("reads an array type without mistaking its brackets for settings", () => {
    const [table] = scanDocument("Table a {\n  tags text[] [note: 'many']\n}\n").tables;
    const tags = table?.columns[0];

    expect(tags?.name).toBe("tags");
    expect("Table a {\n  tags text[] [note: 'many']\n}\n".slice(tags!.typeSpan.start, tags!.typeSpan.end)).toBe(
      "text[]",
    );
  });

  it("treats `note` and `indexes` as columns when no block follows them", () => {
    const [table] = scanDocument("Table a {\n  note text\n  indexes integer\n}\n").tables;

    expect(table?.columns.map((column) => column.name)).toEqual(["note", "indexes"]);
  });

  it("keeps a table's note out of its columns", () => {
    const [users] = scanDocument(SHOP).tables;

    expect(users?.columns.map((column) => column.name)).toEqual(["id", "email"]);
  });

  it("finds the relationship written on a column as well as the one written on its own", () => {
    const spans = scanDocument(SHOP);
    const inline = spans.tables[1]?.columns.find((column) => column.name === "user_id")?.inlineRefs;

    expect(inline?.[0]?.endpoint.table).toBe("U");
    expect(spans.refs).toHaveLength(1);
    expect(spans.refs[0]?.endpoints.map((end) => `${end.schema}.${end.table}.${end.columns[0]?.name}`)).toEqual([
      "sales.invoices.order_id",
      "null.orders.id",
    ]);
  });

  it("reads a composite endpoint as one table and two columns", () => {
    const spans = scanDocument("Table a { x int\n y int }\nTable b { x int\n y int }\nRef: b.(x, y) > a.(x, y)\n");

    expect(spans.refs[0]?.endpoints[0]?.columns.map((column) => column.name)).toEqual(["x", "y"]);
    expect(spans.refs[0]?.endpoints[0]?.table).toBe("b");
  });

  it("does not let a brace inside a note end the table", () => {
    const spans = scanDocument("Table a {\n  id int\n  Note: 'a } brace'\n}\nTable b { id int }\n");

    expect(spans.tables.map((table) => table.name)).toEqual(["a", "b"]);
  });
});

describe("removeTable", () => {
  it("answers null for a table the document does not declare", () => {
    expect(removeTable(SHOP, "public.missing")).toBeNull();
  });

  it("takes the table, the relationships that named it and the settings that pointed at it", () => {
    const next = removeTable(SHOP, "public.orders");
    const parsed = reparsed(next);

    expect(parsed.ok && parsed.model.tables.map((table) => table.key)).toEqual([
      "public.users",
      "sales.invoices",
    ]);
    expect(parsed.ok && parsed.model.refs).toEqual([]);
    // The relationship lived in `orders`; `users` keeps every setting it had.
    expect(next).toContain("email varchar(120) [unique, note: 'lower case']");
    expect(next).not.toContain("ref:");
  });

  it("removes an inline relationship that pointed at the deleted table, and keeps the column", () => {
    const source = "Table users { id int [pk] }\nTable posts {\n  user_id int [not null, ref: > users.id]\n}\n";
    const next = removeTable(source, "public.users");

    expect(next).toContain("user_id int [not null]");
    reparsed(next);
  });

  it("drops the brackets when the relationship was the only setting on the column", () => {
    const source = "Table users { id int [pk] }\nTable posts {\n  user_id int [ref: > users.id]\n}\n";
    const next = removeTable(source, "public.users");

    expect(next).toContain("user_id int\n");
    expect(next).not.toContain("[]");
    reparsed(next);
  });

  it("removes the relationship when it was written first among the settings", () => {
    const source = "Table users { id int [pk] }\nTable posts {\n  user_id int [ref: > users.id, not null]\n}\n";
    const next = removeTable(source, "public.users");

    expect(next).toContain("user_id int [not null]");
    reparsed(next);
  });

  it("takes the table out of every group that named it, because a group with a ghost will not parse", () => {
    const source = `Table a { id int }\nTable b { id int }\n\nTableGroup g {\n  a\n  b\n}\n`;
    const next = removeTable(source, "public.a");

    expect(next).toContain("TableGroup g {\n  b\n}");
    reparsed(next);
  });

  it("keeps the comments and the blank lines of everything it did not touch", () => {
    const next = removeTable(SHOP, "sales.invoices");

    expect(next).toContain("// the catalogue");
    expect(next).toContain("Note: 'people who buy'");
    expect(next).toContain("(user_id, id) [name: 'by_user']");
    // Not three newlines in a row where the table used to be.
    expect(next).not.toMatch(/\n\n\n/);
    reparsed(next);
  });

  it("cuts only the lines that named the table when a Ref block holds more than one", () => {
    const source = `Table a { id int }
Table b { a_id int\n c_id int }
Table c { id int }

Ref together {
  a.id < b.a_id
  c.id < b.c_id
}
`;
    const next = removeTable(source, "public.a");

    expect(next).toContain("c.id < b.c_id");
    expect(next).not.toContain("a.id < b.a_id");
    reparsed(next);
  });
});

describe("renameTable", () => {
  it("renames the header and every relationship that named the table", () => {
    const next = renameTable(SHOP, "public.orders", "purchases");
    const parsed = reparsed(next);

    expect(next).toContain("Table purchases {");
    expect(next).toContain("Ref: sales.invoices.order_id > purchases.id");
    expect(parsed.ok && parsed.model.refs[0]?.to.tableKey).toBe("public.purchases");
  });

  it("leaves an alias alone: the shorthand is not the name", () => {
    const next = renameTable(SHOP, "public.users", "people");

    expect(next).toContain("Table people as U {");
    expect(next).toContain("ref: > U.id");
    reparsed(next);
  });

  it("quotes a name that cannot be written bare", () => {
    const next = renameTable(SHOP, "public.orders", "order details");

    expect(next).toContain('Table "order details" {');
    expect(next).toContain('Ref: sales.invoices.order_id > "order details".id');
    reparsed(next);
  });

  it("renames the entry a group holds", () => {
    const source = "Table a { id int }\nTableGroup g {\n  a\n}\n";
    const next = renameTable(source, "public.a", "b");

    expect(next).toContain("TableGroup g {\n  b\n}");
    reparsed(next);
  });

  it("refuses a name that is blank", () => {
    expect(renameTable(SHOP, "public.orders", "   ")).toBeNull();
  });

  it("produces a document the parser refuses when the name is taken, rather than pretending", () => {
    const next = renameTable(SHOP, "public.orders", "users");

    expect(next).not.toBeNull();
    expect(parseDbmlModel(next!).ok).toBe(false);
  });
});

describe("renameColumn", () => {
  it("renames the column and the relationships and indexes that name it", () => {
    const next = renameColumn(SHOP, "public.orders", "id", "order_id");
    const parsed = reparsed(next);

    expect(next).toContain("Ref: sales.invoices.order_id > orders.order_id");
    expect(next).toContain("(user_id, order_id) [name: 'by_user']");
    expect(parsed.ok && parsed.model.tables[1]?.columns.map((column) => column.name)).toEqual([
      "order_id",
      "user_id",
      "total",
    ]);
  });

  it("follows a relationship written through the table's alias", () => {
    const next = renameColumn(SHOP, "public.users", "id", "uuid");

    expect(next).toContain("ref: > U.uuid");
    reparsed(next);
  });

  it("answers null for a column the table does not declare", () => {
    expect(renameColumn(SHOP, "public.orders", "nope", "x")).toBeNull();
  });
});

describe("retypeColumn", () => {
  it("replaces the type and nothing else", () => {
    const next = retypeColumn(SHOP, "public.users", "email", "text");
    const parsed = reparsed(next);

    expect(next).toContain("email text [unique, note: 'lower case']");
    expect(parsed.ok && parsed.model.tables[0]?.columns[1]?.type).toBe("text");
  });

  it("answers null for a blank type", () => {
    expect(retypeColumn(SHOP, "public.users", "email", " ")).toBeNull();
  });
});

/**
 * The shapes a real `.dbml` takes that the easy fixture above does not: other line endings, tabs,
 * quoted names, blocks that hold braces, brackets inside strings, and the file that ends without a
 * newline. Each of these was written to try to break the scanner; the first one did.
 */
describe("documents that are not the easy shape", () => {
  it("takes both relationships when one column holds two to the same table", () => {
    const source =
      "Table users { id int [pk]\n name varchar }\nTable posts {\n  who int [ref: > users.id, ref: > users.name]\n}\n";
    const next = removeTable(source, "public.users");

    expect(next).toContain("who int\n");
    expect(next).not.toContain("ref:");
    reparsed(next);
  });

  it("handles a table block that ends the file without a newline", () => {
    expect(removeTable("Table a { id int }\nTable b { id int }", "public.b")).toBe("Table a { id int }\n");
  });

  it("handles CRLF line endings", () => {
    const source = "Table a {\r\n  id int\r\n}\r\n\r\nTable b {\r\n  a_id int [ref: > a.id]\r\n}\r\n";

    expect(removeTable(source, "public.a")).toBe("Table b {\r\n  a_id int\r\n}\r\n");
  });

  it("handles tabs as indentation", () => {
    const source = "Table a {\n\tid int [pk]\n}\nTable b {\n\ta_id int [ref: > a.id]\n}\n";

    expect(removeTable(source, "public.a")).toBe("Table b {\n\ta_id int\n}\n");
  });

  it("takes a relationship that carries actions", () => {
    const source = "Table a { id int [pk] }\nTable b { a_id int }\nRef: b.a_id > a.id [delete: cascade, update: no action]\n";

    expect(removeTable(source, "public.a")).not.toContain("Ref:");
  });

  it("does not mistake a bracket inside a string for the end of the settings", () => {
    const source = "Table a { id int [pk] }\nTable b {\n  k int [note: 'an [odd] one', ref: > a.id]\n}\n";

    expect(removeTable(source, "public.a")).toContain("k int [note: 'an [odd] one']");
  });

  it("does not mistake a comment marker inside a note for a comment", () => {
    const spans = scanDocument("Table a {\n  id int [note: 'see // here']\n  b int\n}\n");

    expect(spans.tables[0]?.columns.map((column) => column.name)).toEqual(["id", "b"]);
  });

  it("reads past a note written as a block, braces and all", () => {
    const source = "Table a {\n  id int\n  Note {\n    '''\n    many lines\n    with a } brace\n    '''\n  }\n}\nTable b { id int }\n";

    expect(scanDocument(source).tables.map((table) => table.name)).toEqual(["a", "b"]);
  });

  it("reads an empty table body", () => {
    expect(scanDocument("Table a {\n}\nTable b { id int }\n").tables.map((table) => table.name)).toEqual(["a", "b"]);
  });

  it("leaves an enum and a project block alone", () => {
    const source = "Project shop {\n  database_type: 'PostgreSQL'\n}\n\nEnum s {\n  on\n  off\n}\n\nTable a {\n  state s\n}\n\nTable b { id int }\n";
    const next = removeTable(source, "public.a");

    expect(next).toContain("Project shop");
    expect(next).toContain("Enum s");
    expect(next).not.toContain("state s");
    reparsed(next);
  });

  it("takes an inline relationship written to a composite key", () => {
    const source = "Table a {\n  x int\n  y int\n  indexes { (x, y) [pk] }\n}\nTable b {\n  k int [ref: > a.(x, y)]\n}\n";

    expect(removeTable(source, "public.a")).toContain("k int\n");
  });

  it("takes a relationship that crosses schemas", () => {
    const source = "Table sales.a { id int [pk] }\nTable shop.b {\n  a_id int\n}\nRef: shop.b.a_id > sales.a.id\n";

    expect(removeTable(source, "sales.a")).not.toContain("Ref:");
  });

  it("renames a table that a quoted reference names", () => {
    const source = 'Table "order details" { id int [pk] }\nTable b {\n  d_id int [ref: > "order details".id]\n}\n';
    const next = renameTable(source, "public.order details", "lines");

    expect(next).toContain("Table lines {");
    expect(next).toContain("ref: > lines.id");
    reparsed(next);
  });

  it("retypes a column that has no settings", () => {
    expect(retypeColumn("Table a {\n  id int\n}\n", "public.a", "id", "bigint")).toBe("Table a {\n  id bigint\n}\n");
  });
});

describe("tableNameSpan", () => {
  it("points at the name, so an editor can put the caret on it", () => {
    const span = tableNameSpan(SHOP, "sales.invoices");

    expect(SHOP.slice(span!.start, span!.end)).toBe("invoices");
    expect(lineAndColumn(SHOP, span!.start)).toEqual({ line: 18, column: 13 });
  });

  it("answers null for a table that is not there", () => {
    expect(tableNameSpan(SHOP, "public.nope")).toBeNull();
  });
});
