# Ash Cloud — Plan

A private cloud that runs on my Windows laptop. I use it **only through a web
browser**, from my phone, from anywhere. Built for one person, but designed so
it could grow to more users without a rewrite.

Status: **Phase 1, step 2 built** — settings file, SQLite database, `user create`
and `GET /readyz`. It is done once the phone shows `/readyz` ok
([`server/README.md`](../server/README.md)). Next: step 3.

---

## 1. Decisions (confirmed)

| Question | Decision |
|---|---|
| Laptop OS | **Windows** |
| Who uses it | **Just me.** No other users, no public access. |
| Server language | **Go** |
| How the phone reaches it | **Tailscale** (private network, nothing exposed to the internet) |
| Web UI | **Svelte + TypeScript + Vite**, built into the Go binary |
| Database | **SQLite** |
| Existing Python Ash CLI | Stays as it is; plugged in during Phase 4 |

### Goals
- Works from my phone on any network (mobile data, any Wi-Fi).
- Fast: pages feel instant; server adds < 5 ms to any request.
- Production quality: secure by default, survives reboots, logged, monitored, backed up, tested.
- Built in small steps: a working server first, then features on top.

### Not goals (for now)
- Other users, sharing with other people, or a public website.
- Running on anything other than this one laptop.

---

## 2. Tech stack

| Layer | Choice | Why |
|---|---|---|
| Language | **Go** (1.26 or newer — what current libraries require) | One `.exe`, nothing to install at runtime, low memory, very fast, easy to run as a Windows service. `os.Root` (Go 1.24) blocks `..\` and symlink escapes when touching files. |
| HTTP | Go standard library `net/http` | Method + path routing, HTTP/2, `Range` requests (video seeking) built in. No framework needed. |
| Logging | `log/slog` (standard library) | Structured JSON logs. |
| Database | **SQLite** in WAL mode via `modernc.org/sqlite` | Embedded, zero maintenance, microsecond queries. Pure Go, so no C compiler needed on Windows. |
| SQL | `sqlc` (type-safe generated Go from SQL) + `goose` (migrations) | Plain SQL, checked at build time, versioned schema changes. |
| Config | TOML file (`pelletier/go-toml/v2`) | Human-readable settings. |
| Passwords | argon2id (`golang.org/x/crypto/argon2`) | Current best practice for password hashing. |
| Windows service | `kardianos/service` | `ashcloud service install` → starts at boot. |
| Web UI | **Svelte + TypeScript + Vite** | Tiny bundles → fast on phones. Built files are embedded in the Go binary with `go:embed`. |
| Remote access | **Tailscale** + `tailscale serve` | Encrypted, works behind any router/mobile network, real HTTPS certificate, no ports opened, usually a direct phone↔laptop connection. |
| Later phases | `gopsutil` + `distatus/battery` (status), `tus/tusd` (resumable uploads), `coder/websocket` (live updates, terminal), Windows ConPTY (terminal), `xterm.js` (terminal UI), `fsnotify` (file watcher), `go-webauthn` (passkeys, optional) | |

**Why Go over Python:** Python (FastAPI) would work for one person, but needs a
Python install + virtualenv on the laptop, uses noticeably more CPU/RAM per
request, and is clumsier as a Windows service. Go ships as one `ashcloud.exe`
with the web UI inside it.

---

## 3. Architecture — how a request travels end to end

```
 PHONE (anywhere: 4G/5G or any Wi-Fi)
 Browser / home-screen app  →  https://<laptop>.<tailnet>.ts.net
        │  HTTPS (TLS 1.3, HTTP/2)
 Tailscale app on phone ── WireGuard: direct peer-to-peer (fast) or DERP relay (fallback)
        │  no ports opened on the home router, nothing public
 ══════ WINDOWS LAPTOP ═════════════════════════════════════════════════════
 Tailscale (`tailscale serve`)  →  HTTPS on :443 with a real certificate
        │  http://127.0.0.1:8080   (server never listens on the network itself,
        │                           so no Windows Firewall rule is needed)
 ashcloud.exe  (one Go binary, web UI embedded, runs as a Windows service)
   router → middleware chain → handlers → services ─┬─ SQLite  C:\AshCloud\ashcloud.db
                                                    └─ Storage → C:\AshCloud\storage\users\<id>\
 127.0.0.1:9090 → /metrics, /debug/pprof   (laptop only, never exposed through Tailscale)
