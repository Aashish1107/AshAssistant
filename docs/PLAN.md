# Ash Cloud — Plan

A private cloud that runs on my Windows laptop. I use it **only through a web
browser**, from my phone, from anywhere. Built for one person, but designed so
it could grow to more users without a rewrite.

Status: **Phase 1, step 5 built** — upload (fast and resumable), new folder,
rename/move, delete to a trash with Undo. Resumable uploads and trash/restore
were pulled forward from Phase 2. Earlier steps: browse folders, open or stream
files (video scrubbing via Range requests), Nano ID user IDs, and, pulled
forward from Phase 3, a video player with −10 s / +10 s and audio track
switching. Step 5 is done once a photo from the phone uploads, renames and
deletes ([`server/README.md`](../server/README.md)). Next: step 6.

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
| Media | `ffmpeg` / `ffprobe` (installed with `winget install Gyan.FFmpeg`), run as separate programs | Lists a video's audio tracks and extracts one for the player. Optional: without it, only audio track switching is off. |
| Later phases | `gopsutil` + `distatus/battery` (status), `coder/websocket` (live updates, terminal), Windows ConPTY (terminal), `xterm.js` (terminal UI), `fsnotify` (file watcher), `go-webauthn` (passkeys, optional) | |

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
2. **Access log + metrics** — method, route, status, duration, bytes, client address, user.
3. **Panic recovery** — a bug returns 500 (and is logged with a stack), never kills the server.
4. **Security headers** — CSP, HSTS, `X-Content-Type-Options: nosniff`, `Referrer-Policy`, `frame-ancestors 'none'`.
5. **Limits** — request timeouts; body size limits on JSON endpoints (uploads are streamed and exempt).
6. **Rate limit** — login: 5 failures per 15 minutes per client. **Admin accounts are exempt by the owner's choice** (never locked out; failures are still logged and audited). Separately, at most 4 password checks run at once (each uses 64 MiB); extra attempts wait, never fail.
7. **Session auth** — cookie → SHA-256 → `sessions` table (a sub-millisecond indexed lookup; add an in-memory cache only if measurements ever call for it). Using a session pushes its expiry forward, at most once an hour.
8. **CSRF check** — `SameSite=Strict` cookie, Go's built-in `http.CrossOriginProtection` (`Sec-Fetch-Site`/`Origin`), **and** a required `X-Requested-With: ashcloud` header on every non-GET request.
9. **Role check** — everything is `admin` for now; the check is added with the first admin-only route.
10. **Handler** → **Service** → **Storage / DB**.

Handlers only parse input and write output. Services hold the rules.
Files are only touched through the `Storage` interface (local disk via
`os.Root` today; could become S3 later without changing handlers).

### Example: uploading a photo, end to end
1. Tap **Upload** in the app. The browser derives the upload's ID from the folder and the file's name, size and date (SHA-256), so picking the same file again later finds the same upload.
2. `PUT /api/v1/uploads/{id}?path=/Photos/IMG_1.jpg&size=…&modified=…` with `Upload-Offset: 0`, the session cookie and the `X-Requested-With` header; the body is the file itself (progress bar from XHR upload events). Files of 8 MB or more first send `POST` to the same URL: it checks the folder and free space and says where an unfinished upload stopped.
3. Phone Tailscale → WireGuard → laptop Tailscale terminates HTTPS → forwards to `127.0.0.1:8080`.
4. Middleware chain: request ID → log → recover → headers → cross-origin → CSRF header → auth (no timeout for uploads; a 60 s idle timeout instead).
5. `drive.Service.Write`: checks the name against Windows rules → records the upload (`uploads` table) → streams the body into `.ashcloud-uploads/<id>.part` inside the user's folder (via `os.Root`), 1 MiB per write, flushing to disk every 32 MiB in the background → on the last byte: flush, rename into place (`IMG_1 (1).jpg` if taken), set the photo's date, delete the `uploads` row, write `audit_log`.
6. `201 Created` + the new file's JSON → the app reloads the folder.

