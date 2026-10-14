// Paths, links and formatting.

import type { Entry } from "./api";

/** Encodes each part of a path on its own, keeping the slashes readable. */
export function encodePath(path: string): string {
  return path.split("/").map(encodeURIComponent).join("/");
}

export function parentOf(path: string): string {
  return path.slice(0, path.lastIndexOf("/")) || "/";
}

export function joinPath(folder: string, name: string): string {
  return (folder === "/" ? "" : folder) + "/" + name;
}

export function baseName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}

/** The folders from the top down to path: [{name: "Home", path: "/"}, ...]. */
export function crumbs(path: string): { name: string; path: string }[] {
  const out = [{ name: "Home", path: "/" }];
  let sofar = "";
  for (const part of path.split("/").filter(Boolean)) {
    sofar += "/" + part;
    out.push({ name: part, path: sofar });
  }
  return out;
}

export function contentURL(path: string, inline = false): string {
  return "/api/v1/files/content?path=" + encodeURIComponent(path) + (inline ? "&inline=1" : "");
}

export type Kind = "folder" | "image" | "video" | "audio" | "pdf" | "text" | "archive" | "file";

export function kindOf(entry: Pick<Entry, "type" | "content_type">): Kind {
  if (entry.type === "folder") return "folder";
  const type = entry.content_type ?? "";
  if (type.startsWith("image/")) return "image";
  if (type.startsWith("video/")) return "video";
  if (type.startsWith("audio/")) return "audio";
  if (type === "application/pdf") return "pdf";
  if (type.startsWith("text/") || type === "application/json") return "text";
  if (/zip|x-7z|x-rar|x-tar|gzip/.test(type)) return "archive";
  return "file";
}

/** Images the browser can show in the in-app viewer. */
export function viewable(entry: Entry): boolean {
  return kindOf(entry) === "image" && !/heic|heif|tiff/.test(entry.content_type ?? "");
}

export function formatSize(bytes: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = bytes;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit++;
  }
  return (unit === 0 ? String(size) : size.toFixed(size < 10 ? 1 : 0)) + " " + units[unit];
}

// Some browser language settings (e.g. "en-US@posix") make Intl throw;
// that must never break the app.
function intl<T>(make: (locales?: readonly string[]) => T): T {
  try {
    return make(navigator.languages);
  } catch {
    return make(undefined);
  }
}

const dayMonth = intl((l) => new Intl.DateTimeFormat(l, { day: "numeric", month: "short" }));
const dayMonthYear = intl((l) => new Intl.DateTimeFormat(l, { day: "numeric", month: "short", year: "numeric" }));
const timeOfDay = intl((l) => new Intl.DateTimeFormat(l, { hour: "numeric", minute: "2-digit" }));

/** "Today 14:05", "Yesterday", "12 Oct", "12 Oct 2024". */
export function formatDate(iso: string, now = new Date()): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const day = 24 * 60 * 60 * 1000;
  if (date.getTime() >= startOfToday && date.getTime() < startOfToday + day) return "Today " + timeOfDay.format(date);
  if (date.getTime() >= startOfToday - day && date.getTime() < startOfToday) return "Yesterday";
  return date.getFullYear() === now.getFullYear() ? dayMonth.format(date) : dayMonthYear.format(date);
}

/** "45 s", "3 min", "1.5 h". */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "";
  if (seconds < 60) return `${Math.max(1, Math.ceil(seconds))} s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)} min`;
  return `${(seconds / 3600).toFixed(1)} h`;
}

/** Video clock: "1:05" or "1:02:05". */
export function formatClock(seconds: number): string {
  if (!Number.isFinite(seconds)) return "0:00";
  const s = Math.floor(seconds % 60).toString().padStart(2, "0");
  const m = Math.floor(seconds / 60) % 60;
  const h = Math.floor(seconds / 3600);
  return h > 0 ? `${h}:${m.toString().padStart(2, "0")}:${s}` : `${m}:${s}`;
}

/** Problems Windows has with a name, checked before asking the server. */
export function nameProblem(name: string): string | null {
  if (!name.trim()) return "Type a name";
  if (/[<>:"/\\|?*\u0000-\u001f]/.test(name)) return 'A name can\'t contain < > : " / \\ | ? *';
  if (/[. ]$/.test(name)) return "A name can't end with a dot or a space";
  if (name === "." || name === "..") return "That name isn't allowed";
  return null;
}
