// Folder listings. Folders you've opened are remembered, so going back shows
// them at once; each is still reloaded from the laptop in the background.

import { api, ApiError, messageOf, NetworkError, type Entry, type Listing } from "./api";

const remembered = new Map<string, Entry[]>();

export const folder = $state({
  path: "/",
  entries: [] as Entry[],
  loaded: false, // entries are for path (possibly remembered, being refreshed)
  loading: false,
  error: "",
  offline: false,
});

let latest = 0;
let inFlight: { path: string; done: Promise<void> } | null = null;

/** Shows the folder at path, loading it from the laptop. Opening the
 * folder that's already loading waits for that request instead of sending
 * another; fresh asks again regardless (after a change, an older request
 * could miss it). */
export function openFolder(path: string, { quiet = false, fresh = false } = {}): Promise<void> {
  if (!fresh && inFlight?.path === path) return inFlight.done;
  const done = load(path, quiet).finally(() => {
    if (inFlight?.done === done) inFlight = null;
  });
  inFlight = { path, done };
  return done;
}

async function load(path: string, quiet: boolean) {
  const request = ++latest;
  if (folder.path !== path) {
    folder.path = path;
    const known = remembered.get(path);
    folder.entries = known ?? [];
    folder.loaded = known !== undefined;
  }
  if (!quiet) folder.loading = true;
  try {
    const listing = await api<Listing>("GET", "/api/v1/files?path=" + encodeURIComponent(path));
    if (request !== latest) return;
    remembered.set(path, listing.entries);
    folder.entries = listing.entries;
    folder.loaded = true;
    folder.error = "";
    folder.offline = false;
  } catch (error) {
    if (request !== latest) return;
    folder.offline = error instanceof NetworkError;
    folder.error = error instanceof ApiError && error.status === 401 ? "" : messageOf(error);
  } finally {
    if (request === latest) folder.loading = false;
  }
}

/** Reloads the folder on screen (after an upload, rename, delete…). */
export function refreshFolder() {
  return openFolder(folder.path, { quiet: true, fresh: true });
}

/** Forgets remembered listings, e.g. after logging out. */
export function forgetFolders() {
  remembered.clear();
  folder.entries = [];
  folder.loaded = false;
}

/** Takes an entry off the list at once (the server is told separately). */
export function hideEntry(path: string) {
  folder.entries = folder.entries.filter((entry) => entry.path !== path);
}
