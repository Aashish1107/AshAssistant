"use strict";

// Uploads. Each file goes up in one request, straight from the phone's
// storage to the laptop's disk, three files at a time. If the connection
// drops (walking out of Wi-Fi range, switching to mobile data), the upload
// waits for the network, asks the server how much arrived and sends only the
// rest. Each upload's ID comes from the folder and the file's name, size and
// date, so after the tab was closed or the phone restarted, picking the same
// file again also continues where it stopped.

const PARALLEL_UPLOADS = 3;
// Files at least this big first ask where to continue, and whether they
// fit; smaller ones just start (an extra round trip would cost more than
// resending a few megabytes).
const ASK_FIRST_SIZE = 8 << 20;
// A request that sends nothing for this long is assumed dead and retried.
const STALL_MS = 30000;
// Waits between retries after network trouble, in seconds.
const RETRY_DELAYS = [1, 2, 4, 8, 15, 30];

const uploads = []; // every upload shown in the list, oldest first
let running = 0;

class UploadError extends Error {}

function uploadURL(item) {
  return `/api/v1/uploads/${item.id}?path=${encodeURIComponent(item.path)}&size=${item.file.size}&modified=${item.file.lastModified || 0}`;
}

// uploadID derives an upload's ID from what identifies the file, so the same
// file to the same folder always gets the same ID.
async function uploadID(path, file) {
  const text = `ashcloud-upload-v1\n${path}\n${file.size}\n${file.lastModified}`;
  let bytes;
  if (crypto.subtle) {
    bytes = new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text))).slice(0, 16);
  } else {
    bytes = crypto.getRandomValues(new Uint8Array(16)); // plain http: no hashing available; can't resume after a reload
  }
  return btoa(String.fromCharCode(...bytes)).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}

// ---- queue -----------------------------------------------------------------

async function addUploads(fileList, folder) {
  $("uploads").hidden = false;
  for (const file of fileList) {
    const path = joinPath(folder, file.name);
    const id = await uploadID(path, file);
    if (uploads.some((u) => u.id === id && (u.state === "waiting" || u.state === "sending" || u.state === "retrying"))) {
      continue; // already on its way
    }
    const item = { id, file, path, folder, state: "waiting", sent: 0, message: "", started: 0, xhr: null, row: null };
    uploads.push(item);
    item.row = uploadRow(item);
    $("upload-list").append(item.row.li);
  }
  pump();
  render();
}

function pump() {
  for (const item of uploads) {
    if (running >= PARALLEL_UPLOADS) break;
    if (item.state !== "waiting") continue;
    running++;
    item.state = "sending";
    item.started = performance.now();
    send(item)
      .catch((error) => fail(item, error instanceof UploadError ? error.message : `Upload failed: ${error}`))
      .finally(() => {
        running--;
        pump();
        render();
      });
  }
  keepAwake(running > 0);
}

// send runs one upload to the end: done, failed or cancelled.
async function send(item) {
  let offset = 0;
  let ask = item.file.size >= ASK_FIRST_SIZE;
  let attempt = 0;
  for (;;) {
    if (item.state === "cancelled") return;
    try {
      if (ask) {
        const { response, data } = await api("POST", uploadURL(item));
        if (!response.ok) throw new UploadError(problemFrom(response, data, "upload it"));
        if (data.file) return done(item, data.file);
        offset = data.offset;
        ask = false;
      }
      item.state = "sending";
      item.message = "";
      const result = await put(item, offset);
      attempt = 0;
      const code = result.data?.error?.code;
      if (result.status === 201 || result.data?.file) return done(item, result.data.file);
      if (result.status === 200) { offset = result.data.offset; continue; }
      if (code === "wrong_offset") { offset = Number(result.offset); continue; }
      if (code === "upload_not_found") { offset = 0; continue; }
      if (code === "taken_over") throw new UploadError("Continuing in another tab or window");
      if (code === "interrupted" || (result.status >= 500 && result.status !== 507)) throw new TypeError("server interrupted");
      throw new UploadError(problemFrom({ status: result.status }, result.data, "upload it"));
    } catch (error) {
      if (error instanceof UploadError || item.state === "cancelled") throw error;
      // Network trouble. Wait, then ask the server where to continue.
      await waitToRetry(item, RETRY_DELAYS[Math.min(attempt, RETRY_DELAYS.length - 1)]);
      attempt++;
      ask = true;
    }
  }
}

