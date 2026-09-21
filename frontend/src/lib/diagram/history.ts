/**
 * Undo and redo, as a value (DIAG-008).
 *
 * A drawing tool without undo is one people are afraid to use, and the schema designer has none
 * because its text buffer gets Monaco's. A canvas has no buffer, so this is the buffer.
 *
 * Whole snapshots rather than a list of inverse operations. A document is a few hundred small
 * objects, a hundred of them is nothing next to Monaco sitting in the same window, and the
 * alternative — an `undo` per kind of edit — is where undo bugs come from: every new edit is a new
 * inverse to get right, and the one nobody wrote is discovered by losing work.
 *
 * Generic because the property worth testing is about the stack and not about diagrams.
 */

/** How many steps back it is possible to go. Beyond this the oldest is forgotten. */
export const HISTORY_LIMIT = 100;

export interface History<T> {
  past: readonly T[];
  present: T;
  future: readonly T[];
}

export function initialHistory<T>(present: T): History<T> {
  return { past: [], present, future: [] };
}

/**
 * Records a new state.
 *
 * Redo is discarded, which is the standard rule and the right one: once you branch off a state you
 * undid to, the future you undid is no longer reachable from where you are.
 */
export function commit<T>(history: History<T>, next: T): History<T> {
  if (next === history.present) return history;

  const past = [...history.past, history.present];
  return {
    past: past.length > HISTORY_LIMIT ? past.slice(past.length - HISTORY_LIMIT) : past,
    present: next,
    future: [],
  };
}

/**
 * Replaces the current state without recording a step.
 *
 * For the continuous half of a gesture — every frame of a drag, every pixel of a resize — where
 * `commit` would fill the stack with a hundred states nobody wants to step back through. The
 * gesture commits once, when it ends.
 */
export function amend<T>(history: History<T>, next: T): History<T> {
  return next === history.present ? history : { ...history, present: next };
}

export function canUndo<T>(history: History<T>): boolean {
  return history.past.length > 0;
}

export function canRedo<T>(history: History<T>): boolean {
  return history.future.length > 0;
}

export function undo<T>(history: History<T>): History<T> {
  const previous = history.past.at(-1);
  if (previous === undefined) return history;

  return {
    past: history.past.slice(0, -1),
    present: previous,
    future: [history.present, ...history.future],
  };
}

export function redo<T>(history: History<T>): History<T> {
  const [next, ...rest] = history.future;
  if (next === undefined) return history;

  return { past: [...history.past, history.present], present: next, future: rest };
}

/** Forgets everything but the current state — for opening a different document. */
export function forget<T>(history: History<T>): History<T> {
  return initialHistory(history.present);
}
