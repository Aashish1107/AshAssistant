// Uploads. Each file goes up in one request, straight from the phone's
// storage to the laptop's disk, three files at a time. If the connection
// drops (walking out of Wi-Fi range, switching to mobile data), the upload
// waits for the network, asks the server how much arrived and sends only the
// rest. Each upload's ID comes from the folder and the file's name, size and
// date, so after the app was closed or the phone restarted, picking the same
// file again also continues where it stopped.

import { api, ApiError, messageOf, NetworkError, type Entry, type UploadStatus } from "./api";
import { formatSize, joinPath } from "./paths";

const PARALLEL_UPLOADS = 3;
// Files at least this big first ask where to continue, and whether they
// fit; smaller ones just start (an extra round trip would cost more than
// resending a few megabytes).
const ASK_FIRST_SIZE = 8 << 20;
// A request that sends nothing for this long is assumed dead and retried.
const STALL_MS = 30_000;
// Waits between retries after network trouble, in seconds.
const RETRY_DELAYS = [1, 2, 4, 8, 15, 30];

export type UploadState = "waiting" | "sending" | "retrying" | "done" | "failed" | "cancelled";

export class Upload {
  state = $state<UploadState>("waiting");
  sent = $state(0); // bytes the server has (or is receiving)
  message = $state(""); // why it's waiting or failed
  saved = $state<Entry | null>(null); // the finished file
  seconds = 0; // how long it took
  started = 0;
  xhr: XMLHttpRequest | null = null;

  constructor(
    readonly id: string,
    readonly file: File,
    readonly path: string,
    readonly folder: string,
  ) {}

  get active() {
    return this.state === "waiting" || this.state === "sending" || this.state === "retrying";
  }

  /** Read fresh each time: it can change while an upload awaits the network. */
  get cancelled() {
    return this.state === "cancelled";
  }

  url() {
    return `/api/v1/uploads/${this.id}?path=${encodeURIComponent(this.path)}&size=${this.file.size}&modified=${this.file.lastModified || 0}`;
  }
}

/** Fails an upload for good (as opposed to network trouble, which retries). */
class UploadFailure extends Error {}

export const uploads = $state({
  items: [] as Upload[],
  speed: 0, // bytes per second, all uploads together
  open: false, // the upload list is showing
});

// Called whenever an upload finishes, with its folder.
let onFinished: (folder: string) => void = () => {};
export function whenUploaded(callback: (folder: string) => void) {
  onFinished = callback;
}

/** uploadID derives an upload's ID from what identifies the file, so the
 * same file to the same folder always gets the same ID. */
export async function uploadID(path: string, file: Pick<File, "size" | "lastModified">): Promise<string> {
  const text = `ashcloud-upload-v1\n${path}\n${file.size}\n${file.lastModified}`;
  let bytes: Uint8Array;
  if (crypto.subtle) {
    bytes = new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text))).slice(0, 16);
  } else {
    // Plain http (not through Tailscale): no hashing available, so an
    // upload can't be found again after the app is closed.
    bytes = crypto.getRandomValues(new Uint8Array(16));
  }
  return btoa(String.fromCharCode(...bytes)).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}

// ---- the queue ---------------------------------------------------------------

let running = 0;

export async function addUploads(files: Iterable<File>, folder: string) {
  for (const file of files) {
    const path = joinPath(folder, file.name);
    const id = await uploadID(path, file);
    if (uploads.items.some((u) => u.id === id && u.active)) continue; // already on its way
    uploads.items.push(new Upload(id, file, path, folder));
  }
  pump();
}

function pump() {
  for (const item of uploads.items) {
    if (running >= PARALLEL_UPLOADS) break;
    if (item.state !== "waiting") continue;
    running++;
    item.state = "sending";
    item.started = performance.now();
    send(item)
      .catch((error) => fail(item, error instanceof UploadFailure ? error.message : `Upload failed: ${messageOf(error)}`))
      .finally(() => {
        running--;
        pump();
      });
  }
  keepAwake(running > 0);
  measure(running > 0);
}

// send runs one upload to the end: done, failed or cancelled.
async function send(item: Upload) {
  let offset = 0;
  let ask = item.file.size >= ASK_FIRST_SIZE;
  let attempt = 0;
  for (;;) {
    if (item.cancelled) return;
    try {
      if (ask) {
        const status = await api<UploadStatus>("POST", item.url());
        if (status.file) return done(item, status.file);
        offset = status.offset;
        ask = false;
      }
      item.state = "sending";
      item.message = "";
      const result = await put(item, offset);
      attempt = 0;
      const code = result.data?.error?.code;
      if (result.status === 201 || result.data?.file) return done(item, result.data.file);
      if (result.status === 200) {
        offset = result.data.offset;
        continue;
      }
      if (code === "wrong_offset") {
        offset = Number(result.offset);
        continue;
      }
      if (code === "upload_not_found") {
        offset = 0;
        continue;
      }
      throw new ApiError(result.status, code ?? "http_" + result.status, result.data?.error?.message ?? `HTTP ${result.status}`);
    } catch (error) {
      if (item.cancelled) return;
      if (!retryable(error)) {
        if (error instanceof ApiError && error.code === "taken_over") throw new UploadFailure("Continuing in another tab or window");
        if (error instanceof ApiError && error.status === 401) throw new UploadFailure("Log in again, then tap Retry");
        throw new UploadFailure(messageOf(error));
      }
      // Network trouble. Wait, then ask the server where to continue.
      await waitToRetry(item, RETRY_DELAYS[Math.min(attempt, RETRY_DELAYS.length - 1)]);
      attempt++;
      ask = true;
    }
  }
}

