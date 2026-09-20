import { describe, expect, it } from "vitest";

import { isCancellation } from "./cancellation";

describe("isCancellation", () => {
  it("recognises Monaco's cancellation, which is what switching work items raised", () => {
    // Monaco's own shape: `CancellationError` sets name and message to the same literal.
    const monaco = new Error("Canceled");
    monaco.name = "Canceled";

    expect(isCancellation(monaco)).toBe(true);
  });

  it("recognises the Wails runtime's cancelled call", () => {
    const wails = new Error("Promise cancelled.");
    wails.name = "CancelError";

    expect(isCancellation(wails)).toBe(true);
  });

  // The whole point of the net is that a real failure reaches the user. Matching loosely — on the
  // message, or on anything containing the word — would hide exactly what it exists to show.
  it("does not swallow a failure that merely mentions cancelling", () => {
    for (const reason of [
      new Error("the run was cancelled by the server"),
      new Error("Canceled"), // the message alone, with the default name
      new Error("RUN_CANCELLED:: the user stopped it"),
      new Error("DB_CONNECTION_REFUSED: Login failed for user 'sa'."),
    ]) {
      expect(isCancellation(reason)).toBe(false);
    }
  });

  it("is safe with whatever a rejection actually carries", () => {
    for (const reason of [undefined, null, "Canceled", 0, {}, { name: "Canceled" }]) {
      expect(isCancellation(reason)).toBe(false);
    }
  });
});
