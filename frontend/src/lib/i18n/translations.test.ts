import { describe, expect, test } from "vitest";
import { translations, type TranslationKey } from "./translations";

/**
 * What the compiler cannot check about the translation table.
 *
 * `TranslationKey` guarantees a key exists in English; it says nothing about Spanish, and nothing at
 * all about the placeholders inside a string. This file covers the gap, and it exists because of a
 * real bug: a string written with `{{time}}` rendered as `Se reinicia en {4 h 58 min}` — the braces
 * on screen, because this app interpolates single braces. TypeScript was perfectly happy.
 */

const english = translations.en;
const spanish = translations.es;
const keys = Object.keys(english) as TranslationKey[];

/** Every `{name}` in a string. Doubled braces are matched too, which is the mistake being caught. */
function placeholders(text: string): string[] {
  return [...text.matchAll(/\{+\s*(\w+)\s*\}+/g)].map((match) => match[0]);
}

test("every English key has a Spanish translation", () => {
  const missing = keys.filter((key) => spanish[key] === undefined);

  expect(missing).toEqual([]);
});

test("no Spanish key is orphaned by an English one that was removed", () => {
  const extra = Object.keys(spanish).filter((key) => english[key as TranslationKey] === undefined);

  expect(extra).toEqual([]);
});

describe("placeholders", () => {
  // The app substitutes `{name}`. A doubled brace is another library's syntax and renders literally.
  test("use single braces, because that is what the substitution understands", () => {
    const doubled: string[] = [];

    for (const [language, table] of [["en", english], ["es", spanish]] as const) {
      for (const [key, text] of Object.entries(table)) {
        if (/\{\{|\}\}/.test(text)) doubled.push(`${language}:${key}`);
      }
    }

    expect(doubled).toEqual([]);
  });

  // A translation that names a different placeholder than its English original is a blank on
  // screen for exactly the users who read that language.
  test("match between the two languages", () => {
    const mismatched: string[] = [];

    for (const key of keys) {
      const from = placeholders(english[key]).sort();
      const to = placeholders(spanish[key] ?? "").sort();
      if (from.join() !== to.join()) mismatched.push(key);
    }

    expect(mismatched).toEqual([]);
  });
});
