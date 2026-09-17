import { Call, Events, Window as RuntimeWindow } from "@wailsio/runtime";

/**
 * `invoke` and `listen` over the Wails bridge.
 *
 * This file exists so `lib/ipc/commands.ts`, `apiCommands.ts` and `events.ts` keep their bodies.
 * Each imports its primitive exactly once, on line 1 — all 212 wrappers call
 * `invoke<T>("name", argsObject)` and all 11 call `listen<T>("event", e => handler(e.payload))`.
 * Matching those two signatures precisely is what let the host change from Electron to Wails
 * without touching 125 files.
 *
 * Which makes fidelity here the thing to get right. `listen` in particular returns a
 * `Promise<UnlistenFn>`, not an `UnlistenFn` — `App.tsx` does `unlisten.then((f) => f())`, so
 * returning the function directly would break cleanup in a way TypeScript would not catch.
 */

/** Matches the event-payload type. */
export type UnlistenFn = () => void;

/** Matches the shape `listen` hands its callback. */
export interface Event<T> {
  payload: T;
}

/**
 * Wails names a bound method `<Go package path>.<type>.<method>` (bindings.go builds it).
 *
 * These two strings are the only place the renderer knows anything about the backend's Go module
 * path, which is why they are constants at the top of the one file allowed to hold them. A Go test
 * registers both services and asserts these exact names resolve, so a package move breaks a build
 * rather than the running app.
 *
 * Generated bindings are deliberately not used for the bridge: the generator would give
 * `json.RawMessage` a TypeScript type that competes with the hand-written `types/domain.ts`, and
 * `Call.ByName` needs no build step to stay in sync with a registry keyed by string anyway.
 */
const BRIDGE = "github.com/gastonlarap-a11y/code-flow/backend/bridge.Service";
const HOST = "github.com/gastonlarap-a11y/code-flow/backend/desktop.HostService";

export type Platform = "macos" | "windows" | "linux" | "unknown";

/**
 * The current OS, resolved synchronously.
 *
 * `lib/platform.ts` memoises this and calls it from non-React code — key handling, stores — so it
 * cannot become a promise. The user agent is deterministic per webview and needs no round trip:
 * WKWebView says "Macintosh", WebView2 says "Windows NT".
 */
function detectPlatform(): Platform {
  if (typeof navigator === "undefined") return "unknown";
  const ua = navigator.userAgent;
  if (/Macintosh|Mac OS X/.test(ua)) return "macos";
  if (/Windows NT/.test(ua)) return "windows";
  if (/Linux|X11/.test(ua)) return "linux";
  return "unknown";
}

/**
 * The message a backend failure carries.
 *
 * Unlike Electron's `ipcMain.handle`, which wrapped every rejection in
 * `Error invoking remote method '<channel>': ` and left a bare `Error: ` behind it, Wails delivers
 * the Go error's text unchanged: `bindings.go` builds a `CallError{Message: err.Error()}`, the
 * transport answers 422 with it, and `@wailsio/runtime` throws `new RuntimeError(json.message)`.
 *
 * So there is nothing to strip — and that is load-bearing rather than merely convenient. Every
 * sentinel in `13-cross-language-contracts.md` that is matched with `startsWith`
 * (`STALE_REVIEW: `, `NOTHING_TO_ANALYZE: `, `CHECKOUT_CONFLICT: ` and the rest) depends on the
 * marker sitting at position 0, trailing space included. This function's whole job is to make sure
 * nothing is added in front of it on the way through.
 */
export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * Calls a backend command.
 *
 * Every command goes through the single bound method `bridge.Service.Invoke`, which takes the
 * command name and its parameter object. See `backend/bridge` for why one method rather than one
 * per command.
 */
export async function invoke<T>(method: string, params?: Record<string, unknown>): Promise<T> {
  try {
    // Cast: the bridge is untyped by design, and each wrapper in `lib/ipc` owns its result type.
    return (await Call.ByName(`${BRIDGE}.Invoke`, method, params ?? {})) as T;
  } catch (error: unknown) {
    // Rethrown rather than mutated: an Error's `message` is writable but its stack already carries
    // the old text, and callers compare messages rather than inspect the instance.
    throw new Error(errorMessage(error), { cause: error });
  }
}

/** Subscribes to a backend event. */
export function listen<T>(event: string, handler: (event: Event<T>) => void): Promise<UnlistenFn> {
  // Go emits with a single data argument, which Wails puts in `CustomEvent.Data` and the runtime
  // surfaces as `ev.data`. The `{ payload }` re-wrap is what keeps all 11 call sites unchanged.
  //
  // Wrapped in an already-resolved promise to keep the signature: the subscription is synchronous,
  // but callers store the promise and `.then` it to unsubscribe.
  return Promise.resolve(Events.On(event, (ev) => handler({ payload: ev.data as T })));
}

async function callHost<T>(method: string, ...args: unknown[]): Promise<T> {
  try {
    // Cast: Call.ByName is untyped (`Promise<any>`) because it resolves a method by string. Each
    // caller below states the shape its HostService method returns, which is the same contract the
    // Go side declares.
    return (await Call.ByName(`${HOST}.${method}`, ...args)) as T;
  } catch (error: unknown) {
    throw new Error(errorMessage(error), { cause: error });
  }
}

/** The subset of the window handle the title bar uses, mapped onto the runtime's own wrapper. */
function currentWindow() {
  return {
    minimize: () => RuntimeWindow.Minimise(),
    toggleMaximize: () => RuntimeWindow.ToggleMaximise(),
    close: () => RuntimeWindow.Close(),
    isMaximized: () => RuntimeWindow.IsMaximised(),
    // 2.x never called this: `nativeTheme.themeSource` was wired up and left unused, because the
    // renderer owns its own theme. Kept as a resolved no-op so `CurrentWindow` keeps its shape.
    setTheme: (_theme: "light" | "dark" | null) => Promise.resolve(),
  };
}

/** Everything the renderer needs that is not a backend command. */
export const host = {
  platform: detectPlatform,
  window: currentWindow,
  dialog: () => ({
    openFile: (options?: unknown) => callHost<string | null>("OpenFile", options ?? {}),
    openDirectory: (options?: unknown) => callHost<string | null>("OpenDirectory", options ?? {}),
    save: (options?: unknown) => callHost<string | null>("SaveFile", options ?? {}),
  }),
  openExternal: (url: string) => callHost<void>("OpenExternal", url),
  clipboardWrite: (text: string) => callHost<void>("ClipboardWrite", text),
  openLogs: () => callHost<void>("OpenLogs"),
  quit: (reason: string) => callHost<void>("Quit", reason),
  sidecarStatus: () =>
    callHost<{ status: "starting" | "ready" | "down"; detail?: string; logsDirectory: string }>(
      "SidecarStatus",
    ),
  /**
   * True when the desktop host is present, for the few places that degrade rather than fail.
   *
   * The Wails runtime installs `window.wails` when it loads, so its absence means a plain browser.
   */
  available: () => typeof window !== "undefined" && "wails" in window,
};
