import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { BAND, type TextPlacement } from "../../lib/diagram/stencils";
import { TEXT_INSET } from "../../lib/diagram/text";

/**
 * A shape's text, and the field that edits it in place (DIAG-006).
 *
 * HTML rather than SVG `<text>`, because HTML wraps, centres and edits for free and `<text>` does
 * none of the three. The exporter, which has no HTML, reproduces the same layout by hand in
 * `toSvg.ts` — the two are held together by `textBox`, which both consult.
 */
export function ShapeLabel({
  text,
  placement,
  width,
  height,
  colour,
  editing,
  onCommit,
  onCancel,
}: {
  text: string;
  placement: TextPlacement;
  width: number;
  height: number;
  colour: string;
  editing: boolean;
  onCommit: (text: string) => void;
  onCancel: () => void;
}) {
  if (editing) {
    return <LabelEditor text={text} colour={colour} onCommit={onCommit} onCancel={onCancel} />;
  }
  if (text.trim() === "") return null;

  const common = "pointer-events-none absolute flex items-center justify-center text-center text-body";
  const style = { color: colour, whiteSpace: "pre-wrap" as const, wordBreak: "break-word" as const };

  switch (placement) {
    case "center":
      return (
        <div className={common} style={{ ...style, inset: 0, paddingInline: TEXT_INSET }}>
          {text}
        </div>
      );
    // Under the shape: a person's name, an event's label. The shape is too small to hold it.
    case "below":
      return (
        <div className={common} style={{ ...style, top: height + 4, left: -width / 2, width: width * 2 }}>
          {text}
        </div>
      );
    // A container's title, across the top — which is where Mermaid draws a subgraph's.
    case "band-top":
      return (
        <div
          className={common}
          style={{ ...style, top: 0, left: 0, width, height: BAND, paddingInline: TEXT_INSET }}
        >
          {text}
        </div>
      );
  }
}

/**
 * Typing a label, wherever a label is.
 *
 * A textarea rather than a contentEditable: it already does selection, undo and IME, and none of
 * the three are worth re-implementing on a canvas.
 *
 * Enter commits and Shift+Enter breaks the line, which is the way round people expect in a diagram.
 * Escape abandons; clicking away commits, because losing what you typed by looking elsewhere is not
 * a defensible default.
 *
 * Exported because a connector's label is edited the same way (DIAG-017) and there should be one
 * answer to "what does Escape do here", not two. It fills its parent, so the caller decides where
 * the field sits — inside a shape, or over the middle of an arrow.
 */
export function LabelEditor({
  text,
  colour,
  onCommit,
  onCancel,
}: {
  text: string;
  colour: string;
  onCommit: (text: string) => void;
  onCancel: () => void;
}) {
  const [draft, setDraft] = useState(text);
  const field = useRef<HTMLTextAreaElement>(null);
  /**
   * Set before abandoning, and read by `onBlur`.
   *
   * Escape closes the editor, which unmounts the field, which fires `blur` — and a `blur` that
   * commits would make Escape save exactly the text it was asked to throw away.
   */
  const abandoning = useRef(false);

  useEffect(() => {
    field.current?.focus();
    field.current?.select();
  }, []);

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      abandoning.current = true;
      onCancel();
      return;
    }
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      onCommit(draft);
    }
  };

  return (
    <textarea
      ref={field}
      value={draft}
      onChange={(event) => setDraft(event.target.value)}
      onKeyDown={onKeyDown}
      onPointerDown={(event) => event.stopPropagation()}
      onBlur={() => {
        if (abandoning.current) return;
        onCommit(draft);
      }}
      className="absolute inset-0 resize-none bg-transparent text-center text-body outline-none"
      style={{ color: colour, padding: TEXT_INSET / 2 }}
    />
  );
}
