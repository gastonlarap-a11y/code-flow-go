---
name: verify
description: Launch CodeFlow and prove a change works in the real app, not just in tests. Use when asked to run or screenshot the app, or to confirm a change behaves correctly end to end.
---

# Proving it in the running app

Tests cover the backend. They cannot see a window that opens blank, a tray that never appears, a
drag region that stopped dragging, or a command whose result the renderer reads as `undefined`.
Those are the failures this port is most likely to produce, and the only way to catch them is to
run it.

## Launch

```sh
task dev     # Go rebuild + Vite on 1420, hot reload
```

Or against the real packaged artefact, which is what catches embed and CSP problems that the dev
server hides:

```sh
task build && ./bin/CodeFlow
```

`task smoke` runs the binary's own environment probes (git reachable, a repository can be created
and read back) and exits 0 or 1 — do that first when something looks environmental.

## Watching what it does

The renderer's console is reached through **Safari's Web Inspector** (Develop → the CodeFlow
process) — **in a `task dev` build only**. `task build` compiles with `-tags production`
(BOOT-040), which turns the inspector off, so the packaged artefact is for checking what ships, not
for reading its console. Chromium's remote debugging port does not exist here — that is what the
2.x version of this skill used, and WKWebView has no equivalent. On Windows the WebView2 equivalent
is `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--remote-debugging-port=9222`, also in a dev build.

Go's side writes to `~/CodeFlow/logs/`:

- `shell.log` — window lifecycle and every quit with the reason somebody gave for it
- `errors.log` — one line per command that returned an error, with its type
- `startup.log` — only written when a start-up stage failed

`tail -f ~/CodeFlow/logs/shell.log` while reproducing is usually faster than the inspector.

## What to check, by what changed

| Changed | Prove |
|---|---|
| A command | Call it from the UI, not from a test. Check the panel renders — an empty list is the case that breaks (nil slice → `null` → `.map` crash) |
| An error path | Force it. The banner must show the message, not a raw sentinel like `STALE_REVIEW: ` |
| The window, tray or menu | Close hides · tray *Show* restores · tray *Quit* and ⌘Q exit and leave a reason in `shell.log` · second launch focuses the first · Dock click restores · fullscreen then close lands on the desktop, not a black Space |
| Anything in the renderer | Open the editor (Monaco's workers are the fragile part) and confirm zero `securitypolicyviolation` events in the console |
| A dialog, clipboard or link | Each dialog returns a path or cancels cleanly; copy buttons actually copy; an external link opens the system browser |

## Do not

- Do not report a change as working on the strength of a green test suite. Say which of the above
  you actually did.
- Do not claim anything about Windows from a macOS run. The frameless window, the caption buttons,
  ConPTY and the console-window suppression have no macOS equivalent and are unverified until
  someone runs them there.
