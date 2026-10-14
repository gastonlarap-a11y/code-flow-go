// The TypeScript half of CodeFlow's code map (CODEMAP-005). `backend/codemap/ts.go` embeds this
// file and runs it with the user's own Node against the repository's own TypeScript:
//
//   node --input-type=module - --root <repo> --project <tsconfig.json> [--project …]   (source on stdin)
//
// It prints one JSON document on stdout. Every relation in it comes from the type checker — a call
// lands on the declaration the checker resolves it to, never on a function that merely shares its
// name. Two checker APIs are supported behind one small adapter: the classic JavaScript compiler
// (TypeScript 5 and 6) and the native one TypeScript 7 exposes as `typescript/unstable/sync`.
//
// Plain JavaScript on purpose: Node runs it as it is, with no build step and no type stripping, on
// any version CodeFlow can meet.

import { createRequire } from "node:module";
import path from "node:path";
import process from "node:process";

const VERSION = 1;
const TEST_FILE_RE = /(\.(test|spec)\.[cm]?tsx?$)|(^|\/)__tests__\//;
const BINDINGS_RE = /(?:^|\/)bindings\/(.+?)(?:\/index)?(?:\.[cm]?[jt]s)?$/;
const SQL_HINT_RE = /\b(SELECT|INSERT|UPDATE|DELETE|FROM|JOIN|INTO|CREATE\s+TABLE)\b/i;
const KEY_MAX = 200;
// WRAPPERS are calls that take a function and answer that same function, decorated.
const WRAPPERS = new Set(["memo", "forwardRef", "useCallback", "observer", "debounce", "throttle", "once"]);

function parseArgs(argv) {
  const args = { root: "", projects: [] };
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === "--root") args.root = argv[++i] ?? "";
    else if (argv[i] === "--project") args.projects.push(argv[++i] ?? "");
  }
  if (!args.root || args.projects.length === 0) {
    throw new Error("usage: --root <dir> --project <tsconfig.json> [--project …]");
  }
  return args;
}

function oneLine(text, max = 200) {
  const flat = text.replace(/\s+/g, " ").trim();
  return flat.length <= max ? flat : flat.slice(0, max - 1) + "…";
}

// ---------- the two checker APIs ----------

// loadTypeScript resolves `typescript` the way the project itself would, from its tsconfig's folder.
async function loadTypeScript(tsconfig) {
  const require = createRequire(tsconfig);
  let manifest;
  try {
    manifest = require("typescript/package.json");
  } catch {
    return { note: "TypeScript is not installed for this project (run the package manager's install)" };
  }
  const major = Number.parseInt(String(manifest.version).split(".")[0], 10);
  if (major >= 7) {
    const sync = await import(pathToURL(require.resolve("typescript/unstable/sync")));
    const ast = await import(pathToURL(require.resolve("typescript/unstable/ast")));
    return { version: manifest.version, adapter: nativeAdapter(sync, ast, tsconfig) };
  }
  const ts = require("typescript");
  return { version: manifest.version, adapter: classicAdapter(ts, tsconfig) };
}

function pathToURL(file) {
  return new URL("file://" + (file.startsWith("/") ? "" : "/") + file.replaceAll("\\", "/")).href;
}

// classicAdapter wraps the JavaScript compiler API of TypeScript 5 and 6.
function classicAdapter(ts, tsconfig) {
  const parsed = ts.getParsedCommandLineOfConfigFile(tsconfig, {}, {
    ...ts.sys,
    onUnRecoverableConfigFileDiagnostic: (d) => {
      throw new Error(ts.flattenDiagnosticMessageText(d.messageText, "\n"));
    },
  });
  if (!parsed) throw new Error(`cannot read ${tsconfig}`);
  const program = ts.createProgram({ rootNames: parsed.fileNames, options: { ...parsed.options, noEmit: true } });
  const checker = program.getTypeChecker();
  const resolveAlias = (symbol) =>
    symbol && symbol.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(symbol) : symbol;
  return {
    api: "classic",
    K: ts.SyntaxKind,
    sourceFiles: () => program.getSourceFiles().filter((sf) => !sf.isDeclarationFile),
    declKey: (node) => node,
    // resolve answers, for each node, the declarations of the symbol it refers to.
    resolve(nodes) {
      return nodes.map((node) => {
        let symbol;
        if (node.parent && node.parent.kind === ts.SyntaxKind.ShorthandPropertyAssignment && node.parent.name === node) {
          symbol = checker.getShorthandAssignmentValueSymbol(node.parent);
        } else {
          symbol = checker.getSymbolAtLocation(node);
        }
        return resolveAlias(symbol)?.declarations ?? [];
      });
    },
    // contextualDecls answers the declarations of the type an object literal is contextually typed as.
    contextualDecls(node) {
      const type = checker.getContextualType(node);
      const symbol = type?.aliasSymbol ?? type?.getSymbol();
      return symbol?.declarations ?? [];
    },
    close() {},
  };
}

