import { writeTerminal } from "../ipc/commands";
import { useTerminalStore } from "../../state/terminalStore";
import { useUiStore } from "../../state/uiStore";
import { useWorkspaceStore } from "../../state/workspaceStore";

/**
 * Opens a terminal in the open project and types a command into it — without running it: the person
 * reads what is about to run and presses Enter (REQ-004). Answers false when no project is open,
 * since a terminal opens in one.
 *
 * Settings closes on the way, because it covers the panel the terminal opens in.
 */
export async function typeInTerminal(command: string): Promise<boolean> {
  const project = useWorkspaceStore.getState().activeProject();
  if (!project) return false;
  await useTerminalStore.getState().openNew(project.id, project.local_path);
  const id = useTerminalStore.getState().byProject[project.id]?.focusedId;
  if (!id) return false;
  useUiStore.getState().closeSettings();
  await writeTerminal(id, command);
  return true;
}