If the connection drops, the part file keeps what arrived; the app waits for the network, asks (`POST`) where to continue and sends only the rest. A resend on a new connection takes over from a stalled one at once.

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
└── storage\users\1\         # MY FILES (per-user folder from day one; others: users\<nano id>\)
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
users     (id TEXT NOT NULL PRIMARY KEY,         -- "1" for the first admin, otherwise a 21-char Nano ID
           username TEXT UNIQUE NOT NULL,
           password_hash TEXT NOT NULL,           -- argon2id
           role TEXT NOT NULL CHECK (role IN ('admin', 'user')),
           created_at INTEGER NOT NULL) STRICT

sessions  (id INTEGER PRIMARY KEY,                                  -- step 3 ✅
           user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
           token_hash BLOB UNIQUE NOT NULL,      -- SHA-256 of the cookie, never the token itself
           user_agent TEXT NOT NULL, ip TEXT NOT NULL,
           created_at INTEGER NOT NULL, last_seen_at INTEGER NOT NULL,
           expires_at INTEGER NOT NULL) STRICT

audit_log (id INTEGER PRIMARY KEY,                                  -- step 3 ✅
           user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
           action TEXT NOT NULL,                 -- login, login_failed, login_blocked, logout, password_reset, …
           detail TEXT NOT NULL DEFAULT '',      -- e.g. the username a failed login tried, or a file path
           ip TEXT NOT NULL, at INTEGER NOT NULL) STRICT
```

```sql
uploads   (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,  -- step 5 ✅
           id TEXT NOT NULL,                      -- chosen by the browser, see §3
           path TEXT NOT NULL, size INTEGER NOT NULL,
           modified INTEGER NOT NULL,             -- the original file's date, Unix ms
           synced INTEGER NOT NULL DEFAULT 0,     -- bytes known to be on disk (after a power cut, resume here)
           created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
           PRIMARY KEY (user_id, id)) STRICT

trash     (id TEXT NOT NULL PRIMARY KEY,          -- Nano ID, also the folder name in .ashcloud-trash
           user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,  -- step 5 ✅
           original_path TEXT NOT NULL, is_folder INTEGER NOT NULL,
           deleted_at INTEGER NOT NULL) STRICT    -- emptied after 30 days
