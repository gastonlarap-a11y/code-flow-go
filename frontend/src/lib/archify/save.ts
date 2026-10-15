import * as api from "../ipc/commands";
import { useDiagramStore } from "../../state/diagramStore";
import { useUiStore } from "../../state/uiStore";
import { archifyDocumentPath, archifySlug } from "./documents";

/** How many `-2`, `-3`… a document's name is tried with before giving up. */
const MAX_NAME_ATTEMPTS = 20;

/**
 * Writes an Archify document into the repository as `codemap/<name>-<type>.archify.json`, under a
 * name no document has yet, and answers where (ARCHIFY-005). Never overwrites: the names already
 * taken are read first, and `create_file` itself refuses an existing file.
 */
export async function saveArchifyDocument(rootPath: string, name: string, diagramType: string, document: string): Promise<string> {
  const taken = new Set(await api.diagramListDocuments(rootPath));
  const slug = archifySlug(name);
  for (let attempt = 1; attempt <= MAX_NAME_ATTEMPTS; attempt++) {
    const path = archifyDocumentPath(slug, diagramType, attempt);
    if (taken.has(path)) continue;
    await api.createFile(rootPath, path);
    await api.writeFileText(rootPath, path, document);
    return path;
  }
  throw new Error(`${archifyDocumentPath(slug, diagramType)}: every name up to -${MAX_NAME_ATTEMPTS} is taken`);
}

/** Opens a document in the diagram editor, which loads the folder first and then shows it. */
export function showArchifyDocument(path: string): void {
  useDiagramStore.getState().openWhenShown(path);
  useUiStore.getState().setActiveView("diagram");
}
