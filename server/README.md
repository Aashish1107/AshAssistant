# Ash Cloud server

The Go server behind Ash Cloud. See [`docs/PLAN.md`](../docs/PLAN.md) for the
full plan.

**Current stage: Phase 1, step 3.** You can now log in and out from your
phone, and a device stays logged in while you keep using it. Every request
passes through the server's full set of security checks.

The login screen at `/` is a simple **test page**. The real app replaces it
in step 6.

---

## Update the Windows laptop

### 1. Get the latest code and build

Stop the running server first (**Ctrl+C** in its window). Then, in PowerShell:

```powershell
cd AshAssistant
git pull
cd server
go build -o ashcloud.exe .\cmd\ashcloud
```

(First time on this laptop? See [First-time setup](#first-time-setup) below
instead.)

### 2. Start the server

```powershell
.\ashcloud.exe serve
```

The first start after updating prints `database schema updated versions=[2]`.
That's the new sessions and audit-log tables being added automatically. Your
account and settings are kept.

### 3. Log in on the laptop

Open <http://127.0.0.1:8080/>, enter your username (`aashish`) and password,
and tap **Log in**. You'll see:

> Logged in as **aashish** (admin).
> This device stays logged in until <date 30 days from now>, and longer if you keep using it.

Reload the page: you're still logged in. Tap **Log out**, reload: you're logged out.

### 4. Log in from the phone

With **Wi-Fi off** and the **Tailscale app on**, open:

```
https://<laptop-name>.<tailnet-name>.ts.net/
```

Log in the same way. **Step 3 is done** when the phone shows
"Logged in as aashish", stays logged in after you close and reopen the
browser, and logs out when you tap **Log out**.

---

## How logging in works

| | |
|---|---|
| **Staying logged in** | Each device gets its own session. It lasts 30 days from the **last time you used it**, so a phone you use every week stays logged in indefinitely. Change this with `session_ttl` in `ashcloud.toml`. |
| **Logging out** | **Log out** ends that device's session only. Your other devices stay logged in. |
| **Wrong passwords: admin accounts** | **Never locked out.** That's your choice for admin accounts. Each failed attempt is still written to the server log (`failed login ... lockout_applies=false`) and the audit log. |
| **Wrong passwords: other accounts** | 5 wrong passwords from one device within 15 minutes blocks that device for those accounts until the oldest attempt is 15 minutes old. Unknown usernames count too. |
| **Speed limit on password checks** | Each check uses 64 MB of memory, so at most 4 run at once and extra attempts wait their turn. They're never refused. This protects the laptop's memory. It also caps password guessing at roughly 40 attempts a second. |
| **User IDs** | Your admin account is `1`. Every account created after it gets a random 21-character Nano ID, like `V1StGXR8_Z5jdHi6B-myT`. Each account's files live in `C:\AshCloud\storage\users\<id>\`. |
| **Forgot your password** | On the laptop: `.\ashcloud.exe user reset-password aashish`. It also logs you out on every device. |

### Security, in short

- **Password storage.** Passwords are stored only as argon2id hashes.
- **Session cookie.** It holds a random 256-bit token, and only its SHA-256
  hash is in the database. The cookie is:
  - **HttpOnly**: page scripts can't read it.
  - **Secure**: sent only over HTTPS. Browsers make an exception for `127.0.0.1`.
  - **SameSite=Strict**: never sent when another website starts the request.
- **Forged requests (CSRF).** Requests that change something must come from
  this site, checked using the browser's `Sec-Fetch-Site`/`Origin` headers. They
  must also carry the header `X-Requested-With: ashcloud`.
- **Every response** carries a strict Content-Security-Policy (only this
  server's own scripts), plus HSTS, `nosniff`, no framing and no referrer.
- **Error messages.** Errors never show internal details. Each response
  has an `X-Request-Id`, and the server log has the full story under that ID.
- **Audit log.** Logins, failed logins, blocked attempts, logouts and password
  resets are recorded in the `audit_log` table, with time and device address.

---

## Commands

```
ashcloud init [--data-dir DIR]          create settings, database and files folder (DIR: C:\AshCloud)
ashcloud user create [--admin] NAME     add a user; asks for the password
ashcloud user reset-password NAME       set a new password; logs that user out everywhere
ashcloud serve [--listen ADDR]          start the server (Ctrl+C to stop)
ashcloud version                        print the version
ashcloud help                           show help
```

`user` and `serve` read `C:\AshCloud\ashcloud.toml` unless you pass
`--config FILE`.

## Endpoints

| Endpoint | Login needed | Response |
|---|---|---|
| `GET /` | no | Login test page (the real app arrives in step 6). |
| `GET /healthz` | no | `200 ok`: the process is up. |
| `GET /readyz` | no | `200` / `503` JSON: database and files folder both work. |
| `POST /api/v1/auth/login` | no | `{"username", "password"}` → `200` + session cookie; `401` wrong username or password; `429` too many wrong passwords (not for admins). |
| `POST /api/v1/auth/logout` | no | `204`; ends this device's session if there is one. |
| `GET /api/v1/me` | **yes** | `200` `{"user": {...}, "expires_at": ...}`, or `401`. |

Errors always look like
`{"error": {"code": "invalid_credentials", "message": "Wrong username or password"}}`.
`POST` requests need `Content-Type: application/json` and
`X-Requested-With: ashcloud`.

---

## Settings: `C:\AshCloud\ashcloud.toml`

```toml
listen = '127.0.0.1:8080'       # keep it on 127.0.0.1 so only Tailscale can reach it
data_dir = 'C:\AshCloud'        # database and other server files
storage_dir = 'C:\AshCloud\storage'   # your files, one subfolder per user
log_level = 'info'              # debug, info, warn or error
session_ttl = '720h'            # how long an unused device stays logged in (720h = 30 days, at least 1h)
```

Restart the server after editing. A setting that's missing uses the default
shown. A misspelled one stops the server with a message naming it.

---

## Troubleshooting

| Problem | Fix |
|---|---|
| "Wrong username or password" | Check for typos. The username isn't case-sensitive; the password is. If you've forgotten it: `.\ashcloud.exe user reset-password aashish`. |
| "Too many wrong passwords from this device" | Only happens for non-admin accounts. Wait the number of minutes shown. |
| "Requests from other websites aren't allowed" when logging in | Your browser is too old to send the `Sec-Fetch-Site` header (it's in Safari 16.4+, Chrome 76+). Update the phone's browser. |
| Logged out unexpectedly | Something ended the session. Either the password was reset, the device wasn't used for 30 days, or the browser's cookies were cleared. Log in again. |
| Page says "Couldn't reach the server" | The server isn't running, or the phone's Tailscale app is off. |
| Phone: `502 Bad Gateway` | The server isn't running. Start it. |
| Phone: site can't be found | The Tailscale app is off on the phone, or MagicDNS is off. |
| `/readyz` shows `"storage": "failed"` or `"database": "failed"` | The server window says why. Check `C:\AshCloud` and the files folder exist and aren't read-only. |
| `can't listen on 127.0.0.1:8080` | Something else uses port 8080. Change `listen` in `ashcloud.toml`, restart, and re-run `tailscale serve --bg http://127.0.0.1:<new port>`. |
| `go build` can't write `ashcloud.exe` | The old server is still running. Press Ctrl+C in its window, then build again. |
| An error mentions `request abc123...` | Search the server window for `request_id=abc123...` to see what went wrong. |

---

## First-time setup

Only needed on a laptop that has never run Ash Cloud.

1. **Tools:** `winget install GoLang.Go Git.Git tailscale.tailscale`, then
   open a new PowerShell window. You need Go 1.26 or newer (`go version`).
   If it's older, building downloads the right version by itself.
2. **Code:**
   ```powershell
   git clone https://github.com/Aashish1107/AshAssistant.git
   cd AshAssistant
   git checkout claude/local-cloud-server-setup-mmdj55
   cd server
   go build -o ashcloud.exe .\cmd\ashcloud
   ```
3. **Settings, database, files folder:** `.\ashcloud.exe init`
4. **Your account:** `.\ashcloud.exe user create --admin aashish`. It
   asks for a password of 12+ characters, and nothing shows while you type.
5. **Tailscale:**
   - **Laptop:** open Tailscale and sign in; in the tray menu → Preferences,
     turn on **Run unattended**.
   - **Phone:** install the Tailscale app and sign in with the same account.
   - **Admin console** (<https://login.tailscale.com/admin>):
     - DNS: turn on **MagicDNS** and **HTTPS Certificates**.
     - Machines: on the laptop, choose **Disable key expiry**.
   - **Laptop:** run `tailscale serve --bg http://127.0.0.1:8080`. It prints
     your `https://<laptop-name>.<tailnet-name>.ts.net` address.
6. Continue with [step 2 above](#2-start-the-server).

Files on the laptop:

```
C:\AshCloud\
├── ashcloud.toml        settings
├── ashcloud.db          database: users, sessions, audit log (+ -wal / -shm files while running)
└── storage\users\
    ├── 1\               your files (admin)
    └── <nano id>\       each other account's files
```

---

## Development

```powershell
go vet ./...
go test ./...
```

The database code in `internal/db` (`db.go`, `models.go`, `*.sql.go`) is
generated by [sqlc](https://sqlc.dev) from `internal/db/queries/*.sql` and
`internal/db/migrations/*.sql`. After changing either:

```powershell
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest   # once
sqlc generate
```

Schema changes are new numbered files in `internal/db/migrations`. The
server applies any it hasn't run yet every time it starts.

Code layout:

```
cmd/ashcloud/         CLI: init, user create/reset-password, serve, version, help
internal/config/      ashcloud.toml: defaults, loading, validation, writing
internal/db/          SQLite: opening, migrations/, queries/, sqlc output
internal/auth/        passwords (argon2id), login, sessions, lockout, audit log
internal/storage/     files folder: per-user folders, writability check
internal/httpapi/     routes, middleware chain, JSON errors, server start/stop
internal/webui/       the browser page, built into the binary (test page until step 6)
```
