/**
 * Telling a cancellation apart from a failure, at the global rejection net.
 *
 * `main.tsx` catches every unhandled rejection and shows it as "Algo falló de forma inesperada",
 * because a backend failure reached through one of the ninety-odd `onClick={asyncFn}` handlers
 * otherwise produced no message anywhere. That net is right, and it was catching one thing it
 * should not: **a cancellation is not a failure**.
 *
 * Monaco cancels its own in-flight work whenever an editor or a model is disposed — tokenization,
 * worker round trips, hover and link providers. Switching between work items tears editors down
 * and builds them up, so every switch could reject a handful of promises that nobody awaits and
 * nobody should: the answer was discarded on purpose. The user saw a toast per switch for
 * something that had gone exactly right.
 *
 * Reported against 3.0.0 and not against 2.7.1, which is a timing difference rather than a new
 * bug: WKWebView schedules the worker messages differently from the bundled Chromium, so the
 * teardown wins a race it used to lose. The rejection was always possible.
 */

/**
 * Monaco's `CancellationError` carries this as both its `name` and its `message`.
 *
 * `VERBATIM`, and matched on `name` rather than `message`: the message is what reaches a user and
 * could be localised one day, while the name is the type's identity. Monaco's own
 * `isCancellationError` does the same comparison.
 */
const monacoCancellation = "Canceled";

/**
 * The Wails runtime's cancellation, for a bound call that was cancelled before it answered.
 *
 * Not reachable through `invoke` today — `lib/bridge/host.ts` never cancels — but it is the same
 * category, and finding out otherwise through a toast that says "unexpected" would waste the same
 * afternoon twice.
 */
const wailsCancellation = "CancelError";

/**
 * True when a rejection means "this was called off", not "this went wrong".
 *
 * Deliberately narrow. It matches two known cancellation types by name and nothing else: a net
 * that swallowed anything resembling the word would hide real failures, which is the opposite of
 * why the net exists.
 */
export function isCancellation(reason: unknown): boolean {
  if (!(reason instanceof Error)) return false;
  return reason.name === monacoCancellation || reason.name === wailsCancellation;
}
