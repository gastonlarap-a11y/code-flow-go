import { listen, type UnlistenFn } from "./host";

/**
 * Drag-and-drop file paths, rebuilt on DOM events plus one native event.
 *
 * This is the one bypass whose *mechanism* changes rather than its wiring, and it has now changed
 * twice. A `File` in a webview carries no filesystem path: Electron's preload recovered it with
 * `webUtils.getPathForFile`, and Wails instead reports the paths natively, through
 * `WindowFilesDropped`, which Go re-emits as `codeflow:files-dropped`.
 *
 * So enter/over/leave stay DOM-driven — they only need pointer position and depth counting — while
 * `drop` waits for the native event to supply the paths. The payload shape is reproduced exactly,
 * so `ImportModal` keeps its logic.
 *
 * Wails only reports a drop onto an element carrying `data-file-drop-target`, which is why the
 * attribute goes on `ImportModal`'s full-screen backdrop: the modal accepts a drop anywhere while
 * it is open, with no hit-testing, exactly as it always has.
 *
 * Listeners are attached to the document rather than to a specific element for the same reason.
 */

export type DragDropPayload =
  | { type: "enter"; paths: string[]; position: { x: number; y: number } }
  | { type: "over"; position: { x: number; y: number } }
  | { type: "drop"; paths: string[]; position: { x: number; y: number } }
  | { type: "leave" };

export interface DragDropEvent {
  payload: DragDropPayload;
}

interface CurrentWebview {
  onDragDropEvent(handler: (event: DragDropEvent) => void): Promise<UnlistenFn>;
}

export function getCurrentWebview(): CurrentWebview {
  return {
    onDragDropEvent(handler) {
      // `dragenter` and `dragleave` fire for every element the pointer crosses, so a naive
      // handler flickers the drop target. Counting depth is what makes "leave" mean "left the
      // window" rather than "moved between two children".
      let depth = 0;

      // The last pointer position seen, so the native drop event — which carries paths but no
      // coordinates — can report where the drop landed.
      let lastPosition = { x: 0, y: 0 };

      const position = (event: DragEvent) => ({ x: event.clientX, y: event.clientY });

      const onEnter = (event: DragEvent) => {
        event.preventDefault();
        depth += 1;
        if (depth === 1) handler({ payload: { type: "enter", paths: [], position: position(event) } });
      };

      const onOver = (event: DragEvent) => {
        // Without this the browser treats the drop as a navigation and opens the file.
        event.preventDefault();
        lastPosition = position(event);
        handler({ payload: { type: "over", position: lastPosition } });
      };

      const onLeave = (event: DragEvent) => {
        event.preventDefault();
        depth = Math.max(0, depth - 1);
        if (depth === 0) handler({ payload: { type: "leave" } });
      };

      // The DOM drop only resets the depth counter; the paths arrive from Go. Both fire for the
      // same gesture, and neither is guaranteed to be first, so the two are kept independent
      // rather than one waiting on the other.
      const onDrop = (event: DragEvent) => {
        event.preventDefault();
        depth = 0;
        lastPosition = position(event);
      };

      document.addEventListener("dragenter", onEnter);
      document.addEventListener("dragover", onOver);
      document.addEventListener("dragleave", onLeave);
      document.addEventListener("drop", onDrop);

      const dropped = listen<{ paths: string[] }>("codeflow:files-dropped", (event) => {
        depth = 0;
        handler({ payload: { type: "drop", paths: event.payload.paths ?? [], position: lastPosition } });
      });

      return dropped.then((unlistenDropped) => () => {
        unlistenDropped();
        document.removeEventListener("dragenter", onEnter);
        document.removeEventListener("dragover", onOver);
        document.removeEventListener("dragleave", onLeave);
        document.removeEventListener("drop", onDrop);
      });
    },
  };
}