```

Later phases add: `file_index` + FTS5 search table and `thumbnails` (P3),
`passkeys` (P5).

**User IDs.** The first admin created on an empty server is `"1"`; every
other user gets a random 21-character Nano ID (same alphabet and length as
the npm `nanoid` package), so IDs can't be guessed or used to count accounts.
Each user's files live in `storage\users\<id>\`.

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
| `POST /api/v1/uploads/{id}?path=&size=&modified=` | Start an upload / ask where it stopped; checks folder and free space | uploads, Storage | 1 ✅ |
| `PUT /api/v1/uploads/{id}?…` + `Upload-Offset` | Upload, streamed to disk, resumable; auto-renames `photo (1).jpg` on clash | uploads, Storage, audit | 1 ✅ |
| `DELETE /api/v1/uploads/{id}` | Cancel an upload | uploads, Storage | 1 ✅ |
| `POST /api/v1/files/folder` | Create folder `{path}` | Storage, audit | 1 ✅ |
| `POST /api/v1/files/move` | Rename or move `{from, to}`; never overwrites | Storage, audit | 1 ✅ |
| `DELETE /api/v1/files?path=` | Delete: moves to the trash, returns `trash_id` | trash, Storage, audit | 1 ✅ |
| `POST /api/v1/trash/restore` | Undo a delete `{id}` | trash, Storage, audit | 1 ✅ (early) |
| `GET /api/v1/sessions` · `DELETE /api/v1/sessions/{id}` | See / log out my devices | sessions | 2 |
| `POST /api/v1/auth/password` | Change password; logs out other devices | users, sessions | 2 |
| `GET /api/v1/trash` · `DELETE /api/v1/trash` | Trash: list, empty now | trash, Storage | 2 |
| `GET /api/v1/system/status` | CPU, RAM, disk, battery, uptime, Tailscale status | OS | 2 |
| `GET /metrics` (port 9090 only) | Prometheus metrics: latency, errors, bytes | — | 2 |
| `GET /api/v1/media/info?path=` | A video's audio tracks (ffprobe) and whether they can be switched | ffprobe, memory cache | 3 ✅ (early) |
| `POST /api/v1/media/audio` · `GET /api/v1/media/audio?path=&track=` | Extract one audio track (ffmpeg; copy, or AAC for AC3/DTS…) and play it with `Range` | ffmpeg, `cache\audio\` | 3 ✅ (early) |
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
| Files | `GET /files`, `GET /files/content`, `POST`/`PUT`/`DELETE /uploads/{id}`, `POST /files/folder`, `POST /files/move`, `DELETE /files`, `POST /trash/restore` (Undo) (+ P3 `/files/thumbnail`, `/search`, `/files/zip`, `WS /events`) |
| Trash (P2) | `GET /trash`, `POST /trash/restore`, `DELETE /trash` |
| Status (P2) | `GET /system/status` (+ P3 `WS /events` for live numbers) |
| Settings (P2) | `GET /sessions`, `DELETE /sessions/{id}`, `POST /auth/password`, `POST /auth/logout` |
| Terminal (P4) | `WS /terminal`, `POST /ash` |

### File download safety
User files are served with `Content-Security-Policy: sandbox allow-same-origin`
and `nosniff`. That sandbox disables all scripts, forms and pop-ups, so an
uploaded `.html` or `.svg` can never run scripts as the app. `allow-same-origin`
keeps the file's real origin, which a video opened directly needs: without it,
the player's own requests would lose the `SameSite=Strict` session cookie. PDFs
skip the sandbox because browser PDF viewers refuse to run inside one.
`?inline=1` shows images, video, audio and PDF; text and code are shown as plain
text; everything else downloads.

---

## 8. One-time laptop setup (Windows)

1. **Install tools** (PowerShell):
   ```powershell
   winget install GoLang.Go OpenJS.NodeJS.LTS Git.Git tailscale.tailscale Gyan.FFmpeg
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
| 3 (built) | Login / logout / `me`, sessions, full middleware chain, login rate limit (admins exempt), login test page, `user reset-password` | Can log in from the phone; wrong passwords get rate-limited; cookie is HttpOnly/Secure/Strict. |
| 4 (built) | List folder, download with `Range` | Browse folders, open a photo, scrub through a video on the phone. |
| 5 (built) | Upload (resumable), new folder, move/rename, delete to trash + Undo | Upload a photo from the camera roll; rename it; delete it. |
| 6 | Web UI: login screen, file browser, upload with progress bar, installable to home screen | Everything above works by tapping, no typing URLs. |
| 7 | Windows service + CI (lint, tests, build `ashcloud.exe`) | Reboot the laptop → it comes back by itself; CI is green. |

**Phase 1 is done when:** with Wi-Fi off on the phone, I can log in, upload,
browse, download and delete; a laptop reboot needs no action from me; all tests
pass.

### Phase 2 — Always on, safe to rely on
**Built early (step 5):** resumable uploads (own protocol, simpler than tus:
one request per file, resume by asking for the offset) and trash with Undo.
Still to do: a Trash screen (list, restore, empty) · see/revoke logged-in devices ·
change password · Status screen (CPU, RAM, disk, battery, Tailscale) · metrics
on `:9090` · log rotation · nightly backups (§13) · polished home-screen app.

### Phase 3 — Fast with photos and videos
**Built early:** video player with −10 s / +10 s, full screen with visible
controls, and audio track switching. Chrome on Android plays only a video's
first audio track, so the server extracts the chosen track with ffmpeg (cached
in `C:\AshCloud\cache\audio\`, pruned after 30 days unused) and the player
plays it in sync with the muted video. See `server/README.md` → *The video player*.

Still to do: thumbnails (iPhone HEIC via `ffmpeg`) · file-name search (SQLite FTS5) · folder
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
| Uploads | Limited only by the network | One request per file, 3 files at once, body streamed straight to disk in 1 MiB writes with background flushes every 32 MiB (measured: 1 GB in ~0.8 s locally; 100 × 3 MB photos in 1.4 s). Resume sends only the missing bytes. |
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
4. **Login** — argon2id password hash; non-admin accounts locked out after 5 failures per 15 minutes per device; **admin accounts never locked out** (owner's choice — the password's strength and the 4-at-a-time check limit are what slow guessing); every login, failure and block recorded in `audit_log`.
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
