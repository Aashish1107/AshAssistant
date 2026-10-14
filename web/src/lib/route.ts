// Reading the address after # into a screen. See router.svelte.ts.

export type Route =
  | { view: "folder"; path: string }
  | { view: "player"; path: string }
  | { view: "viewer"; path: string }
  | { view: "share" };

export function parseRoute(hash: string): Route {
  let value = hash.replace(/^#/, "");
  try {
    value = decodeURIComponent(value);
  } catch {
    // a malformed address: treat it literally
  }
  if (value === "share") return { view: "share" };
  if (value.startsWith("play=")) return { view: "player", path: value.slice(5) };
  if (value.startsWith("view=")) return { view: "viewer", path: value.slice(5) };
  return { view: "folder", path: value.startsWith("/") ? value.replace(/\/+$/, "") || "/" : "/" };
}
