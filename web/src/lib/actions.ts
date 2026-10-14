// Changing files. Each returns nothing when it worked, or a message saying
// why not (shown in the dialog that asked).

import { api, messageOf, type Entry } from "./api";
import { hideEntry, refreshFolder } from "./folders.svelte";
import { baseName, joinPath, nameProblem, parentOf } from "./paths";
import { toast } from "./toast.svelte";

export async function newFolder(folder: string, name: string): Promise<string | void> {
  const problem = nameProblem(name);
  if (problem) return problem;
  try {
    await api<Entry>("POST", "/api/v1/files/folder", { path: joinPath(folder, name) });
  } catch (error) {
    return messageOf(error);
  }
  refreshFolder();
}

export async function rename(entry: Entry, name: string): Promise<string | void> {
  if (name === entry.name) return;
  const problem = nameProblem(name);
  if (problem) return problem;
  try {
    await api<Entry>("POST", "/api/v1/files/move", { from: entry.path, to: joinPath(parentOf(entry.path), name) });
  } catch (error) {
    return messageOf(error);
  }
  refreshFolder();
}

export async function move(entry: Entry, folder: string): Promise<string | void> {
  try {
    await api<Entry>("POST", "/api/v1/files/move", { from: entry.path, to: joinPath(folder, entry.name) });
  } catch (error) {
    return messageOf(error);
  }
  hideEntry(entry.path);
  refreshFolder();
  toast(`Moved to ${folder === "/" ? "Home" : baseName(folder)}`);
}

/** Deleting is instant and goes to the trash, so there's no "are you
 * sure?": an Undo button appears instead. */
export async function remove(entry: Entry): Promise<string | void> {
  hideEntry(entry.path); // gone at once; back if the server says no
  let trashID: string;
  try {
    ({ trash_id: trashID } = await api<{ trash_id: string }>("DELETE", "/api/v1/files?path=" + encodeURIComponent(entry.path)));
  } catch (error) {
    refreshFolder();
    toast(`Couldn't delete ${entry.name}: ${messageOf(error)}`);
    return messageOf(error);
  }
  refreshFolder();
  toast(`Deleted ${entry.name}`, {
    label: "Undo",
    run: async () => {
      try {
        await api<Entry>("POST", "/api/v1/trash/restore", { id: trashID });
      } catch (error) {
        toast(`Couldn't undo: ${messageOf(error)}`);
      }
      refreshFolder();
    },
  });
}