```

### Middleware chain (every request, in this order)
1. **Request ID** — attached to logs and the response (`X-Request-Id`).
2. **Panic recovery** — a bug returns 500, never kills the server.
3. **Access log + metrics** — method, route, status, duration, bytes.
4. **Security headers** — CSP, HSTS, `X-Content-Type-Options: nosniff`, `Referrer-Policy`, `frame-ancestors 'none'`.
5. **Limits** — request timeouts; body size limits on JSON endpoints (uploads are streamed and exempt).
6. **Rate limit** — login: 5 failures per 15 minutes per client.
7. **Session auth** — cookie → SHA-256 → `sessions` table (cached in memory).
8. **CSRF check** — `SameSite=Strict` cookie **and** a required `X-Requested-With: ashcloud` header on every non-GET request.
9. **Role check** — everything is `admin` for now; the check exists so more users are possible later.
10. **Handler** → **Service** → **Storage / DB**.

Handlers only parse input and write output. Services hold the rules.
Files are only touched through the `Storage` interface (local disk via
`os.Root` today; could become S3 later without changing handlers).

### Example: uploading a photo, end to end
1. Tap **Upload** in the app → `PUT /api/v1/files/content?path=/Photos/IMG_1.jpg` with the session cookie and `X-Requested-With` header (progress bar from upload events).
2. Phone Tailscale → WireGuard → laptop Tailscale terminates HTTPS → forwards to `127.0.0.1:8080`.
3. Middleware chain: request ID → recover → log → headers → limits → auth → CSRF.
4. `FilesService.Upload` checks the name against Windows rules → `Storage.Create` (via `os.Root`, can't escape the user folder) → body streamed to a temp file → flushed → atomically renamed into place → `audit_log` row written.
5. `201 Created` + the new file's JSON → the app adds it to the list.

---

## 4. Repository layout

```
AshAssistant/
├── AshAssistant.py, main.py, ash.txt   # existing Python Ash CLI (unchanged)
├── docs/PLAN.md                        # this file
├── server/                             # Go module
│   ├── go.mod
│   ├── sqlc.yaml
│   ├── cmd/ashcloud/main.go            # CLI: init, serve, user, service, backup
│   └── internal/
│       ├── config/     # load ashcloud.toml (+ env var overrides)
│       ├── httpapi/    # router, middleware, handlers, JSON error format
│       ├── auth/       # argon2id, sessions, rate limiter
│       ├── files/      # FilesService: business rules for files
│       ├── storage/    # Storage interface + localfs implementation (os.Root)
│       ├── db/         # migrations/*.sql, queries/*.sql, sqlc output
│       ├── system/     # CPU / RAM / disk / battery (Phase 2)
│       └── webui/      # go:embed of the built frontend (web/ builds into here)
├── web/                                # Svelte + TypeScript + Vite
│   ├── src/            # screens: Login, Files, Status, Terminal
│   └── vite.config.ts
└── .github/workflows/ci.yml
```

---

## 5. Configuration and files on disk

Everything lives in one folder so it's easy to find and back up:

```
C:\AshCloud\
├── ashcloud.toml            # settings
├── ashcloud.db              # SQLite (plus -wal / -shm files)
├── logs\                    # rotated JSON logs
├── backups\                 # nightly database snapshots
├── cache\thumbs\            # generated thumbnails (Phase 3)
└── storage\users\1\         # MY FILES (per-user folder from day one)
```

`ashcloud.toml`:
```toml
listen       = "127.0.0.1:8080"   # only reachable through Tailscale
admin_listen = "127.0.0.1:9090"   # metrics + profiling, laptop only
data_dir     = 'C:\AshCloud'
storage_dir  = 'C:\AshCloud\storage'
session_ttl  = "720h"             # stay logged in on the phone for 30 days
log_level    = "info"
```

---

## 6. Data model (SQLite)

Phase 1:
```sql
users     (id INTEGER PRIMARY KEY, username TEXT UNIQUE NOT NULL,   -- step 2 ✅
           password_hash TEXT NOT NULL,                           -- argon2id
           role TEXT NOT NULL CHECK (role IN ('admin', 'user')),
           created_at INTEGER NOT NULL) STRICT

