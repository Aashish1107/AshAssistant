# Ash Cloud server

The Go server behind Ash Cloud. See [`docs/PLAN.md`](../docs/PLAN.md) for the
full plan.

**Current stage: Phase 1, step 2.** The server now has:
- a settings file,
- a database,
- your user account (with your password stored as a secure hash),
- `/readyz`, which proves the database and your files folder are working.

Login comes in step 3.

---

## Set up or update on the Windows laptop

### 1. Tools (once)

In PowerShell:

```powershell
winget install GoLang.Go Git.Git tailscale.tailscale
```

Open a **new** PowerShell window afterwards. The server needs **Go 1.26 or
newer**; check with `go version`. If yours is older, either run
`winget upgrade GoLang.Go`, or just build: Go downloads the version it needs
by itself the first time.

### 2. Get the latest code and build

First time:

```powershell
git clone https://github.com/Aashish1107/AshAssistant.git
cd AshAssistant
git checkout claude/local-cloud-server-setup-mmdj55
```

Already cloned (from step 1): `cd AshAssistant`, then `git pull`.

Then build:

```powershell
cd server
go build -o ashcloud.exe .\cmd\ashcloud
```

If the step 1 server is still running, stop it with **Ctrl+C** first.
Windows won't let you replace an `.exe` that's running.

### 3. Create the settings, database and files folder (once)

```powershell
.\ashcloud.exe init
```

```
Settings:  C:\AshCloud\ashcloud.toml (created)
Database:  C:\AshCloud\ashcloud.db (schema updated to version 1)
Files:     C:\AshCloud\storage

Next, create your login:
  ashcloud user create --admin <your-name>
```

Running `init` again is safe. It never overwrites your settings and only adds
whatever is missing.

### 4. Create your account (once)

```powershell
.\ashcloud.exe user create --admin aashish
```

It asks for a password twice. Use at least 12 characters. Nothing appears
on screen while you type; that's normal. Only a secure hash (argon2id) is
saved, never the password itself. Your files will live in
`C:\AshCloud\storage\users\1\`.

### 5. Start the server

```powershell
.\ashcloud.exe serve
```

It now reads its settings from `C:\AshCloud\ashcloud.toml`, so `--listen`
isn't needed anymore. Leave the window open.

### 6. Check it on the laptop

Open <http://127.0.0.1:8080/readyz>:

```json
{
  "status": "ok",
  "checks": {
    "database": "ok",
    "storage": "ok"
  }
}
```

### 7. Check it from the phone

You set up `tailscale serve` in step 1, and it's still active. (If not, see
[Tailscale setup](#tailscale-setup-once) below.)

On the phone, with **Wi-Fi off** and the **Tailscale app on**, open:

```
https://<laptop-name>.<tailnet-name>.ts.net/readyz
```

**Step 2 is done** when the phone shows `"status": "ok"` with both checks `ok`.

---

## What `/healthz` and `/readyz` mean

| Endpoint | Answers | Means |
|---|---|---|
| `/healthz` | `ok` | The server process is running. |
| `/readyz` | `200` + `"status": "ok"` | The server can do real work: the database answers **and** a file can be written to your files folder. |
| `/readyz` | `503` + `"status": "unavailable"` | Something is wrong. The JSON says which check `failed`; the server window shows why. |

---

## Files on the laptop

```
C:\AshCloud\
├── ashcloud.toml        settings (edit with Notepad, then restart the server)
├── ashcloud.db          database (plus ashcloud.db-wal / -shm while running)
└── storage\
    └── users\1\         your files
