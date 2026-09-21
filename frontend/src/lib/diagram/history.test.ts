import { describe, expect, test } from "vitest";
import {
  HISTORY_LIMIT,
  amend,
  canRedo,
  canUndo,
  commit,
  forget,
  initialHistory,
  redo,
  undo,
  type History,
} from "./history";

function states(history: History<string>): string[] {
  return [...history.past, `[${history.present}]`, ...history.future];
}

test("a fresh history has nowhere to go", () => {
  const history = initialHistory("a");

  expect(canUndo(history)).toBe(false);
  expect(canRedo(history)).toBe(false);
});

test("undo and redo walk the same line", () => {
  let history = commit(commit(initialHistory("a"), "b"), "c");
  expect(states(history)).toEqual(["a", "b", "[c]"]);

  history = undo(history);
  expect(states(history)).toEqual(["a", "[b]", "c"]);

  history = undo(history);
  expect(states(history)).toEqual(["[a]", "b", "c"]);

  history = redo(redo(history));
  expect(states(history)).toEqual(["a", "b", "[c]"]);
});

test("stepping off either end changes nothing", () => {
  const start = initialHistory("a");

  expect(undo(start)).toBe(start);
  expect(redo(start)).toBe(start);
});

// The rule every editor follows: once you do something new from a state you undid to, the branch
// you undid is gone. Anything else needs a tree, and a tree needs a UI nobody asked for.
test("a new edit after an undo discards the redo branch", () => {
  const history = commit(undo(commit(commit(initialHistory("a"), "b"), "c")), "d");

  // "c" is gone; "b" stays in the past, because it is the state the new edit was made from and
  // undoing once more has to arrive back at it.
  expect(states(history)).toEqual(["a", "b", "[d]"]);
  expect(canRedo(history)).toBe(false);
});

test("committing the same state is not a step", () => {
  const history = commit(initialHistory("a"), "b");

  expect(commit(history, "b")).toBe(history);
});

// What a drag uses: the present moves, the stack does not grow. Without it a single drag across the
// canvas would bury every earlier step under a hundred frames of itself.
test("amend replaces the present without recording a step", () => {
  const history = amend(amend(commit(initialHistory("a"), "b"), "b1"), "b2");

  expect(states(history)).toEqual(["a", "[b2]"]);
  expect(undo(history).present).toBe("a");
});

test("the stack forgets its oldest step rather than growing without end", () => {
  const extra = 10;
  let history = initialHistory("s0");
  for (let step = 1; step <= HISTORY_LIMIT + extra; step += 1) {
    history = commit(history, `s${step}`);
  }

  // s0…s109 were recorded; the last HISTORY_LIMIT of them are kept, so the oldest reachable is s10.
  expect(history.past).toHaveLength(HISTORY_LIMIT);
  expect(history.past[0]).toBe(`s${extra}`);
  expect(history.present).toBe(`s${HISTORY_LIMIT + extra}`);
});

describe("opening another document", () => {
  test("forget keeps what is on screen and drops where it came from", () => {
    const history = forget(commit(commit(initialHistory("a"), "b"), "c"));

    expect(states(history)).toEqual(["[c]"]);
    expect(canUndo(history)).toBe(false);
  });
});