function retryable(error: unknown): boolean {
  if (error instanceof NetworkError || error instanceof TypeError) return true;
  if (error instanceof ApiError) return error.code === "interrupted" || (error.status >= 500 && error.status !== 507);
  return false;
}

interface PutResult {
  status: number;
  data: any;
  offset: string | null;
}

// put sends the file from offset on, reporting progress as it goes.
function put(item: Upload, offset: number): Promise<PutResult> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    item.xhr = xhr;
    item.sent = offset;
    let lastProgress = performance.now();
    const watchdog = setInterval(() => {
      if (performance.now() - lastProgress > STALL_MS) xhr.abort();
    }, 5000);
    const finish = () => {
      clearInterval(watchdog);
      item.xhr = null;
    };

    xhr.open("PUT", item.url());
    xhr.setRequestHeader("X-Requested-With", "ashcloud");
    xhr.setRequestHeader("Upload-Offset", String(offset));
    xhr.setRequestHeader("Content-Type", "application/octet-stream");
    xhr.responseType = "json";
    xhr.upload.onprogress = (event) => {
      lastProgress = performance.now();
      item.sent = offset + event.loaded;
    };
    // Once every byte is sent, the server needs a moment to finish.
    xhr.upload.onload = () => {
      lastProgress = performance.now() + STALL_MS;
    };
    xhr.onload = () => {
      finish();
      resolve({ status: xhr.status, data: xhr.response, offset: xhr.getResponseHeader("Upload-Offset") });
    };
    xhr.onerror = () => {
      finish();
      reject(new TypeError("network error"));
    };
    xhr.onabort = () => {
      finish();
      reject(new TypeError("no progress"));
    };
    xhr.send(item.file.slice(offset));
  });
}

function waitToRetry(item: Upload, seconds: number): Promise<void> {
  item.state = "retrying";
  return new Promise((resolve) => {
    let left = seconds;
    const stop = () => {
      clearInterval(timer);
      window.removeEventListener("online", online);
      item.message = "Reconnecting…";
      resolve();
    };
    const tick = () => {
      if (item.cancelled) return stop();
      if (!navigator.onLine) {
        item.message = "Waiting for the network…";
      } else if (left <= 0) {
        return stop();
      } else {
        item.message = `Connection lost - trying again in ${left} s`;
        left--;
      }
    };
    const online = () => {
      left = 0;
      tick();
    };
    const timer = setInterval(tick, 1000);
    window.addEventListener("online", online);
    tick();
  });
}

function done(item: Upload, file: Entry) {
  item.state = "done";
  item.sent = item.file.size;
  item.saved = file;
  item.seconds = (performance.now() - item.started) / 1000;
  const took = item.seconds >= 1 ? ` in ${item.seconds.toFixed(1)} s` : "";
  item.message = (file.name !== item.file.name ? `Saved as ${file.name} · ` : "") + formatSize(item.file.size) + took;
  onFinished(item.folder);
}

function fail(item: Upload, message: string) {
  if (item.cancelled) return;
  item.state = "failed";
  item.message = message;
}

export function cancelUpload(item: Upload) {
  const started = item.state !== "waiting";
  item.state = "cancelled";
  item.xhr?.abort();
  uploads.items.splice(uploads.items.indexOf(item), 1);
  if (started) {
    // Delete what reached the server. If this doesn't get through, the
    // server removes it after a week anyway.
    api("DELETE", `/api/v1/uploads/${item.id}`).catch(() => {});
  }
}

export function retryUpload(item: Upload) {
  item.state = "waiting";
  item.message = "";
  pump();
}

export function clearFinished() {
  uploads.items = uploads.items.filter((item) => item.active);
}

// ---- totals --------------------------------------------------------------------

export function totals() {
  let size = 0;
  let sent = 0;
  let active = 0;
  let sending = 0;
  let finished = 0;
  let failed = 0;
  for (const item of uploads.items) {
    if (item.state === "failed") failed++;
    else {
      size += item.file.size;
      sent += item.sent;
    }
    if (item.active) active++;
    if (item.state === "sending") sending++;
    if (item.state === "done") finished++;
  }
  return { size, sent, active, sending, finished, failed };
}

// Speed is measured over the last few seconds, across all uploads.
let samples: { at: number; sent: number }[] = [];
let meter = 0;

function measure(on: boolean) {
  if (on && !meter) {
    samples = [];
    meter = window.setInterval(() => {
      const now = performance.now();
      const { sent } = totals();
      samples.push({ at: now, sent });
      while (samples.length > 2 && now - samples[1].at > 3000) samples.shift();
      const first = samples[0];
      const seconds = (now - first.at) / 1000;
      uploads.speed = seconds > 0.5 ? Math.max(0, (sent - first.sent) / seconds) : 0;
    }, 500);
  } else if (!on && meter) {
    clearInterval(meter);
    meter = 0;
    uploads.speed = 0;
  }
}

// ---- staying awake ------------------------------------------------------------------

// Keep the phone's screen on while uploading: when it turns off, Android
// soon pauses the app and the uploads with it.
let wakeLock: WakeLockSentinel | null = null;

async function keepAwake(on: boolean) {
  if (!("wakeLock" in navigator)) return;
  if (on && !wakeLock && document.visibilityState === "visible") {
    try {
      wakeLock = await navigator.wakeLock.request("screen");
      wakeLock.addEventListener("release", () => {
        wakeLock = null;
      });
    } catch {
      // Not allowed right now (e.g. battery saver); uploads go on regardless.
    }
  } else if (!on && wakeLock) {
    wakeLock.release();
    wakeLock = null;
  }
}

document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") keepAwake(running > 0);
});

window.addEventListener("beforeunload", (event) => {
  if (running > 0) event.preventDefault(); // "Leave site? Uploads in progress"
});