```

`ashcloud.toml`:

```toml
# Address the server listens on. Keep it on 127.0.0.1 so only Tailscale can reach it.
listen = '127.0.0.1:8080'
# Folder for the database and other server files.
data_dir = 'C:\AshCloud'
# Folder that holds your files (one subfolder per user).
storage_dir = 'C:\AshCloud\storage'
# How much to log: debug, info, warn or error.
log_level = 'info'
```

To keep files on another drive, change `storage_dir` (for example to
`'D:\AshCloud'`), move the `users` folder there, and restart the server. A
misspelled setting stops the server with a message naming it.

---

## Tailscale setup (once)

Skip this if you did it in step 1.

1. **Laptop:** open Tailscale from the Start menu and sign in. Tray icon →
   **Preferences** → turn on **Run unattended**.
2. **Phone:** install the **Tailscale** app, sign in with the **same
   account**, switch it on.
3. **Admin console** (<https://login.tailscale.com/admin>):
   - **DNS** page: turn on **MagicDNS** and **HTTPS Certificates**.
   - **Machines** page: on the laptop's `…` menu, choose **Disable key expiry**.
4. **Laptop**, in a second PowerShell window:
   ```powershell
   tailscale serve --bg http://127.0.0.1:8080
   ```
   It prints your `https://<laptop-name>.<tailnet-name>.ts.net` address. If it
   prints a link instead, open it, approve, and run the command again.

Useful: `tailscale serve status` shows the address, `tailscale serve reset`
stops forwarding, and `tailscale ping <phone-name>` checks the connection
is direct (fast) rather than `via DERP` (relayed).

---

## Troubleshooting

| Problem | Fix |
|---|---|
| `no settings file at C:\AshCloud\ashcloud.toml` | Run `.\ashcloud.exe init` first. |
| `go build` says it needs a newer Go | Run `winget upgrade GoLang.Go` and open a new PowerShell window. |
| `go build` can't write `ashcloud.exe` | The old server is still running. Press Ctrl+C in its window, then build again. |
| `a user named "aashish" already exists` | Your account is already there. Nothing to do. |
| `password must be at least 12 characters` / `didn't match` | Run `user create` again. Nothing was saved. |
| `/readyz` shows `"storage": "failed"` | The files folder is missing or can't be written. The server window says why. If `storage_dir` is on another drive, make sure it's plugged in. |
| `/readyz` shows `"database": "failed"` | The server window shows the error. Check `C:\AshCloud` still exists and isn't read-only. |
| `can't listen on 127.0.0.1:8080` | Something else uses port 8080. Change `listen` in `ashcloud.toml` (for example `'127.0.0.1:8081'`), restart, and re-run `tailscale serve --bg http://127.0.0.1:8081`. |
| `has a setting Ash Cloud doesn't know` | There's a typo in `ashcloud.toml`. The message names the line. |
| Phone: `502 Bad Gateway` | The server isn't running. Start it (step 5). |
| Phone: site can't be found | The Tailscale app is off on the phone, or MagicDNS is off. |
| Forgot your password | Password reset comes with login in step 3. |

---

## Commands

```
ashcloud init [--data-dir DIR]          create settings, database and files folder (DIR: C:\AshCloud)
ashcloud user create [--admin] NAME     add a user; asks for the password
ashcloud serve [--listen ADDR]          start the server (Ctrl+C to stop)
ashcloud version                        print the version
ashcloud help                           show help
```

`user` and `serve` read `C:\AshCloud\ashcloud.toml` unless you pass
`--config FILE`.

## Endpoints

| Endpoint | Response |
|---|---|
| `GET /healthz` | `200 ok`: the process is up. |
| `GET /readyz` | `200` / `503` JSON: database and files folder both work. |

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

Schema changes are new numbered files in `internal/db/migrations`; the
server applies any it hasn't run yet every time it starts.

Build with a version stamp:

```powershell
go build -ldflags "-X main.version=v0.2.0" -o ashcloud.exe .\cmd\ashcloud
```

Code layout:

```
cmd/ashcloud/         CLI: init, user create, serve, version, help
internal/config/      ashcloud.toml: defaults, loading, validation, writing
internal/db/          SQLite: opening, migrations/, queries/, sqlc output
internal/auth/        password hashing (argon2id)
internal/storage/     files folder: per-user folders, writability check
internal/httpapi/     routes (/healthz, /readyz), request logging, server start/stop
```
