import { useState } from "react";
import { Workflow } from "lucide-react";
import { Modal } from "../common/Modal";
import { Button } from "../common/Button";
import { useT } from "../../state/languageStore";
import { confirmAction } from "../../state/confirmStore";
import { DOCUMENT_EXTENSION, useDiagramStore } from "../../state/diagramStore";
import type { DiagramDocument } from "../../lib/diagram/model";
import type { TranslationKey } from "../../lib/i18n/translations";

/** Why the name was refused, as the key that says so in the user's language. */
const ERROR_KEYS = {
  empty: "diagram.error.empty",
  escapes: "diagram.error.escapes",
  absolute: "diagram.error.absolute",
  invalidChar: "diagram.error.invalidChar",
  exists: "diagram.error.exists",
} as const satisfies Record<string, TranslationKey>;

/**
 * Names a new diagram and creates it.
 *
 * The validation is `lib/documentPath.ts` through the store — the same rules the schema designer
 * uses, with its own extension. What is left here is showing the refusal under the field.
 *
 * `contents` is what an import hands over: the same dialog names the file either way, so importing
 * cannot overwrite the document that is open.
 */
export function NewDiagramModal({
  rootPath,
  contents,
  onClose,
}: {
  rootPath: string;
  contents?: DiagramDocument;
  onClose: () => void;
}) {
  const t = useT();
  const createDocument = useDiagramStore((s) => s.createDocument);
  const dirty = useDiagramStore((s) => s.dirty);
  const [name, setName] = useState("");
  const [error, setError] = useState<keyof typeof ERROR_KEYS | null>(null);
  const [creating, setCreating] = useState(false);

  const submit = async () => {
    // Creating replaces whatever is open, so it is a document switch and asks the question a
    // switch asks (DIAG-009). Without this the work on the open diagram goes silently.
    if (dirty && !(await confirmAction(t("diagram.discardConfirm"), true, t("diagram.discard")))) {
      return;
    }

    setCreating(true);
    const failure = await createDocument(rootPath, name, contents);
    setCreating(false);
    if (failure === null) {
      onClose();
      return;
    }
    setError(failure);
  };

  return (
    <Modal
      title={contents === undefined ? "diagram.newDocumentTitle" : "diagram.importTitle"}
      icon={Workflow}
      size="sm"
      onClose={onClose}
    >
      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <label className="flex flex-col gap-1.5">
          <span className="text-relaxed text-[var(--cf-text)]">{t("diagram.nameLabel")}</span>
          <input
            autoFocus
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              // Clears on edit: a refusal that outlives the thing it refused reads as the field
              // still being wrong.
              setError(null);
            }}
            placeholder={t("diagram.namePlaceholder")}
            className="cf-focusable w-full rounded-control border border-[var(--cf-border)] bg-[var(--cf-bg)] px-2 py-1.5 text-body text-[var(--cf-text)] outline-none"
          />
          {/* The extension is appended when missing, so the hint is not a rule to obey. The folder
              half of it is: a name with a `/` in it has always created the folder, and nothing
              said so. */}
          <span className="text-badge text-[var(--cf-text-muted)]">
            {DOCUMENT_EXTENSION} · {t("diagram.nameFolderHint")}
          </span>
        </label>

        {error !== null && (
          <p role="alert" className="text-body text-[var(--cf-danger)]">
            {t(ERROR_KEYS[error])}
          </p>
        )}

        <div className="flex justify-end gap-2">
          <Button variant="secondary" type="button" onClick={onClose}>
            {t("diagram.cancel")}
          </Button>
          <Button variant="primary" type="submit" pending={creating}>
            {t("diagram.create")}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