sessions  (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id),
           token_hash BLOB UNIQUE NOT NULL,      -- SHA-256 of the cookie, never the token itself
           user_agent TEXT, ip TEXT,
           created_at INTEGER NOT NULL, last_seen_at INTEGER NOT NULL,
           expires_at INTEGER NOT NULL)

audit_log (id INTEGER PRIMARY KEY, user_id INTEGER, action TEXT NOT NULL,
           path TEXT, ip TEXT, at INTEGER NOT NULL)
```

Later phases add: `uploads` (resumable upload state, P2), `trash` (P2),
`file_index` + FTS5 search table and `thumbnails` (P3), `passkeys` (P5).

**The disk is the source of truth for files.** The database stores users,
sessions, history and indexes, never the files themselves. Files dropped into
the folder directly on the laptop show up in the app.

---

## 7. API — every endpoint

Conventions:
- All API routes are under `/api/v1`. JSON in, JSON out.
- Paths are relative to my storage folder, use `/`, and `/` is the root.
- Errors always look like `{"error": {"code": "not_found", "message": "File not found"}}`.
- Names are checked against Windows rules: no `< > : " / \ | ? *`, no control characters, no trailing dot/space, no reserved names (`CON`, `NUL`, `COM1`…). (`:` matters: on NTFS it would create a hidden alternate data stream.)

| Endpoint | What it does | Touches | Phase |
|---|---|---|---|
| `GET /healthz` | Liveness: process is up | — | 1 |
| `GET /readyz` | Readiness: DB answers, storage folder writable | DB, disk | 1 |
| `GET /` · `GET /assets/*` | The web app (hashed file names, cached forever) | binary | 1 |
| `POST /api/v1/auth/login` | Check password → set session cookie | users, sessions, audit | 1 |
| `POST /api/v1/auth/logout` | Delete current session | sessions | 1 |
| `GET /api/v1/me` | Who am I + settings the UI needs | users | 1 |
| `GET /api/v1/files?path=` | List a folder (name, size, modified, type) | Storage | 1 |
| `GET /api/v1/files/content?path=` | Download / stream; `Range` + `ETag`; `?inline=1` to view in browser | Storage | 1 |
| `PUT /api/v1/files/content?path=` | Upload, streamed to disk; auto-renames `photo (1).jpg` on clash | Storage, audit | 1 |
| `POST /api/v1/files/folder` | Create folder `{path}` | Storage, audit | 1 |
| `POST /api/v1/files/move` | Rename or move `{from, to}` | Storage, audit | 1 |
| `DELETE /api/v1/files?path=` | Delete (moves to trash from Phase 2) | Storage, audit | 1 |
| `GET /api/v1/sessions` · `DELETE /api/v1/sessions/{id}` | See / log out my devices | sessions | 2 |
| `POST /api/v1/auth/password` | Change password; logs out other devices | users, sessions | 2 |
| `POST /api/v1/uploads` · `HEAD`/`PATCH /api/v1/uploads/{id}` | Resumable uploads (tus) for big videos on weak signal | uploads, Storage | 2 |
| `GET /api/v1/trash` · `POST /api/v1/trash/restore` · `DELETE /api/v1/trash` | Trash: list, restore, empty | trash, Storage | 2 |
| `GET /api/v1/system/status` | CPU, RAM, disk, battery, uptime, Tailscale status | OS | 2 |
| `GET /metrics` (port 9090 only) | Prometheus metrics: latency, errors, bytes | — | 2 |
| `GET /api/v1/files/thumbnail?path=&size=` | Cached thumbnails (incl. iPhone HEIC) | cache, Storage | 3 |
| `GET /api/v1/search?q=` | Search file names | file_index (FTS5) | 3 |
| `POST /api/v1/files/zip` | Download several files / a folder as a streamed zip | Storage | 3 |
| `WS /api/v1/events` | Live updates: file changed, upload done, status | event bus | 3 |
| `WS /api/v1/terminal` | PowerShell on the laptop in the browser (ConPTY) | OS | 4 |
| `POST /api/v1/ash` | Natural language → command via the Python Ash code | Ash | 4 |
| `/api/v1/auth/passkey/{register,login}/{begin,finish}` | Log in with Face ID / fingerprint | passkeys | 5 (optional) |

