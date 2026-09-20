import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
// Monaco is deliberately absent from this file. Its setup used to be imported here, which put the
// whole editor in the entry chunk and made every launch pay for it. It now travels with the first
// component that actually needs an editor, through `lib/monacoEditor.ts`.

// The two typefaces the app names in `--font-sans` / `--font-mono`, self-hosted. Before this they
// were named and never loaded, so the app fell back to Segoe UI on Windows and -apple-system on
// macOS and rendered differently on each. Bundled rather than fetched for the same reason Monaco is:
// this app works offline. Only the upright weights ship — nothing here renders italic UI text — and
// each file carries a `unicode-range`, so a latin-script session never touches the greek or
// cyrillic subsets. These must precede `index.css`: the `@font-face` rules have to be registered
// before the rule that asks for the family.
import "@fontsource-variable/inter";
import "@fontsource-variable/jetbrains-mono";
import "./index.css";
import { pushErrorToast } from "./state/toastStore";
import { translate } from "./state/languageStore";
import { reloadForStaleChunk } from "./lib/lazyRetry";
import { isCancellation } from "./lib/cancellation";
import { openUrl } from "./lib/bridge/shell";

/** Vite's own event, which it does not put in `WindowEventMap` (vitejs/vite#17508). The error is on
 * `payload`, not on `detail`. */
interface VitePreloadError extends Event {
  payload: Error;
}
declare global {
  interface WindowEventMap {
    "vite:preloadError": VitePreloadError;
  }
}

/**
 * The last resort for a promise nobody caught.
 *
 * `eslint.config.js` turns off `no-misused-promises`' `checksVoidReturn` for JSX attributes, on the
 * stated grounds that rewriting `onClick={asyncFn}` to `onClick={() => void asyncFn()}` leaves the
 * rejection exactly as unhandled as before. That reasoning holds, and it is what makes a net at the
 * edge the right place rather than a `try`/`catch` at each of the ninety-odd call sites: with none,
 * a backend failure reached by one of those handlers produced no message anywhere at all.
 *
 * Deliberately not a replacement for handling an error where it happens — a caller that knows what
 * failed can say something better than this can, and the four paths this bug was reported through
 * now do.
 */
window.addEventListener("unhandledrejection", (event) => {
  const reason: unknown = event.reason;

  // A cancellation is not a failure, and this net was reporting it as one. Monaco cancels its own
  // in-flight work when an editor is disposed, so switching between work items raised a toast per
  // switch for something that had gone exactly right. See `lib/cancellation.ts`.
  if (isCancellation(reason)) return;

  // `.message`, not `String(reason)`: the latter prepends `Error: ` to text that already reads as a
  // sentence, and the transport had already left one of its own in there.
  pushErrorToast(
    translate("toast.unexpected", { error: reason instanceof Error ? reason.message : String(reason) }),
  );
});

/**
 * A chunk that no longer exists, caught before it reaches a component.
 *
 * Vite fires this from the preload helper it wraps every built dynamic import in — production only;
 * the dev server serves modules directly and has nothing to preload. The cause is always the same:
 * chunk names carry a content hash, an update replaced `renderer/dist`, and this window is still
 * running the `index.html` it started with, asking for hashes that are gone.
 *
 * Reloading is the only fix — there is no way to rewrite the running document's idea of the chunk
 * map — and `reloadForStaleChunk` allows exactly one, so a build that is broken rather than merely
 * stale surfaces as an error instead of an endless reload.
 */
window.addEventListener("vite:preloadError", (event) => {
  // Without this the rejection also reaches `window.onerror` as an unhandled failure.
  event.preventDefault();
  if (!reloadForStaleChunk()) {
    pushErrorToast(translate("toast.unexpected", { error: String((event as VitePreloadError).payload) }));
  }
});

/**
 * External links, opened in the user's browser rather than swallowed by the webview.
 *
 * Electron had two handlers for this: `will-navigate` refused anything outside `app://codeflow/`,
 * and `setWindowOpenHandler` sent `target="_blank"` to the system browser. A webview has neither,
 * and in WKWebView a `target="_blank"` link simply does nothing — a dead link, with no error.
 *
 * One capture-phase listener replaces both. Capture, so it runs before any component's own click
 * handler can call `stopPropagation`; `closest("a")` so it catches a click on an icon inside a
 * link; and it covers the links `marked` renders from pull-request descriptions and release notes,
 * which no component owns and which could not be fixed one call site at a time.
 *
 * The scheme is re-checked in Go before anything is handed to the OS (`HostService.OpenExternal`).
 * This side decides *which* clicks are navigations; that side decides what is safe to open.
 */
document.addEventListener(
  "click",
  (event) => {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey) return;

    const anchor = (event.target as Element | null)?.closest?.("a");
    const href = anchor?.getAttribute("href");
    if (!href || !/^https?:\/\//i.test(href)) return;

    event.preventDefault();
    void openUrl(href).catch((error: unknown) => {
      pushErrorToast(translate("toast.unexpected", { error: String(error) }));
    });
  },
  { capture: true },
);

/**
 * `window.open` does nothing in a webview either, and a few libraries reach for it directly.
 * Routing it to the same place keeps "the link did nothing" from coming back through another door.
 */
window.open = ((url?: string | URL) => {
  if (url) void openUrl(String(url)).catch(() => {});
  return null;
}) as typeof window.open;

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
