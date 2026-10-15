import { useEffect, useState } from "react";

/** One question's answer as the panel shows it. */
export type Answer<T> =
  | { state: "loading" }
  | { state: "done"; data: T }
  | { state: "failed"; error: string };

/**
 * Asks once per `key` and answers what came back — never the answer to an earlier key, even for the
 * one render between the key changing and the new question going out.
 */
export function useAnswer<T>(key: string, ask: () => Promise<T>): Answer<T> {
  const [held, setHeld] = useState<{ key: string; answer: Answer<T> } | null>(null);

  useEffect(() => {
    let current = true;
    ask().then(
      (data) => current && setHeld({ key, answer: { state: "done", data } }),
      (error: unknown) =>
        current && setHeld({ key, answer: { state: "failed", error: error instanceof Error ? error.message : String(error) } }),
    );
    return () => {
      current = false;
    };
    // `ask` closes over what `key` already names, so the key alone decides when to ask again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  return held?.key === key ? held.answer : { state: "loading" };
}