### Screens → endpoints

| Screen | Calls |
|---|---|
| Login | `POST /auth/login`, `GET /me` |
| Files | `GET /files`, `GET /files/content`, `PUT /files/content`, `POST /files/folder`, `POST /files/move`, `DELETE /files` (+ P2 `/uploads`, P3 `/files/thumbnail`, `/search`, `/files/zip`, `WS /events`) |
| Trash (P2) | `GET /trash`, `POST /trash/restore`, `DELETE /trash` |
| Status (P2) | `GET /system/status` (+ P3 `WS /events` for live numbers) |
| Settings (P2) | `GET /sessions`, `DELETE /sessions/{id}`, `POST /auth/password`, `POST /auth/logout` |
| Terminal (P4) | `WS /terminal`, `POST /ash` |

### File download safety
User files are served with `Content-Security-Policy: sandbox` and
`nosniff`, so an uploaded `.html` or `.svg` can never run scripts as the app.
`?inline=1` shows images, video, audio and PDF; text and code are shown as plain
text; everything else downloads.

---

## 8. One-time laptop setup (Windows)

1. **Install tools** (PowerShell):
   ```powershell
   winget install GoLang.Go OpenJS.NodeJS.LTS Git.Git tailscale.tailscale
   ```
2. **Tailscale on the laptop**: sign in. In the tray icon → Preferences, turn on **Run unattended** (keeps it connected when nobody is logged in).
3. **Tailscale on the phone**: install the Tailscale app, sign in with the **same account**, leave it on.
4. **Tailscale admin console** (login.tailscale.com):
   - **DNS** → enable **MagicDNS** and **HTTPS Certificates**.
   - **Machines** → the laptop → **Disable key expiry** (otherwise it drops off after ~180 days).
5. **Keep the laptop awake when plugged in** (PowerShell as Administrator):
   ```powershell
   powercfg /change standby-timeout-ac 0
   powercfg /change hibernate-timeout-ac 0
   powercfg /setacvalueindex SCHEME_CURRENT SUB_BUTTONS LIDACTION 0   # lid closed = do nothing
   powercfg /setactive SCHEME_CURRENT
   ```
6. **Battery care**: the laptop will be plugged in 24/7 — turn on the manufacturer's battery charge limit (often 80%) if it has one.
7. **Windows Update**: set Active Hours. Reboots are fine — the service and Tailscale start on their own.

## 9. Build and first run (once Phase 1 code exists)

```powershell
cd web;    npm ci; npm run build                  # builds the UI into server/internal/webui
cd ..\server
go build -o ashcloud.exe .\cmd\ashcloud
.\ashcloud.exe init                               # creates C:\AshCloud, config, database, tables
.\ashcloud.exe user create --admin aashish        # asks for a password
.\ashcloud.exe serve                              # test on the laptop: http://127.0.0.1:8080
tailscale serve --bg http://127.0.0.1:8080        # phone URL: https://<laptop>.<tailnet>.ts.net
.\ashcloud.exe service install                    # run at boot under MY Windows account
.\ashcloud.exe service start
```

