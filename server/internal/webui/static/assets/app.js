"use strict";

const $ = (id) => document.getElementById(id);

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

function show(session) {
  $("status").textContent = session ? "You're logged in." : "You're not logged in.";
  $("login").hidden = Boolean(session);
  $("account").hidden = !session;
  if (session) {
    $("who").textContent = session.user.username;
    $("role").textContent = session.user.role;
    $("expires").textContent = new Date(session.expires_at).toLocaleString();
  }
}

function showError(message) {
  $("error").textContent = message || "";
}

async function busy(button, work) {
  button.disabled = true;
  try { await work(); } finally { button.disabled = false; }
}

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
