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
  $("files").hidden = !session;
  if (session) {
    $("who").textContent = session.user.username;
    $("role").textContent = session.user.role;
    $("expires").textContent = new Date(session.expires_at).toLocaleString();
    loadFolder(currentFolder());
  }
}

function showError(message) {
  $("error").textContent = message || "";
}

async function busy(button, work) {
  button.disabled = true;
  try { await work(); } finally { button.disabled = false; }
}

// ---- files -------------------------------------------------------------

// The open folder lives in the address (#/Photos/2024) so Back works.
function currentFolder() {
  return decodeURIComponent(location.hash.slice(1)) || "/";
}

function folderLink(path) {
  return "#" + encodeURI(path);
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
  renderCrumbs(data.path);
  const rows = data.entries.map((entry) => {
    if (entry.type === "folder") {
      return element("li", {},
        element("a", { href: folderLink(entry.path), className: "name" }, icon(entry) + " " + entry.name));
    }
    return element("li", {},
      element("a", { href: contentLink(entry.path, true), className: "name" }, icon(entry) + " " + entry.name),
      element("span", { className: "meta" }, formatSize(entry.size)),
      element("a", { href: contentLink(entry.path, false), className: "download", title: "Download", ariaLabel: "Download " + entry.name }, "⬇"));
  });
  $("entries").replaceChildren(...rows);
  $("empty").hidden = rows.length > 0;
  $("empty").textContent = `This folder is empty. On the laptop, put files in C:\\AshCloud\\storage\\users\\${session.user.id}\\`;
}

window.addEventListener("hashchange", () => { if (session) loadFolder(currentFolder()); });

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