// put sends the file from offset on, reporting progress as it goes.
function put(item, offset) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    item.xhr = xhr;
    item.sent = offset;
    let lastProgress = performance.now();
    const watchdog = setInterval(() => {
      if (performance.now() - lastProgress > STALL_MS) xhr.abort();
    }, 5000);
    const finish = () => { clearInterval(watchdog); item.xhr = null; };

    xhr.open("PUT", uploadURL(item));
    xhr.setRequestHeader("X-Requested-With", "ashcloud");
    xhr.setRequestHeader("Upload-Offset", String(offset));
    xhr.setRequestHeader("Content-Type", "application/octet-stream");
    xhr.responseType = "json";
    xhr.upload.onprogress = (event) => {
      lastProgress = performance.now();
      item.sent = offset + event.loaded;
      render();
    };
    // Once every byte is sent, the server needs a moment to finish.
    xhr.upload.onload = () => { lastProgress = performance.now() + STALL_MS; };
    xhr.onload = () => {
      finish();
      resolve({ status: xhr.status, data: xhr.response, offset: xhr.getResponseHeader("Upload-Offset") });
    };
    xhr.onerror = () => { finish(); reject(new TypeError("network error")); };
    xhr.onabort = () => { finish(); reject(new TypeError("no progress")); };
    xhr.send(item.file.slice(offset));
  });
}

function waitToRetry(item, seconds) {
  item.state = "retrying";
  return new Promise((resolve) => {
    let left = seconds;
    const tick = () => {
      if (item.state === "cancelled") return stop();
      if (!navigator.onLine) {
        item.message = "Waiting for the network…";
      } else if (left <= 0) {
        return stop();
      } else {
        item.message = `Connection lost - trying again in ${left} s`;
        left--;
      }
      render();
    };
    const timer = setInterval(tick, 1000);
    const online = () => { left = 0; tick(); };
    window.addEventListener("online", online);
    function stop() {
      item.message = "Reconnecting…";
      clearInterval(timer);
      window.removeEventListener("online", online);
      resolve();
    }
    tick();
  });
}

function done(item, file) {
  item.state = "done";
  item.sent = item.file.size;
  const seconds = (performance.now() - item.started) / 1000;
  item.message = (file.path !== item.path ? `Saved as ${file.name} · ` : "") + `${formatSize(item.file.size)} in ${seconds.toFixed(1)} s`;
  if (item.folder === currentFolder()) refreshSoon();
}

function fail(item, message) {
  if (item.state === "cancelled") return;
  item.state = "failed";
  item.message = message;
}

function cancel(item) {
  const wasRunning = item.state === "sending" || item.state === "retrying";
  item.state = "cancelled";
  item.xhr?.abort();
  item.row.li.remove();
  uploads.splice(uploads.indexOf(item), 1);
  if (wasRunning || item.sent > 0) {
    // Delete what reached the server. If this doesn't get through, the
    // server removes it after a week anyway.
    api("DELETE", `/api/v1/uploads/${item.id}`).catch(() => {});
  }
  render();
}

function retry(item) {
  item.state = "waiting";
  item.message = "";
  pump();
  render();
}

// Several uploads finishing together reload the folder once.
let refreshTimer = 0;
function refreshSoon() {
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(refreshFolder, 300);
}

// ---- display -----------------------------------------------------------------

function uploadRow(item) {
  const name = element("span", { className: "upload-name" }, item.file.name);
  const bar = element("progress", { max: 1, value: 0 });
  const meta = element("span", { className: "upload-meta" });
  const cancelButton = element("button", { type: "button", className: "upload-cancel", ariaLabel: "Cancel " + item.file.name, title: "Cancel" }, "✕");
  cancelButton.addEventListener("click", () => cancel(item));
  const retryButton = element("button", { type: "button", className: "secondary small", hidden: true }, "Retry");
  retryButton.addEventListener("click", () => retry(item));
  const li = element("li", { className: "upload" }, name, cancelButton, bar, meta, retryButton);
  return { li, bar, meta, cancelButton, retryButton };
}

// Speed is measured over the last few seconds, across all uploads.
const samples = [];

