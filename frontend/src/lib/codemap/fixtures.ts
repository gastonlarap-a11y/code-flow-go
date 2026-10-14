import type {
  CodemapFlow,
  CodemapImpact,
  CodemapOverview,
  CodemapSymbolDetail,
  CodemapSymbolRef,
} from "../../types/domain";

/** Hand-built answers of the code map's commands, shared by the store's and the view's tests. */

export function symbolRef(id: string, overrides: Partial<CodemapSymbolRef> = {}): CodemapSymbolRef {
  // Named the way the backend names them: "go:app/shop.Save" is "shop.Save", "ts:a.ts#List" is "List".
  const bare = id.replace(/^[a-z]+:/, "");
  const name = bare.includes("#") ? (bare.split("#").at(-1) ?? bare) : bare.slice(bare.lastIndexOf("/") + 1);
  return {
    id,
    name,
    kind: "func",
    lang: id.startsWith("go:") ? "go" : id.startsWith("sql:") ? "sql" : "ts",
    file: "src/app.ts",
    line: 3,
    end_line: 9,
    signature: `${name}()`,
    ...overrides,
  };
}

export function overview(overrides: Partial<CodemapOverview> = {}): CodemapOverview {
  return {
    status: {
      root_path: "/repo",
      nodes: 120,
      edges: 340,
      languages: [
        { lang: "go", state: "mapped", files: 10, symbols: 60, detail: "" },
        { lang: "ts", state: "mapped", files: 12, symbols: 58, detail: "TypeScript 7.0.2 (native)" },
        { lang: "sql", state: "mapped", files: 2, symbols: 2, detail: "" },
      ],
      notes: [],
      built_at: "2026-10-14T12:00:00Z",
      duration_ms: 1800,
      archify: "",
    },
    areas: [{ path: "src", lang: "ts", symbols: 58 }],
    services: [{ name: "Service", file: "shop/shop.go", methods: 4, called_by: 3 }],
    endpoints: 2,
    tables: [{ table: symbolRef("sql:items", { kind: "table", file: "db/001.sql", line: 1 }), readers: 2, writers: 1 }],
    hotspots: [{ symbol: symbolRef("ts:src/api.ts#invoke"), callers: 9 }],
    ...overrides,
  };
}

export function detail(id: string, overrides: Partial<CodemapSymbolDetail> = {}): CodemapSymbolDetail {
  return {
    symbol: symbolRef(id),
    doc: "Saves one item.",
    parent: null,
    bound: "",
    callers: [
      {
        symbol: symbolRef("ts:src/List.tsx#List", { kind: "component", file: "src/List.tsx", line: 4 }),
        kind: "call",
        file: "src/List.tsx",
        line: 12,
        via: null,
      },
    ],
    callees: [],
    implementations: [],
    implements: [],
    tables: [
      {
        table: symbolRef("sql:items", { kind: "table", file: "db/001.sql", line: 1 }),
        read: false,
        write: true,
        mention: false,
        direct: false,
        via: symbolRef("go:app/shop.Save"),
      },
    ],
    tests: [],
    unresolved: [],
    ...overrides,
  };
}

export function flow(rootId: string): CodemapFlow {
  return {
    root: {
      symbol: symbolRef(rootId),
      via: "",
      file: "",
      line: 0,
      repeat: false,
      truncated: false,
      children: [
        {
          symbol: symbolRef("ep:item_save", { kind: "endpoint", lang: "go", file: "shop/commands.go", line: 8 }),
          via: "cross",
          file: "src/app.ts",
          line: 5,
          repeat: false,
          truncated: false,
          children: [
            {
              symbol: symbolRef("sql:items", { kind: "table", file: "db/001.sql", line: 1 }),
              via: "write",
              file: "shop/save.go",
              line: 9,
              repeat: false,
              truncated: false,
              children: [],
            },
          ],
        },
      ],
    },
    steps: 2,
    truncated: false,
  };
}

export function impact(rootId: string): CodemapImpact {
  return {
    root: symbolRef(rootId),
    levels: [[symbolRef("ts:src/List.tsx#List", { kind: "component" })]],
    entry_points: [symbolRef("ts:src/List.tsx#List", { kind: "component" })],
    go_tests: [
      { package_dir: "shop", tests: ["TestSave"], working_dir: "", command: "go test -run '^(TestSave)$' ./shop" },
    ],
    ts_tests: [
      { package_dir: "web", files: ["web/List.test.tsx"], runner: "vitest", command: "npx vitest run List.test.tsx" },
    ],
    truncated: false,
  };
}