// nativeAdapter wraps TypeScript 7's native checker, reached over its synchronous API channel.
// Symbols are resolved a file at a time — one round trip per file rather than per identifier.
function nativeAdapter(sync, ast, tsconfig) {
  const api = new sync.API({ cwd: path.dirname(tsconfig) });
  const snapshot = api.updateSnapshot({ openProject: tsconfig });
  const project = snapshot.getProject(tsconfig) ?? snapshot.getProjects()[0];
  if (!project) {
    api.close();
    throw new Error(`no project for ${tsconfig}`);
  }
  const { program, checker } = project;
  const aliasCache = new Map();
  const resolveAlias = (symbol) => {
    if (!symbol || !(symbol.flags & sync.SymbolFlags.Alias)) return symbol;
    if (!aliasCache.has(symbol.id)) aliasCache.set(symbol.id, checker.getAliasedSymbol(symbol));
    return aliasCache.get(symbol.id);
  };
  const handleKey = (handle) => `${handle.path}#${handle.index}`;
  return {
    api: "native",
    K: ast.SyntaxKind,
    sourceFiles: () =>
      program
        .getSourceFileNames()
        .map((name) => program.getSourceFile(name))
        .filter((sf) => sf && !sf.isDeclarationFile),
    declKey: (node) => `${node.getSourceFile().path}#${node.index}`,
    resolve(nodes) {
      if (nodes.length === 0) return [];
      const shorthand = new Map();
      const plain = [];
      nodes.forEach((node, i) => {
        if (node.parent && node.parent.kind === ast.SyntaxKind.ShorthandPropertyAssignment && node.parent.name === node) {
          shorthand.set(i, checker.getShorthandAssignmentValueSymbol(node.parent));
        } else plain.push(i);
      });
      const symbols = plain.length > 0 ? checker.getSymbolAtLocation(plain.map((i) => nodes[i])) : [];
      const out = new Array(nodes.length);
      plain.forEach((i, j) => (out[i] = symbols[j]));
      for (const [i, symbol] of shorthand) out[i] = symbol;
      return out.map((symbol) => (resolveAlias(symbol)?.declarations ?? []).map(handleKey));
    },
    contextualDecls(node) {
      const type = checker.getContextualType(node);
      const symbol = type?.getAliasSymbol?.() ?? type?.getSymbol?.();
      return (symbol?.declarations ?? []).map(handleKey);
    },
    close() {
      api.close();
    },
  };
}

// ---------- the walk ----------

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const out = {
    version: VERSION,
    projects: [],
    symbols: [],
    edges: [],
    bindings: [],
    binding_aliases: [],
    calls: [],
    strings: [],
  };
  const rootDir = path.resolve(args.root);
  // A file belongs to the first project that walks it: tsconfig.json and tsconfig.web.json overlap.
  const walked = new Set();

  for (const tsconfig of args.projects) {
    const entry = { tsconfig: path.relative(rootDir, tsconfig).replaceAll("\\", "/"), typescript: "", api: "", files: 0, note: "" };
    out.projects.push(entry);
    let loaded;
    try {
      loaded = await loadTypeScript(tsconfig);
    } catch (error) {
      entry.note = `could not load TypeScript: ${firstLine(error)}`;
      continue;
    }
    if (!loaded.adapter) {
      entry.note = loaded.note;
      continue;
    }
    entry.typescript = loaded.version;
    entry.api = loaded.adapter.api;
    try {
      entry.files = walkProject(loaded.adapter, rootDir, walked, out);
    } catch (error) {
      entry.note = `indexing failed: ${firstLine(error)}`;
    } finally {
      loaded.adapter.close();
    }
  }
  process.stdout.write(JSON.stringify(out));
}

function firstLine(error) {
  return oneLine(String(error?.message ?? error).split("\n")[0], 300);
}

