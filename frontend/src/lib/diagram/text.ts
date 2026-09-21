/**
 * Breaking a shape's label into lines (DIAG-010).
 *
 * The canvas does not need this — HTML wraps text by itself, and the node is an HTML box. The
 * exporter does: SVG `<text>` has no wrapping at all, so an exported diagram would show every
 * label as one long line running out of its shape and across the next one.
 *
 * `<foreignObject>` would wrap natively and is the wrong answer twice over: half the tools people
 * open an SVG in ignore it, and rasterising one to PNG through a canvas is blocked or blank
 * depending on the engine. A measured approximation that works everywhere beats a perfect result
 * that works in one viewer.
 *
 * So widths are estimated rather than measured, and the estimate is deliberately slightly wide: a
 * label that breaks one word earlier than the canvas does is unremarkable, and one that overflows
 * its shape is the thing being avoided.
 */

/**
 * Average glyph width as a fraction of the font size, for the UI font.
 *
 * Inter's lowercase average sits near 0.5em and its uppercase nearer 0.62em; 0.55 is between them
 * and errs wide on ordinary sentence-case labels, which is the direction that does no harm.
 */
export const GLYPH_RATIO = 0.55;

/** How much of a shape's width the text may use, leaving it a margin on both sides. */
export const TEXT_INSET = 12;

export function estimateWidth(text: string, fontSize: number): number {
  return text.length * fontSize * GLYPH_RATIO;
}

/**
 * Splits a label into lines that fit `width`.
 *
 * Wraps on spaces, and breaks inside a word only when the word alone cannot fit — a long
 * identifier in a narrow box is common in this app, and leaving it to overflow would be worse than
 * splitting it.
 *
 * Explicit newlines in the label are honoured: a person who pressed Enter meant it.
 */
export function wrapText(text: string, width: number, fontSize: number): string[] {
  const usable = Math.max(fontSize, width);
  const lines: string[] = [];

  for (const paragraph of text.split("\n")) {
    const words = paragraph.split(/\s+/).filter((word) => word.length > 0);
    if (words.length === 0) {
      lines.push("");
      continue;
    }

    let current = "";
    for (const word of words) {
      const candidate = current === "" ? word : `${current} ${word}`;
      if (estimateWidth(candidate, fontSize) <= usable) {
        current = candidate;
        continue;
      }

      if (current !== "") lines.push(current);
      if (estimateWidth(word, fontSize) <= usable) {
        current = word;
        continue;
      }

      // A single word too long for the box: break it, and carry the tail on.
      const pieces = breakWord(word, usable, fontSize);
      lines.push(...pieces.slice(0, -1));
      current = pieces[pieces.length - 1] ?? "";
    }
    if (current !== "") lines.push(current);
  }

  // A label that is only whitespace is no label; an empty array renders nothing at all.
  return lines.length === 1 && lines[0] === "" ? [] : lines;
}

function breakWord(word: string, width: number, fontSize: number): string[] {
  const perLine = Math.max(1, Math.floor(width / (fontSize * GLYPH_RATIO)));
  const pieces: string[] = [];
  for (let at = 0; at < word.length; at += perLine) {
    pieces.push(word.slice(at, at + perLine));
  }
  return pieces;
}

/**
 * Escapes text for an XML attribute or text node.
 *
 * The exporter builds a string, so a label containing `<` or `&` — "A & B", "<draft>" — would
 * otherwise produce a file no parser will open. There is no DOM in the export path to do this.
 */
export function escapeXml(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&apos;");
}
