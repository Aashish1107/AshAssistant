"use strict";

const $ = (id) => document.getElementById(id);
let session = null;

// Every request that changes something must carry this header (see the
// server's CSRF check).
async function api(method, path, body) {
  const response = await fetch(path, {
    method,
    headers: { "Content-Type": "application/json", "X-Requested-With": "ashcloud" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  let data = null;
  if (response.status !== 204) {
    try { data = await response.json(); } catch { data = null; }
  }
  return { response, data };
}

function show(newSession) {
  session = newSession;
  $("status").textContent = session ? "" : "You're not logged in.";
  $("status").hidden = Boolean(session);
  $("login").hidden = Boolean(session);
  $("account").hidden = !session;
  if (session) {
    $("who").textContent = session.user.username;
    $("role").textContent = session.user.role;
    $("expires").textContent = new Date(session.expires_at).toLocaleString();
  }
  route();
}

function showError(message) {
  $("error").textContent = message || "";
}

async function busy(button, work) {
  button.disabled = true;
  try { await work(); } finally { button.disabled = false; }
}

// ---- views -------------------------------------------------------------

// What's open lives in the address, so the phone's Back button works:
// #/Photos/2024 is a folder, #play=/Videos/trip.mov is the video player.
function currentRoute() {
  const hash = decodeURIComponent(location.hash.slice(1));
  if (hash.startsWith("play=")) return { view: "player", path: hash.slice(5) };
  return { view: "folder", path: hash || "/" };
}

function route() {
  const where = session ? currentRoute() : { view: "none" };
  $("files").hidden = where.view !== "folder";
  $("player").hidden = where.view !== "player";
  if (where.view === "player") {
    openPlayer(where.path);
  } else {
    closePlayer();
    if (where.view === "folder") loadFolder(where.path);
  }
}

window.addEventListener("hashchange", route);

// ---- files -------------------------------------------------------------

// Each part of a path is encoded on its own, so names containing # or %
// still work, while the slashes stay readable.
function encodePath(path) {
  return path.split("/").map(encodeURIComponent).join("/");
}

function folderLink(path) {
  return "#" + encodePath(path);
}

function playerLink(path) {
  return "#play=" + encodePath(path);
}

function contentLink(path, inline) {
  return "/api/v1/files/content?path=" + encodeURIComponent(path) + (inline ? "&inline=1" : "");
}

function formatSize(bytes) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = bytes;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit++; }
  return (unit === 0 ? size : size.toFixed(1)) + " " + units[unit];
}

function icon(entry) {
  if (entry.type === "folder") return "📁";
  const type = entry.content_type || "";
  if (type.startsWith("video/")) return "🎬";
  if (type.startsWith("image/")) return "🖼️";
  if (type.startsWith("audio/")) return "🎵";
  if (type === "application/pdf") return "📕";
  return "📄";
}

function element(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  Object.assign(node, props);
  node.append(...children);
  return node;
}

function renderCrumbs(path) {
  const crumbs = $("crumbs");
  crumbs.replaceChildren(element("a", { href: folderLink("/"), textContent: "Home" }));
  let sofar = "";
  for (const part of path.split("/").filter(Boolean)) {
    sofar += "/" + part;
    crumbs.append(" / ", element("a", { href: folderLink(sofar), textContent: part }));
  }
}

async function loadFolder(path) {
  showError("");
  const { response, data } = await api("GET", "/api/v1/files?path=" + encodeURIComponent(path));
  if (response.status === 401) { show(null); return; }
  if (!response.ok) {
    showError(data?.error?.message ?? `Couldn't open the folder (HTTP ${response.status})`);
    return;
  }
  if (currentRoute().view !== "folder" || currentRoute().path !== path) return; // moved on meanwhile
  renderCrumbs(data.path);
  const rows = data.entries.map((entry) => {
    if (entry.type === "folder") {
      return element("li", {},
        element("a", { href: folderLink(entry.path), className: "name" }, icon(entry) + " " + entry.name),
        moreButton(entry));
    }
    const isVideo = (entry.content_type || "").startsWith("video/");
    const open = isVideo ? playerLink(entry.path) : contentLink(entry.path, true);
    return element("li", {},
      element("a", { href: open, className: "name" }, icon(entry) + " " + entry.name),
      element("span", { className: "meta" }, formatSize(entry.size)),
      moreButton(entry));
  });
  $("entries").replaceChildren(...rows);
  $("empty").hidden = rows.length > 0;
}

// refreshFolder reloads the folder on screen, if one is.
function refreshFolder() {
  const where = currentRoute();
  if (session && where.view === "folder") loadFolder(where.path);
}

function currentFolder() {
  const where = currentRoute();
  return where.view === "folder" ? where.path : "/";
}

function parentOf(path) {
  const parent = path.slice(0, path.lastIndexOf("/"));
  return parent || "/";
}

function joinPath(folder, name) {
  return (folder === "/" ? "" : folder) + "/" + name;
}

function baseName(path) {
  return path.slice(path.lastIndexOf("/") + 1);
}

// ---- changing files ----------------------------------------------------------

let menuEntry = null; // the file or folder whose ⋯ menu is open
let menuRow = null; // ...and its row in the list

function moreButton(entry) {
  const button = element("button", { type: "button", className: "more", ariaLabel: "More for " + entry.name, title: "More" }, "⋯");
  button.addEventListener("click", () => {
    menuRow = button.closest("li");
    openMenu(entry);
  });
  return button;
}

// Dialogs close when you tap outside them.
for (const dialog of document.querySelectorAll("dialog")) {
  dialog.addEventListener("click", (event) => { if (event.target === dialog) dialog.close(); });
}

function openMenu(entry) {
  menuEntry = entry;
  $("item-menu-title").textContent = icon(entry) + " " + entry.name;
  $("item-download").hidden = entry.type === "folder";
  $("item-download").href = contentLink(entry.path, false);
  $("item-menu").showModal();
}

$("item-cancel").addEventListener("click", () => $("item-menu").close());
$("item-download").addEventListener("click", () => $("item-menu").close());

// ask shows a dialog asking for a name. submit(value) does the work and
// returns an error message to show, or nothing when it's done.
function ask({ title, value = "", okLabel = "OK", select, submit }) {
  const dialog = $("ask");
  $("ask-title").textContent = title;
  $("ask-ok").textContent = okLabel;
  $("ask-error").textContent = "";
  const input = $("ask-input");
  input.value = value;
  $("ask-form").onsubmit = async (event) => {
    event.preventDefault();
    const name = input.value.trim();
    if (!name) return;
    if (name.includes("/") || name.includes("\\")) {
      $("ask-error").textContent = "A name can't contain / or \\";
      return;
    }
    $("ask-ok").disabled = true;
    try {
      const problem = await submit(name);
      if (problem) $("ask-error").textContent = problem;
      else dialog.close();
    } catch (error) {
      $("ask-error").textContent = "Couldn't reach the server - try again.";
    } finally {
      $("ask-ok").disabled = false;
    }
  };
  dialog.showModal();
  input.focus();
  if (select) input.setSelectionRange(...select);
  else input.select();
}

$("ask-cancel").addEventListener("click", () => $("ask").close());

function problemFrom(response, data, what) {
  if (response.status === 401) { show(null); return "Log in again first."; }
  return data?.error?.message ?? `Couldn't ${what} (HTTP ${response.status})`;
}

$("new-folder").addEventListener("click", () => {
  const folder = currentFolder();
  ask({
    title: "New folder",
    okLabel: "Create",
    submit: async (name) => {
      const { response, data } = await api("POST", "/api/v1/files/folder", { path: joinPath(folder, name) });
      if (!response.ok) return problemFrom(response, data, "create the folder");
      refreshFolder();
    },
  });
});

$("item-rename").addEventListener("click", () => {
  const entry = menuEntry;
  $("item-menu").close();
  // Select the name without its extension, as a file manager would.
  const dot = entry.type === "file" ? entry.name.lastIndexOf(".") : -1;
  ask({
    title: "Rename",
    value: entry.name,
    okLabel: "Rename",
    select: dot > 0 ? [0, dot] : null,
    submit: async (name) => {
      if (name === entry.name) return;
      const { response, data } = await api("POST", "/api/v1/files/move", { from: entry.path, to: joinPath(parentOf(entry.path), name) });
      if (!response.ok) return problemFrom(response, data, "rename it");
      refreshFolder();
    },
  });
});

// Moving: browse to the folder to move into, then tap "Move here".
let moveFolder = "/";

$("item-move").addEventListener("click", () => {
  const entry = menuEntry;
  $("item-menu").close();
  $("move-title").textContent = "Move " + entry.name + " to…";
  $("move-error").textContent = "";
  $("move-here").onclick = async () => {
    $("move-here").disabled = true;
    try {
      const { response, data } = await api("POST", "/api/v1/files/move", { from: entry.path, to: joinPath(moveFolder, entry.name) });
      if (!response.ok) {
        $("move-error").textContent = problemFrom(response, data, "move it");
        return;
      }
      $("move").close();
      refreshFolder();
      toast(`Moved to ${moveFolder === "/" ? "Home" : baseName(moveFolder)}`);
    } catch {
      $("move-error").textContent = "Couldn't reach the server - try again.";
    } finally {
      $("move-here").disabled = false;
    }
  };
  browseMoveTarget(parentOf(entry.path), entry);
  $("move").showModal();
});

$("move-cancel").addEventListener("click", () => $("move").close());

async function browseMoveTarget(folder, moving) {
  moveFolder = folder;
  $("move-here").disabled = folder === parentOf(moving.path);
  const crumbs = $("move-crumbs");
  const go = (path, label) => {
    const button = element("button", { type: "button", className: "link" }, label);
    button.addEventListener("click", () => browseMoveTarget(path, moving));
    return button;
  };
  crumbs.replaceChildren(go("/", "Home"));
  let sofar = "";
  for (const part of folder.split("/").filter(Boolean)) {
    sofar += "/" + part;
    crumbs.append(" / ", go(sofar, part));
  }
  const list = $("move-folders");
  list.replaceChildren(element("li", { className: "note" }, "Loading…"));
  const { response, data } = await api("GET", "/api/v1/files?path=" + encodeURIComponent(folder));
  if (moveFolder !== folder) return; // moved on meanwhile
  if (!response.ok) {
    list.replaceChildren(element("li", { className: "error" }, problemFrom(response, data, "open the folder")));
    return;
  }
  // A folder can't go inside itself, so it isn't offered.
  const folders = data.entries.filter((e) => e.type === "folder" && e.path !== moving.path);
  list.replaceChildren(...folders.map((e) => element("li", {}, go(e.path, "📁 " + e.name))));
  if (!folders.length) list.append(element("li", { className: "note" }, "No folders here."));
}

// Deleting is instant and goes to the trash, so there's no "are you sure?":
// an Undo button appears instead.
$("item-delete").addEventListener("click", async () => {
  const entry = menuEntry;
  $("item-menu").close();
  showError("");
  menuRow.hidden = true; // gone at once; put back if the server says no
  try {
    const { response, data } = await api("DELETE", "/api/v1/files?path=" + encodeURIComponent(entry.path));
    if (!response.ok) {
      showError(problemFrom(response, data, "delete it"));
      refreshFolder();
      return;
    }
    refreshFolder();
    toast(`Deleted ${entry.name}`, "Undo", async () => {
      const { response, data: restored } = await api("POST", "/api/v1/trash/restore", { id: data.trash_id });
      if (!response.ok) showError(problemFrom(response, restored, "undo"));
      refreshFolder();
    });
  } catch {
    showError("Couldn't reach the server - try again.");
    refreshFolder();
  }
});

let toastTimer = 0;

// toast shows a short message at the bottom, optionally with a button.
function toast(text, actionLabel, action) {
  clearTimeout(toastTimer);
  $("toast-text").textContent = text;
  const button = $("toast-action");
  button.hidden = !actionLabel;
  button.textContent = actionLabel || "";
  button.onclick = () => {
    $("toast").hidden = true;
    action();
  };
  $("toast").hidden = false;
  toastTimer = setTimeout(() => { $("toast").hidden = true; }, actionLabel ? 8000 : 3000);
}

// ---- login / logout --------------------------------------------------------

$("login").addEventListener("submit", (event) => {
  event.preventDefault();
  const form = event.target;
  busy(form.querySelector("button"), async () => {
    showError("");
    const fields = new FormData(form);
    const { response, data } = await api("POST", "/api/v1/auth/login", {
      username: fields.get("username"),
      password: fields.get("password"),
    });
    if (!response.ok) {
      showError(data?.error?.message ?? `Login failed (HTTP ${response.status})`);
      return;
    }
    form.reset();
    show(data);
  }).catch((error) => showError(`Couldn't reach the server: ${error}`));
});

$("logout").addEventListener("click", (event) => {
  busy(event.target, async () => {
    showError("");
    const { response, data } = await api("POST", "/api/v1/auth/logout");
    if (!response.ok) {
      showError(data?.error?.message ?? `Logout failed (HTTP ${response.status})`);
      return;
    }
    show(null);
  }).catch((error) => showError(`Couldn't reach the server: ${error}`));
});

api("GET", "/api/v1/me")
  .then(({ response, data }) => show(response.ok ? data : null))
  .catch((error) => {
    $("status").textContent = "Couldn't reach the server.";
    showError(String(error));
  });