The service runs under **my Windows account, not SYSTEM**, so file permissions
(and the Phase 4 terminal) act as me, never with SYSTEM's extra power.

On the phone: open `https://<laptop>.<tailnet>.ts.net` → log in → Share →
**Add to Home Screen**.

Updating later: `git pull` → rebuild → `ashcloud service restart`. The server
shuts down gracefully, finishing requests already in progress.

---

## 10. Build order

Rule: build the thinnest possible path from the phone to the disk and back
first, then make each part deeper. Every step ends with something I can try on
my phone.

### Phase 1 — A working server
| Step | Build | Done when |
|---|---|---|
| 1 ✅ | Go module, `ashcloud serve`, `GET /healthz`, `tailscale serve` | Phone on **mobile data** shows `ok` at the `ts.net` address. Proves the whole network path. |
| 2 (built) | Config file, `ashcloud init`, SQLite + migrations, `user create`, `GET /readyz` | `/readyz` reports DB + storage OK. |
| 3 | Login / logout / `me`, sessions, full middleware chain, login rate limit | Can log in from the phone; wrong passwords get rate-limited; cookie is HttpOnly/Secure/Strict. |
| 4 | List folder, download with `Range` | Browse folders, open a photo, scrub through a video on the phone. |
| 5 | Upload, new folder, move/rename, delete | Upload a photo from the camera roll; rename it; delete it. |
| 6 | Web UI: login screen, file browser, upload with progress bar, installable to home screen | Everything above works by tapping, no typing URLs. |
| 7 | Windows service + CI (lint, tests, build `ashcloud.exe`) | Reboot the laptop → it comes back by itself; CI is green. |

**Phase 1 is done when:** with Wi-Fi off on the phone, I can log in, upload,
browse, download and delete; a laptop reboot needs no action from me; all tests
pass.

### Phase 2 — Always on, safe to rely on
Trash (undo deletes) · resumable uploads (tus) · see/revoke logged-in devices ·
change password · Status screen (CPU, RAM, disk, battery, Tailscale) · metrics
on `:9090` · log rotation · nightly backups (§13) · polished home-screen app.

### Phase 3 — Fast with photos and videos
Thumbnails (iPhone HEIC via `ffmpeg`) · file-name search (SQLite FTS5) · folder
download as zip · live updates over WebSocket · `fsnotify` watcher so files
added on the laptop are indexed immediately.

### Phase 4 — Control the laptop from the phone
Web terminal (PowerShell through Windows ConPTY, `xterm.js` in the browser) ·
Ash integration: `POST /api/v1/ash` runs the Python Ash code to turn plain
English into a command, shown for confirmation before it runs.

### Phase 5 — Optional, only if needed
Passkeys (Face ID / fingerprint login) · share links for other people (needs
public access: Tailscale Funnel or Cloudflare Tunnel) · more users (§15).

---

## 11. Speed

| Target | Goal | How |
|---|---|---|
| Server time per API call | p95 < 5 ms | Go, SQLite WAL with prepared statements, in-memory session cache. |
| App opening on the phone | < 1 s on 4G, instant once installed | JS bundle < 100 KB; hashed asset names with `Cache-Control: immutable`; assets pre-compressed (brotli/gzip) at build time; home-screen app keeps the shell cached. |
| Round trips | 1 per screen | Each screen's endpoint returns everything that screen needs. |
| Network path | Direct, not relayed | `tailscale ping <phone>` should show a direct connection, not `DERP`. If relayed: enable UPnP on the router or allow UDP 41641. |
| Big files | Never held in memory | Uploads and downloads stream; `Range` for video seeking; thumbnails instead of full photos in lists (P3). |
| Measured, not guessed | Always visible | `Server-Timing` header on responses + Prometheus latency histograms per route. |

