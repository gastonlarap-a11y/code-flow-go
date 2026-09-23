---
paths:
  - "frontend/**"
---

# The renderer

77 566 lines across 381 files, copied verbatim from CodeFlow 2.x. **It is not being rewritten.**
Change only what the host swap forces; every other file staying byte-identical is what keeps the UX
contract (`docs/UX-REDESIGN.md`) and the 910 tests meaning what they meant.

## The one door to the backend

`src/lib/bridge/host.ts` is the only file that knows Go exists. It owns the two service names
(`…/backend/bridge.Service`, `…/backend/desktop.HostService`) and exposes `invoke`, `listen` and
`host`. Nothing else imports `@wailsio/runtime`.

Generated bindings are deliberately unused: `Call.ByName` needs no build step and avoids a generated
TypeScript type competing with the hand-written `src/types/domain.ts`.

## WebKit is the target, not Chromium

Minimum macOS 26 (Safari 26+), so anchor positioning, the Popover API and `light-dark()` are native
and need no fallback. What WKWebView still does *not* do:

- `-webkit-app-region` — use `--wails-draggable: drag | no-drag`.
- `navigator.clipboard.writeText` without a live user gesture — use `copyText` from `lib/ui/useCopy`,
  which goes through Go. An image copy must hand `ClipboardItem` the `Promise<Blob>`, never an
  awaited one.
- `target="_blank"` — a capture-phase listener in `main.tsx` routes external links to
  `host.openExternal`. Do not add per-component handlers.
- `user-select` unprefixed — the built CSS must carry `-webkit-user-select`.

## Conventions

- No `any`: `unknown` plus narrowing. An `as` cast carries a comment saying why.
- Type-only imports use `import type`.
- No floating promises: `await`, return, or `void` with a reason.
- Named exports only.
- Tests live beside what they test, run with `vitest`. Logic tests are `.test.ts` in the `node`
  environment; a component test is `.test.tsx`, starts with `// @vitest-environment jsdom`, renders
  with `@testing-library/react`, and mocks only the module that reaches Go (see
  `components/layout/UpdateAlert.test.tsx`). No jest-dom: plain `expect` on what Testing Library finds.

## Known gap: lint is off

typescript-eslint refuses to load against TypeScript 7 until 7.1 ships a compiler API, so
`pnpm lint` runs **oxlint** (type-aware, on typescript-go) from `.oxlintrc.json`, which mirrors
`eslint.config.js`. A rule added to one belongs in the other until one is retired; `pnpm lint:eslint`
is the old command, kept for the day support lands.
