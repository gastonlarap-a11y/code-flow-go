/**
 * The same picture, rasterised (DIAG-010).
 *
 * Deliberately the thinnest thing in this folder, because it is the only part of the export that
 * cannot be tested without a browser: everything that decides what the image *contains* is
 * `toSvg.ts`, which is pure. This turns a finished SVG into pixels and nothing else.
 *
 * **The intermediate image has to be a `data:` URL.** The production CSP (`vite.config.ts`) allows
 * `img-src 'self' data:` and does not allow `blob:`, so the obvious `URL.createObjectURL(blob)`
 * works in `pnpm dev` — where no policy is injected — and fails in the packaged app, which is the
 * worst possible place to find out.
 */

/** Drawn at twice the size, so the image is not soft on the displays this app runs on. */
export const PNG_SCALE = 2;

/**
 * Rasterises an SVG document into PNG bytes.
 *
 * Rejects rather than resolving empty when the image cannot be decoded: a zero-byte file written
 * to the path the user chose would look like a successful export.
 */
export async function svgToPng(svg: string, scale = PNG_SCALE): Promise<Uint8Array> {
  const size = measure(svg);
  const image = await decode(svg);

  const canvas = document.createElement("canvas");
  canvas.width = Math.max(1, Math.round(size.width * scale));
  canvas.height = Math.max(1, Math.round(size.height * scale));

  const context = canvas.getContext("2d");
  if (context === null) throw new Error("this webview cannot rasterise an image");
  context.drawImage(image, 0, 0, canvas.width, canvas.height);

  const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/png"));
  if (blob === null) throw new Error("the image could not be encoded");

  return new Uint8Array(await blob.arrayBuffer());
}

function decode(svg: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const image = new Image();
    image.addEventListener("load", () => resolve(image));
    image.addEventListener("error", () => reject(new Error("the diagram could not be drawn")));
    // `encodeURIComponent` rather than `btoa`: the labels are the user's own text and may hold
    // anything, and `btoa` throws on the first character outside Latin-1.
    image.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
  });
}

/**
 * The size the SVG declares.
 *
 * Read back out of the string rather than passed in beside it, so there is one answer to how big
 * the picture is and it is the one written into the file.
 */
function measure(svg: string): { width: number; height: number } {
  const width = Number(/\swidth="(\d+(?:\.\d+)?)"/.exec(svg)?.[1] ?? "0");
  const height = Number(/\sheight="(\d+(?:\.\d+)?)"/.exec(svg)?.[1] ?? "0");
  return { width: width || 1, height: height || 1 };
}
