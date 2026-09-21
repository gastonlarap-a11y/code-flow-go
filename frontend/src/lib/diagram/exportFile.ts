import { save } from "../bridge/dialog";
import { saveTextFile, writeFileBytes } from "../ipc/commands";
import { documentToSvg } from "./toSvg";
import { svgToPng } from "./toPng";
import { serializeDocument } from "./serialize";
import type { DiagramDocument } from "./model";

/**
 * Writing a diagram out of the app (DIAG-010).
 *
 * Three formats and **no new backend command**: the native save dialog belongs to the shell, and
 * the write is `write_file_bytes`, which exists precisely for this — the chosen path is by
 * definition anywhere, and only the dialog authorises writing there. The same pair the schema
 * designer's export uses.
 *
 * Each returns the path written, or `null` when the dialog was dismissed.
 */
export type ExportFormat = "png" | "svg" | "json";

export const EXPORT_SUFFIX: Record<ExportFormat, string> = {
  png: ".png",
  svg: ".svg",
  json: ".diagram.json",
};

export async function exportDiagram(
  doc: DiagramDocument,
  baseName: string,
  format: ExportFormat,
): Promise<string | null> {
  const defaultPath = `${baseName}${EXPORT_SUFFIX[format]}`;

  if (format === "json") return saveTextFile(defaultPath, serializeDocument(doc));
  if (format === "svg") return saveTextFile(defaultPath, documentToSvg(doc));

  // PNG is the only one that is not text, so it goes through the dialog and the byte write by
  // hand rather than through `saveTextFile`.
  const bytes = await svgToPng(documentToSvg(doc));
  const path = await save({ defaultPath });
  if (path === null) return null;

  await writeFileBytes(path, bytes);
  return path;
}
