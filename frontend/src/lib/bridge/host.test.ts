import { describe, expect, test } from "vitest";
import { errorMessage } from "./host";

/**
 * What a backend failure's message is, by the time a caller sees it.
 *
 * Under Electron this file tested a *subtraction*: `ipcMain.handle` re-threw every rejection as
 * `Error invoking remote method '<channel>': <real message>`, and that prefix reached the screen —
 * a user analysing a clean working tree read `codeflow:invoke` as the explanation.
 *
 * Wails adds nothing. `bindings.go` builds a `CallError{Message: err.Error()}`, the transport
 * answers 422 with it, and `@wailsio/runtime` throws `new RuntimeError(json.message)`. So the test
 * inverts: it now pins that the message passes through *untouched*, because the sentinel prefixes
 * the renderer parses are matched with `startsWith` and anything prepended moves the marker off
 * position 0 — silently turning a handled state back into a red banner.
 */
describe("errorMessage", () => {
  test("passes a backend message through unchanged", () => {
    expect(errorMessage(new Error("the tree is clean"))).toBe("the tree is clean");
  });

  test("keeps every startsWith sentinel at position 0", () => {
    for (const sentinel of [
      "STALE_REVIEW: reviewed abc1234, head is now def5678",
      "CREDENTIAL_REFUSED: the token was rejected",
      "SELF_APPROVAL: you opened this pull request",
      "CHECKOUT_CONFLICT: uncommitted changes",
      "NOTHING_TO_ANALYZE: the working tree is clean",
      "TICKET_NOT_LINKED: no work item is linked",
      "TICKET_SYNC_FAILED: the remote refused",
      "DB_CONNECTION_REFUSED: password authentication failed",
    ]) {
      const message = errorMessage(new Error(sentinel));
      expect(message).toBe(sentinel);
      expect(message.indexOf(sentinel)).toBe(0);
    }
  });

  test("keeps the trailing space that makes a prefix match", () => {
    // "CREDENTIAL_REFUSED:no space here" is deliberately not a match; the space is the contract.
    expect(errorMessage(new Error("CREDENTIAL_REFUSED: "))).toBe("CREDENTIAL_REFUSED: ");
    expect(errorMessage(new Error("CREDENTIAL_REFUSED: ")).startsWith("CREDENTIAL_REFUSED: ")).toBe(true);
    expect(errorMessage(new Error("CREDENTIAL_REFUSED:no space here")).startsWith("CREDENTIAL_REFUSED: ")).toBe(
      false,
    );
  });

  test("keeps the :: markers the renderer matches with includes", () => {
    for (const marker of [
      "QUOTA_EXCEEDED::usage limit reached, resets in 3 hours",
      "AUTH_EXPIRED::run claude auth login",
      "RUN_CANCELLED::",
      "RUN_TIMED_OUT::10",
    ]) {
      expect(errorMessage(new Error(marker))).toBe(marker);
    }
  });

  test("does not edit a message that happens to start with Error:", () => {
    // Under Electron this text had to be corrected for, because the transport had added its own
    // `Error: `. Wails adds none, so these are the backend's own words and trimming them would be
    // editing the message rather than the transport.
    expect(errorMessage(new Error("Error: git said no"))).toBe("Error: git said no");
  });

  test("stringifies a rejection that is not an Error", () => {
    expect(errorMessage("a bare string")).toBe("a bare string");
    expect(errorMessage(42)).toBe("42");
    expect(errorMessage(null)).toBe("null");
    expect(errorMessage(undefined)).toBe("undefined");
  });

  test("keeps an unknown command's message, which eleven deferred commands rely on", () => {
    expect(errorMessage(new Error("unknown command 'debug_attach'"))).toBe("unknown command 'debug_attach'");
  });

  test("keeps a missing-parameter message, which the user is shown verbatim", () => {
    expect(errorMessage(new Error("missing required parameter 'repoPath'"))).toBe(
      "missing required parameter 'repoPath'",
    );
  });
});