**Hard limit:** downloads to the phone can't be faster than the home
internet's *upload* speed (often 10–50 Mbps). Uploads from the phone are limited
by the mobile connection.

---

## 12. Security

Layers, outermost first:
1. **Tailscale** — only my own signed-in devices can reach the laptop at all. Nothing is exposed to the public internet.
2. **HTTPS** — real certificate from `tailscale serve`; HSTS on.
3. **Server only listens on `127.0.0.1`** — unreachable except through Tailscale.
4. **Login** — argon2id password hash; 5 failures per 15 minutes then locked out; every login recorded in `audit_log`.
5. **Sessions** — random 256-bit token in an HttpOnly, Secure, `SameSite=Strict` cookie; only its hash is stored; can be revoked per device; expire after 30 days.
6. **CSRF** — Strict cookie + required custom header on every change.
7. **Files** — `os.Root` confines all file access to my storage folder; Windows name rules enforced; served files are sandboxed (§7).
8. **Headers** — strict CSP (no inline scripts), `nosniff`, no framing.
9. **Ops endpoints** — metrics and profiling only on `127.0.0.1:9090`, never forwarded by Tailscale.
10. **Phase 4 terminal** — off by default, turned on in config; runs as my account, never SYSTEM.

---

## 13. Operations

- **Logs**: JSON via `slog` to `C:\AshCloud\logs\`, rotated daily, kept 14 days. Every line has the request ID.
- **Health**: `/healthz` and `/readyz`; the Status screen shows them too.
- **Metrics**: Prometheus format on `127.0.0.1:9090/metrics` (request latency, errors, bytes in/out, upload count).
- **Backups**:
  - Database: `ashcloud backup` runs nightly (built-in scheduler) using SQLite `VACUUM INTO` → `C:\AshCloud\backups\` (keeps 14).
  - Files: `restic` to an external drive and/or Backblaze B2, scheduled with Windows Task Scheduler.
  - Restore is tested once per phase, not assumed.
- **Shutdown**: graceful — stop accepting, finish in-flight requests (30 s), close the DB cleanly.

---

## 14. Testing and CI

- **Go**: unit tests per package; HTTP integration tests with `httptest` against a temp storage folder and temp SQLite; tests for every path-escape and bad-name case.
- **Web**: `svelte-check` + `vitest`; Playwright end-to-end tests at iPhone screen size (login → upload → rename → delete).
- **CI** (GitHub Actions, `windows-latest`, since that's where it runs): `go vet`, `golangci-lint`, `go test ./...`, frontend checks, build `ashcloud.exe` as an artifact.

---

## 15. If it ever needs more users or a bigger machine

Already in place from day one so nothing needs rewriting:
- `users` table with roles, and a per-user storage folder (`storage\users\<id>\`).
- The server keeps nothing important in memory: state is in the DB and the Storage interface.

Then, step by step, only as needed:
1. Add user management, quotas and share links (Phase 5).
2. Add public access without the Tailscale app: Cloudflare Tunnel (needs a domain) or Tailscale Funnel, in front of the same server.
3. Move off the laptop: same `ashcloud.exe` on a mini PC or rented server.
4. Bigger still: SQLite → Postgres (sqlc supports both; keep SQL portable), local disk → S3-compatible storage behind the same `Storage` interface, several copies of the server behind a load balancer.

---

## 16. Known limits and risks

| Risk | Mitigation |
|---|---|
| Laptop sleeps, is closed, or loses power | Power settings in §8; service auto-starts after any reboot. |
| Home internet upload speed caps download speed | Thumbnails and streaming; nothing else can fix it. |
| Tailscale falls back to a relay (slower) | Check with `tailscale ping`; enable UPnP / allow UDP 41641. |
| Battery wear from being plugged in 24/7 | Manufacturer charge limit (§8). |
| Laptop disk dies or laptop is stolen | Off-laptop backups (§13); Windows BitLocker on. |
| Forgotten password | `ashcloud user reset-password aashish` on the laptop itself. |