function totals() {
  let size = 0;
  let sent = 0;
  for (const item of uploads) {
    if (item.state === "failed") continue;
    size += item.file.size;
    sent += item.sent;
  }
  return { size, sent };
}

function currentSpeed(sent) {
  const now = performance.now();
  samples.push({ now, sent });
  while (samples.length > 2 && now - samples[1].now > 3000) samples.shift();
  const first = samples[0];
  const seconds = (now - first.now) / 1000;
  return seconds > 0.5 ? Math.max(0, (sent - first.sent) / seconds) : 0;
}

function formatTime(seconds) {
  if (!isFinite(seconds)) return "";
  if (seconds < 60) return `${Math.ceil(seconds)} s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)} min`;
  return `${(seconds / 3600).toFixed(1)} h`;
}

let renderQueued = false;

// render updates the list at most once per frame, however often progress
// is reported.
function render() {
  if (renderQueued) return;
  renderQueued = true;
  requestAnimationFrame(() => {
    renderQueued = false;
    draw();
  });
}

function draw() {
  for (const item of uploads) {
    const { bar, meta, cancelButton, retryButton, li } = item.row;
    const size = item.file.size;
    bar.value = size ? item.sent / size : item.state === "done" ? 1 : 0;
    li.dataset.state = item.state;
    cancelButton.hidden = item.state === "done";
    retryButton.hidden = item.state !== "failed";
    if (item.state === "waiting") meta.textContent = `Waiting · ${formatSize(size)}`;
    else if (item.state === "sending") meta.textContent = `${Math.floor(bar.value * 100)}% · ${formatSize(item.sent)} of ${formatSize(size)}`;
    else if (item.state === "done") meta.textContent = "✓ " + item.message;
    else meta.textContent = item.message;
  }

  const active = uploads.filter((u) => u.state === "waiting" || u.state === "sending" || u.state === "retrying");
  const finished = uploads.filter((u) => u.state === "done").length;
  const failed = uploads.filter((u) => u.state === "failed").length;
  const { size, sent } = totals();
  const speed = currentSpeed(sent);
  let summary;
  if (active.length) {
    summary = `Uploading ${finished + 1} of ${finished + active.length}`;
    if (!active.some((u) => u.state === "sending")) summary += " · waiting for the connection";
    else if (speed > 0) summary += ` · ${formatSize(speed)}/s · ${formatTime((size - sent) / speed)} left`;
  } else if (uploads.length) {
    summary = failed ? `${failed} upload${failed > 1 ? "s" : ""} failed` : `All ${finished} uploaded`;
  }
  $("uploads-summary").textContent = summary || "";
  $("uploads-clear").hidden = active.length === uploads.length;
  $("uploads").hidden = uploads.length === 0;
}

$("uploads-clear").addEventListener("click", () => {
  for (const item of [...uploads]) {
    if (item.state === "done" || item.state === "failed") {
      item.row.li.remove();
      uploads.splice(uploads.indexOf(item), 1);
    }
  }
  samples.length = 0;
  render();
});

// ---- picking files -------------------------------------------------------------

$("upload-button").addEventListener("click", () => $("file-input").click());

$("file-input").addEventListener("change", (event) => {
  const picked = [...event.target.files];
  event.target.value = ""; // so picking the same file again fires again
  if (picked.length) addUploads(picked, currentFolder());
});

// On a computer, files can be dropped onto the folder.
$("files").addEventListener("dragover", (event) => {
  if (!event.dataTransfer?.types.includes("Files")) return;
  event.preventDefault();
  $("files").classList.add("dropping");
});
$("files").addEventListener("dragleave", () => $("files").classList.remove("dropping"));
$("files").addEventListener("drop", (event) => {
  event.preventDefault();
  $("files").classList.remove("dropping");
  const dropped = [...event.dataTransfer.files];
  if (dropped.length) addUploads(dropped, currentFolder());
});

// ---- staying awake -------------------------------------------------------------

// Keep the phone's screen on while uploading: when it turns off, Android
// soon pauses the page and the uploads with it.
let wakeLock = null;

async function keepAwake(on) {
  if (!("wakeLock" in navigator)) return;
  if (on && !wakeLock && document.visibilityState === "visible") {
    try {
      wakeLock = await navigator.wakeLock.request("screen");
      wakeLock.addEventListener("release", () => { wakeLock = null; });
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