// walkProject indexes the files of one project not already indexed by an earlier one, and answers
// how many it walked.
function walkProject(adapter, rootDir, walked, out) {
  const K = adapter.K;
  const prefix = rootDir.endsWith(path.sep) ? rootDir : rootDir + path.sep;
  const ids = new Map(); // declaration key → symbol id
  const kinds = new Map(); // symbol id → kind
  const pending = []; // { from, decls, kind, file, line }
  const pendingImplements = []; // { decls, members: [{ name, id }] }
  const pendingAliases = []; // { decls, specifier, name }
  let count = 0;

  for (const sf of adapter.sourceFiles()) {
    const fileName = path.resolve(sf.fileName);
    if (!fileName.startsWith(prefix)) continue;
    const rel = fileName.slice(prefix.length).replaceAll("\\", "/");
    if (/(^|\/)node_modules\//.test(rel) || /(^|\/)bindings\//.test(rel) || walked.has(rel)) continue;
    walked.add(rel);
    count++;
    walkFile(sf, rel);
  }

  const seen = new Set();
  for (const p of pending) {
    for (const decl of p.decls) {
      const to = ids.get(decl);
      if (!to || to === p.from) continue;
      if (p.kind === "ref" && !isFunctionKind(kinds.get(to))) continue;
      const key = `${p.from}|${to}|${p.kind}|${p.line}`;
      if (seen.has(key)) continue;
      seen.add(key);
      out.edges.push({ from: p.from, to, kind: p.kind, file: p.file, line: p.line });
    }
  }
  for (const p of pendingImplements) {
    const iface = p.decls.map((d) => ids.get(d)).find((id) => kinds.get(id) === "interface");
    if (!iface) continue;
    for (const member of p.members) {
      const hub = `${iface}.${member.name}`;
      if (kinds.get(hub) === "interface_method") {
        out.edges.push({ from: hub, to: member.id, kind: "dispatch", file: member.file, line: member.line });
      }
    }
  }
  for (const p of pendingAliases) {
    const iface = p.decls.map((d) => ids.get(d)).find((id) => kinds.get(id) === "interface");
    if (iface) out.binding_aliases.push({ iface, specifier: p.specifier, name: p.name });
  }
  return count;

  function walkFile(sf, rel) {
    const text = sf.text;
    const isTest = TEST_FILE_RE.test(rel);
    const isTsx = rel.endsWith(".tsx");
    const fileScope = { id: `ts:${rel}`, qual: "" };
    addSymbol({ id: fileScope.id, name: rel, kind: isTest ? "test_file" : "module", file: rel, line: 1, end_line: lineOf(sf.end) });
    const bindingImports = new Map(); // local name → { specifier, name }
    const handled = new Set();
    const targets = []; // nodes to resolve, with what to do with each
    const actions = [];

    visit(sf, fileScope);

    // One batch per file: on the native API each batch is one round trip.
    adapter.resolve(targets).forEach((decls, i) => actions[i](decls));

    function lineOf(pos) {
      return sf.getLineAndCharacterOfPosition(pos).line + 1;
    }
    function startOf(node) {
      return node.getStart(sf);
    }
    function textOf(node) {
      return text.slice(startOf(node), node.end);
    }
    function posOf(node) {
      return { file: rel, line: lineOf(startOf(node)), end_line: lineOf(node.end) };
    }
    function nameText(node) {
      if (!node) return undefined;
      if (node.kind === K.Identifier || node.kind === K.PrivateIdentifier) return node.text;
      if (node.kind === K.StringLiteral || node.kind === K.NumericLiteral) return node.text;
      return undefined;
    }

    function addSymbol(symbol) {
      if (kinds.has(symbol.id)) return false; // an overload or a redeclaration: keep the first
      kinds.set(symbol.id, symbol.kind);
      out.symbols.push(symbol);
      return true;
    }

    // declare registers a named symbol for decl and answers the scope of its body.
    function declare(decl, name, kind, scope, signature) {
      const qual = scope.qual ? `${scope.qual}.${name}` : name;
      const id = `ts:${rel}#${qual}`;
      addSymbol({
        id,
        name: qual,
        kind,
        ...posOf(decl),
        parent: scope.qual ? scope.id : "",
        signature: signature ?? "",
        doc: docOf(decl),
      });
      ids.set(adapter.declKey(decl), id);
      return { id, qual };
    }

    function functionKind(name) {
      if (/^use[A-Z0-9]/.test(name)) return "hook";
      if (isTsx && /^[A-Z]/.test(name)) return "component";
      return "function";
    }

    function signatureOf(name, fn) {
      const params = (fn.parameters ?? []).map(textOf).join(", ");
      const ret = fn.type ? `: ${textOf(fn.type)}` : "";
      return oneLine(`${name}(${params})${ret}`);
    }

    // docOf is the comment right above a declaration, first sentence only.
    function docOf(decl) {
      let anchor = decl;
      if (decl.kind === K.VariableDeclaration && decl.parent?.parent?.kind === K.VariableStatement) anchor = decl.parent.parent;
      const trivia = text.slice(anchor.pos, startOf(anchor));
      const blocks = [...trivia.matchAll(/\/\*\*?([\s\S]*?)\*\/|((?:^|\n)[ \t]*\/\/[^\n]*)+/g)];
      const last = blocks.at(-1);
      if (!last || /\n\s*\n/.test(trivia.slice(last.index + last[0].length))) return "";
      const body = (last[1] ?? last[0]).replace(/^\s*(\/\/+|\*+)/gm, " ").replace(/@\w+[^\n]*/g, " ");
      const flat = oneLine(body, 400);
      const dot = flat.indexOf(". ");
      return oneLine(dot >= 0 ? flat.slice(0, dot + 1) : flat, 240);
    }

    // link resolves target later, in the file's one batch, and records an edge to what it names.
    function link(scope, target, kind, at) {
      const where = posOf(at);
      targets.push(target);
      actions.push((decls) => pending.push({ from: scope.id, decls, kind, file: where.file, line: where.line }));
    }

    function isWrapper(callee) {
      const name = callee.kind === K.PropertyAccessExpression ? callee.name.text : callee.kind === K.Identifier ? callee.text : "";
      return WRAPPERS.has(name) || /^with[A-Z]/.test(name);
    }

    function isFunctionLike(node) {
      return (
        node !== undefined &&
        (node.kind === K.FunctionDeclaration ||
          node.kind === K.ArrowFunction ||
          node.kind === K.FunctionExpression ||
          node.kind === K.MethodDeclaration)
      );
    }

    function visit(node, scope) {
      switch (node.kind) {
        case K.ImportDeclaration:
          recordImport(node);
          return;
        case K.ExportDeclaration:
          return;
        case K.FunctionDeclaration: {
          const name = nameText(node.name) ?? "default";
          const inner = declare(node, name, isTest ? "function" : functionKind(name), scope, signatureOf(name, node));
          if (node.body) visit(node.body, inner);
          return;
        }
        case K.VariableDeclaration:
          recordBindingAlias(node);
          if (visitVariable(node, scope)) return;
          break;
        case K.ObjectLiteralExpression:
          visitObject(node, scope, scope.qual);
          return;
        case K.ClassDeclaration:
          visitClass(node, scope);
          return;
        case K.InterfaceDeclaration:
          visitInterface(node, scope);
          return;
        case K.CallExpression:
          recordCall(node, scope);
          break;
        case K.NewExpression:
          link(scope, node.expression, "call", node);
          break;
        case K.JsxOpeningElement:
        case K.JsxSelfClosingElement: {
          const tag = node.tagName.kind === K.PropertyAccessExpression ? node.tagName.name : node.tagName;
          handled.add(tag);
          // Lower-case tags are DOM elements, not components.
          if (/^[A-Z]/.test(textOf(tag))) link(scope, tag, "render", node);
          break;
        }
        case K.Identifier:
          recordRef(node, scope);
          break;
        case K.StringLiteral:
        case K.NoSubstitutionTemplateLiteral:
          recordString(node, node.text, scope);
          break;
        case K.TemplateExpression:
          // `${table}` becomes "?": a placeholder in a table slot is a name only known at run time.
          recordString(node, node.head.text + node.templateSpans.map((s) => "?" + s.literal.text).join(""), scope);
          break;
      }
      node.forEachChild((child) => visit(child, scope));
    }

    function visitVariable(decl, scope) {
      const name = nameText(decl.name);
      const init = decl.initializer;
      if (!name || !init) return false;
      if (isFunctionLike(init)) {
        const inner = declare(decl, name, functionKind(name), scope, signatureOf(name, init));
        ids.set(adapter.declKey(init), inner.id);
        visit(init.body, inner);
        return true;
      }
      // memo(…), forwardRef(…), useCallback(…), withRouter(…): the variable names the function it
      // wraps. Only these: `useStore((s) => s.load)` also takes a function, and its variable is the
      // value the selector picks, not a function declared here.
      if (init.kind === K.CallExpression && isWrapper(init.expression) && init.arguments.some(isFunctionLike)) {
        const inner = declare(decl, name, functionKind(name), scope, oneLine(`${name} = ${textOf(init.expression)}(…)`));
        visit(init, inner);
        return true;
      }
      const prefixed = scope.qual ? `${scope.qual}.${name}` : name;
      if (init.kind === K.ObjectLiteralExpression) {
        visitObject(init, scope, prefixed);
        return true;
      }
      // Any other value — `create((set) => ({ save … }))`, a selector's result — is not a function
      // declared here, so what it calls stays with the scope around it; what it declares is named
      // under the variable, so two stores in one file cannot collide on `save`.
      visit(init, { id: scope.id, qual: prefixed });
      return true;
    }

    // visitObject declares the methods of an object literal under prefix. One contextually typed as
    // an interface of the repository implements it: each method is a dispatch target of the member
    // it shares a name with.
    function visitObject(obj, scope, prefix) {
      const members = [];
      for (const prop of obj.properties) {
        const name = nameText(prop.name);
        const fn = prop.kind === K.MethodDeclaration ? prop : prop.kind === K.PropertyAssignment ? prop.initializer : undefined;
        if (!name || !isFunctionLike(fn)) {
          visit(prop, scope);
          continue;
        }
        const inner = declare(prop, name, "method", { id: scope.id, qual: prefix }, signatureOf(name, fn));
        if (fn !== prop) ids.set(adapter.declKey(fn), inner.id);
        members.push({ name, id: inner.id, ...posOf(prop) });
        if (fn.body) visit(fn.body, inner);
      }
      if (members.length > 0 && !isTest) {
        pendingImplements.push({ decls: adapter.contextualDecls(obj), members });
      }
    }

    function visitClass(node, scope) {
      const className = nameText(node.name) ?? "default";
      const inner = declare(node, className, "class", scope, `class ${className}`);
      const members = [];
      for (const member of node.members) {
        const name = member.kind === K.Constructor ? "constructor" : nameText(member.name);
        let fn;
        if (member.kind === K.MethodDeclaration || member.kind === K.Constructor) fn = member;
        else if (member.kind === K.PropertyDeclaration && isFunctionLike(member.initializer)) fn = member.initializer;
        if (!name || !fn) {
          visit(member, inner);
          continue;
        }
        const method = declare(member, name, "method", inner, signatureOf(name, fn));
        if (fn !== member) ids.set(adapter.declKey(fn), method.id);
        members.push({ name, id: method.id, ...posOf(member) });
        if (fn.body) visit(fn.body, method);
      }
      for (const clause of node.heritageClauses ?? []) {
        for (const type of clause.types) {
          const target = type.expression.kind === K.PropertyAccessExpression ? type.expression.name : type.expression;
          targets.push(target);
          actions.push((decls) => pendingImplements.push({ decls, members }));
        }
      }
    }

    function visitInterface(node, scope) {
      const name = nameText(node.name);
      if (!name) return;
      const inner = declare(node, name, "interface", scope, `interface ${name}`);
      for (const member of node.members) {
        const memberName = nameText(member.name);
        const callable =
          member.kind === K.MethodSignature || (member.kind === K.PropertySignature && member.type?.kind === K.FunctionType);
        if (memberName && callable) declare(member, memberName, "interface_method", inner, oneLine(textOf(member)));
      }
    }

    function recordImport(node) {
      const specifier = node.moduleSpecifier?.kind === K.StringLiteral ? node.moduleSpecifier.text : "";
      const match = BINDINGS_RE.exec(specifier);
      const clause = node.importClause;
      if (!match || !clause) return;
      const tail = match[1];
      if (clause.name) bindingImports.set(clause.name.text, { specifier: tail, name: "" });
      const named = clause.namedBindings;
      if (!named) return;
      if (named.kind === K.NamespaceImport) {
        bindingImports.set(named.name.text, { specifier: tail, name: "*" });
        return;
      }
      for (const element of named.elements ?? []) {
        bindingImports.set(element.name.text, { specifier: tail, name: (element.propertyName ?? element.name).text });
      }
    }

    // recordBindingAlias: `export const FinanceService: FinanceServiceContract = Bound`, an interface
    // of the repository satisfied by a Wails binding as a whole.
    function recordBindingAlias(decl) {
      const init = decl.initializer;
      if (!init || init.kind !== K.Identifier || !decl.type || decl.type.kind !== K.TypeReference) return;
      const binding = bindingImports.get(init.text);
      if (!binding) return;
      const typeName = decl.type.typeName.kind === K.QualifiedName ? decl.type.typeName.right : decl.type.typeName;
      targets.push(typeName);
      actions.push((decls) => pendingAliases.push({ decls, specifier: binding.specifier, name: binding.name }));
    }

    function recordCall(call, scope) {
      const callee = call.expression;
      const nameNode =
        callee.kind === K.PropertyAccessExpression ? callee.name : callee.kind === K.Identifier ? callee : undefined;
      recordKey(call, scope, nameNode);
      if (!nameNode) return;
      handled.add(nameNode);
      if (recordBindingCall(call, scope)) return;
      link(scope, nameNode, "call", call);
    }

    // recordBindingCall: a call into Wails bindings, whose Go method is named by the import itself.
    function recordBindingCall(call, scope) {
      const chain = [];
      let expr = call.expression;
      while (expr.kind === K.PropertyAccessExpression) {
        chain.unshift(expr.name.text);
        expr = expr.expression;
      }
      if (expr.kind !== K.Identifier) return false;
      const binding = bindingImports.get(expr.text);
      if (!binding) return false;
      const where = posOf(call);
      let name = binding.name;
      let member;
      if (chain.length === 0) {
        member = binding.name; // a function imported from a service's own file
        name = "";
      } else if (chain.length === 1) {
        member = chain[0];
      } else {
        name = chain[chain.length - 2];
        member = chain[chain.length - 1];
      }
      if (name === "*") name = "";
      out.bindings.push({ from: scope.id, specifier: binding.specifier, name, member, file: where.file, line: where.line });
      return true;
    }

    // recordKey keeps a call whose first argument is a literal string: the key of an endpoint or a
    // command the backend may serve, matched against Go's registrations later.
    function recordKey(call, scope, nameNode) {
      const first = call.arguments?.[0];
      if (!first) return;
      let value;
      if (first.kind === K.StringLiteral || first.kind === K.NoSubstitutionTemplateLiteral) value = first.text;
      else if (first.kind === K.TemplateExpression) value = first.head.text + first.templateSpans.map((s) => "{}" + s.literal.text).join("");
      if (value === undefined || value.length === 0 || value.length > KEY_MAX || /\s/.test(value.trim())) return;
      const where = posOf(call);
      out.calls.push({ from: scope.id, callee: nameNode ? textOf(nameNode) : "", value, file: where.file, line: where.line });
    }

    function recordRef(id, scope) {
      if (handled.has(id)) return;
      const parent = id.parent;
      if (!parent) return;
      if (parent.kind === K.ShorthandPropertyAssignment) {
        link(scope, id, "ref", id); // `{ onSave }` hands over the function onSave
        return;
      }
      if (parent.kind === K.PropertyAccessExpression) {
        // `s.load` handed on uncalled — a store selector, a callback prop — is a reference to the
        // method; the object it is read from is not.
        if (parent.name === id) link(scope, id, "ref", id);
        return;
      }
      if (
        parent.kind === K.ImportSpecifier ||
        parent.kind === K.ExportSpecifier ||
        parent.kind === K.ImportClause ||
        parent.kind === K.NamespaceImport
      ) {
        return;
      }
      // A declaration's own name is not a reference to it.
      if (parent.name === id) return;
      link(scope, id, "ref", id);
    }

    function recordString(node, value, scope) {
      const parent = node.parent;
      if (
        !parent ||
        parent.kind === K.ImportDeclaration ||
        parent.kind === K.ExportDeclaration ||
        parent.kind === K.JsxAttribute ||
        parent.kind === K.LiteralType ||
        parent.kind === K.ExternalModuleReference
      ) {
        return;
      }
      if (!SQL_HINT_RE.test(value)) return;
      const where = posOf(node);
      out.strings.push({ from: scope.id, text: value, file: where.file, line: where.line });
    }
  }
}

// isFunctionKind says what a reference may point at: something that can be called.
function isFunctionKind(kind) {
  return kind === "function" || kind === "component" || kind === "hook" || kind === "method" || kind === "interface_method";
}

main().catch((error) => {
  process.stderr.write(String(error?.stack ?? error));
  process.exitCode = 1;
});
