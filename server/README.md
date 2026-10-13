# Ash Cloud server

The Go server behind Ash Cloud. See [`docs/PLAN.md`](../docs/PLAN.md) for the
full plan.

**Current stage: Phase 1, step 5.** From your phone you can log in, browse
your files, open photos and documents, and play videos (drag the slider to
any point without downloading the rest). You can also **upload** files, fast
and resumable, make folders, and rename, move and delete (with Undo).

The page at `/` is a simple **test page**. The real app replaces it in step 6.

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

The first start after updating prints `database schema updated` with the new
versions (`[4]` if you're coming from step 4). Those are added automatically.
Your account, password, settings, sessions and files are kept, and your admin
account's ID stays `1`.

### 2b. Install ffmpeg (once, for switching audio tracks)

```powershell
winget install Gyan.FFmpeg
```

Then **open a new PowerShell window** (so it can find ffmpeg) and start the
server from there. The server window should say
`ffmpeg found - audio track switching is on`. Without ffmpeg everything else
still works; the player's Audio menu just explains that switching is off.

### 3. Upload from the phone

With **Wi-Fi off** and the **Tailscale app on**, open
`https://<laptop-name>.<tailnet-name>.ts.net/` and log in if asked. Then:

1. Tap **New folder**, call it `Camera`, and open it.
2. Tap **Upload** and pick a few photos and a video from your gallery. They
   go up three at a time, each with its own progress bar, and the total
   speed and time left at the top.
3. Tap **⋯** next to a photo → **Rename**. Then **⋯** → **Delete**, and tap
   **Undo** in the bar that appears at the bottom.

**Step 5 is done** when the photo arrives, is renamed and comes back after
Undo. On the laptop the files are in **`C:\AshCloud\storage\users\1\Camera\`**,
with the dates they were taken. See [Uploading and changing files](#uploading-and-changing-files)
for what happens when the connection drops.

You can still copy files into `C:\AshCloud\storage\users\1\` with File
Explorer; they show up on the phone right away.

### 4. Browse and play on the phone

1. Your folders and files are listed, folders first. Tap a folder to open
   it, and use the path at the top (Home / …) or the phone's Back button to go up.
2. Tap a **photo** or **PDF** to view it, and a **text file** to read it.
   To download a file, tap **⋯** next to it → **Download**.
3. Tap your **video**. It opens in Ash Cloud's player (see
   [The video player](#the-video-player)). Press ▶, then **drag the slider**
   to near the end, then back to the middle.

The video should start within a few seconds and resume within about 1–2
seconds after each drag. In the server window on the laptop
you'll see several lines like this one:

```
msg=request method=GET path=/api/v1/files/content status=206 bytes=8244224 ...
```

`status=206` means "here's the part you asked for". The phone fetches only
the pieces it plays, not the whole file.

### Which videos play in the browser

| Phone | Plays | Doesn't play (downloads instead) |
|---|---|---|
| iPhone (Safari) | `.MOV`, `.MP4`: H.264 or HEVC, i.e. everything the iPhone camera records | `.MKV`, `.AVI` |
| Android (Chrome) | `.MP4` (H.264), `.WEBM`; HEVC depends on the phone | `.MKV` (usually), `.AVI` |

How smoothly a video plays depends on your **home internet's upload speed**.
The laptop sends the video *up* that connection. 1080p phone video (about
10–20 Mbps) needs roughly 25 Mbps of upload to play without pausing; 4K
(about 40–60 Mbps) needs roughly 70 Mbps. Slower connections still play and
scrub, but pause to buffer.

---

## The video player

Tapping a video opens it in Ash Cloud's own player:

```
[━━━━━━●━━━━━━━━━━━━━━━━]  12:04 / 1:58:30
 −10   ▶   +10            Audio: Hindi   ⤢
```

| Control | What it does |
|---|---|
| **−10** | Back 10 seconds |
| **▶ / ❚❚** | Play / pause. Tapping the picture does the same. |
| **+10** | Forward 10 seconds |
| **Slider** | Jump anywhere in the video |
| **Audio: …** | Choose the audio track, e.g. English or Hindi, for videos that have more than one |
| **⤢** | Full screen. The phone turns sideways and the controls stay visible. |

On a laptop keyboard, **←** and **→** skip 10 seconds and **Space** plays or
pauses.

**How switching the audio works.** Chrome on Android can only play a video's
*first* audio track. When you pick another one, the server pulls just that
track out of the video with ffmpeg. The player then mutes the video and plays
that track alongside it, keeping the two in sync when you pause, skip or
drag. Your current audio keeps playing while the new track is prepared.

- **The first time** you pick a track, it says *Preparing Hindi audio… 40%*.
  A movie's track takes from a few seconds to about a minute, depending on its
  length and format. After that the track is saved, so it's instant.
- **Formats.** AAC, Opus, MP3 and FLAC tracks are copied as they are. Formats
  phones can't play, such as AC3, E-AC3 and DTS (common in movies), are
  converted to stereo AAC.
- **Saved tracks** live in `C:\AshCloud\cache\audio\`. Tracks nobody has
  played for 30 days are deleted when the server starts. Deleting the folder
  yourself is always safe; tracks are simply prepared again when needed.
- **Labels.** Track names come from the video, e.g. "Hindi 5.1", or from its
  language code, e.g. `hin` becomes "Hindi".

---

## Uploading and changing files

**Uploads are as fast as your connection.** Each file goes up in one
request and streams straight onto the laptop's disk; the server never holds
it in memory and adds almost nothing. Measured on a test machine: a 1 GB file
in under a second, and 100 photos of 3 MB in 1.4 s (14 ms each) when the
network isn't the limit. Over the internet, your home connection and the
phone's signal are the limit. Three files go up at once, which keeps the
connection busy even with many small photos.

**They survive a bad connection.**

| What happens | What you see |
|---|---|
| The connection drops (walking out of Wi-Fi, a tunnel, switching to mobile data) | `Waiting for the network…` or `Connection lost - trying again in 4 s`. When the network is back it asks the laptop how much arrived and **sends only the rest**. Nothing is sent twice. |
| The connection hangs without dropping | After 30 s without progress, the phone starts a new connection and the laptop drops the old one at once. |
| You close the tab, the phone kills the browser, or the laptop restarts | Pick the same file into the same folder again: it **continues where it stopped**. Unfinished uploads are kept for 7 days. |
| The power goes off on the laptop mid-upload | The upload continues from the last point safely on disk (at most 32 MB back). |
| You picked the wrong file | Tap **✕** next to it. What it sent is deleted. |

Keep the page open while uploading. The screen stays on by itself during
uploads, because Android pauses the page soon after the screen turns off.

**Nothing is ever overwritten.** Uploading `IMG_1.jpg` where one already
exists saves `IMG_1 (1).jpg`. Renaming or moving onto a name that's taken is
refused. Uploaded photos and videos keep the date they were taken.

**Before a big upload starts,** the laptop checks the folder exists and the
file fits, keeping 512 MB free so Windows keeps working. If not, you're told
at once rather than after sending everything.

**Delete goes to the trash.** It's instant, even for a big folder, and the
**Undo** button brings it back. Deleted items stay in the trash for 30 days,
then they're removed for good. (A page to browse the trash comes in Phase 2.)

**Where things are on the laptop:** each account's folder has two folders
of the server's own, `.ashcloud-uploads` (unfinished uploads) and
`.ashcloud-trash` (deleted items, one subfolder per delete). File Explorer
shows them; the phone never does. Leave them alone: the server cleans both up
by itself.

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
- **Your files are sandboxed.** A file opened in the browser can never run
  scripts as part of this site, so an `.html` or `.svg` someone sends you
  can't act with your login. HTML is shown as plain text. Only PDFs skip the
  sandbox, because browser PDF viewers refuse to run inside one.
- **Each account only sees its own folder.** Every file access goes through
  Go's `os.Root`, which refuses `..`, Windows device names like `NUL`, and
  shortcuts pointing outside the folder.
- **Error messages.** Errors never show internal details. Each response
  has an `X-Request-Id`, and the server log has the full story under that ID.
- **Uploads can't escape either.** An upload's name goes through the same
  rules, its partial file lives inside your own folder, and the upload's ID
  can only contain letters, digits, `-` and `_`.
- **Audit log.** Logins, failed logins, blocked attempts, logouts, password
  resets, uploads, new folders, moves, deletes and restores are recorded in
  the `audit_log` table, with time and device address.

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
| `GET /api/v1/files?path=/Photos` | **yes** | `200` `{"path": "/Photos", "entries": [{"name", "path", "type": "folder"\|"file", "size", "modified", "content_type"}]}`. Folders first, then files, sorted by name. |
| `GET /api/v1/files/content?path=/Videos/trip.mov` | **yes** | The file, as a download. Add `&inline=1` to open it in the browser instead: images, video, audio and PDFs display, text files show as plain text, and anything else still downloads. Supports `Range` (`206`), `ETag`/`If-None-Match` (`304`), `If-Range` and `HEAD`. No time limit. |
| `POST /api/v1/uploads/{id}?path=/Photos/IMG_1.jpg&size=123&modified=<ms>` | **yes** | Start an upload, or ask where an unfinished one stopped: `200` `{"id", "path", "size", "offset"}`; with `"file": {...}` too if it already finished. Checks the folder exists (`404`/`400`) and the file fits (`507 disk_full`). `{id}` is chosen by the browser: 16–64 letters, digits, `-` or `_`. |
| `PUT /api/v1/uploads/{id}?path=…&size=…&modified=…` + header `Upload-Offset: <n>` | **yes** | The file's bytes from byte `n` on, as the raw request body. `201` `{..., "file": {...}}` once complete (the name may become `name (1).ext`); `200` with the new `offset` if the body ended early; `409 wrong_offset` with the right `Upload-Offset` header if `n` isn't where the upload stopped; `409 taken_over` if a newer request continued it. No time limit; 60 s without data counts as a dropped connection. |
| `DELETE /api/v1/uploads/{id}` | **yes** | `204`; stops the upload and deletes what it received. |
| `POST /api/v1/files/folder` | **yes** | `{"path": "/Photos/2026"}` → `201` the new folder; `409 exists`. |
| `POST /api/v1/files/move` | **yes** | `{"from", "to"}` → `200` the moved file or folder. Renames too. `409 exists` (never overwrites), `409 in_use` (open in a program on the laptop), `400 into_itself`. |
| `DELETE /api/v1/files?path=/Photos/old.jpg` | **yes** | Moves it to the trash: `200` `{"trash_id"}`. |
| `POST /api/v1/trash/restore` | **yes** | `{"id": "<trash_id>"}` → `200` the restored file or folder, back where it was (as `name (1)` if that name was taken since). `404 not_in_trash`. |
| `GET /api/v1/media/info?path=/Videos/movie.mp4` | **yes** | `200` `{"duration_seconds", "audio_tracks": [{"track", "codec", "channels", "language", "title", "default", "native"}], "can_switch_audio", "reason"}`. `native` is the track the browser plays by itself (the first). `422` if the file isn't a video. |
| `POST /api/v1/media/audio` | **yes** | `{"path", "track"}` → `{"state": "preparing", "progress": 0.4}`, then `{"state": "ready", "url": ...}` (or `"failed"`, with `error`). Starts extracting the track the first time; call again to follow progress. `409` without ffmpeg. |
| `GET /api/v1/media/audio?path=…&track=1` | **yes** | The prepared track, with `Range` support like files. `404 not_ready` until prepared. No time limit. |

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
ffmpeg_dir = ''                 # folder with ffmpeg.exe and ffprobe.exe; empty = find them on the PATH
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
| Audio menu says "ffmpeg isn't installed" | Run `winget install Gyan.FFmpeg`, then start the server from a **new** PowerShell window. Or set `ffmpeg_dir` in the settings to the folder holding `ffmpeg.exe`. |
| Audio menu says "only one audio track" | That video really has just one. |
| Switching audio says "Couldn't prepare that audio track" | The server window shows ffmpeg's reason under `audio track extraction failed`. |
| Audio and picture drift apart | They're re-aligned automatically within about a second. If not, pause and play again. |
| A video won't play, or downloads instead | The browser can't play that format (see the table above). `.MKV` and `.AVI` never play in a browser. |
| A video keeps pausing to buffer | Your home internet's upload speed is lower than the video's bitrate. On the laptop, run `tailscale ping <phone-name>`. If it says `via DERP`, the connection is relayed: enable UPnP on your router. |
| Upload says "The laptop's drive is too full for that" | Free up space on the laptop's drive. Uploads leave 512 MB free on purpose. |
| Upload says "Waiting for the network…" for a long time | The phone has no connection to the laptop: check the Tailscale app is on, and that the server is running. It continues by itself once it can. |
| Uploads stop when the phone's screen turns off | Android pauses pages in the background. Keep Ash Cloud open and the screen on (it stays on by itself during uploads unless battery saver blocks it). If one stopped, pick the same file again: it continues. |
| Rename or delete says the file "is open in another program" | Windows doesn't let a file that's open be renamed or deleted. Close it on the laptop (or stop playing it) and try again. |
| "Something with that name is already there" | Pick another name. Nothing is ever overwritten. |
| Deleted something by mistake and Undo is gone | It's in `C:\AshCloud\storage\users\1\.ashcloud-trash\` for 30 days, in a folder named with a random ID (sort by date). Move it back from there with File Explorer. |
| An upload fails with "That name or path isn't allowed" | The file's name has a character Windows can't use, such as `:` or `?`. Rename it on the phone and upload it again. |
| A file on the laptop doesn't show up | Windows' own `desktop.ini` and `Thumbs.db` are hidden on purpose. So are names Windows itself can't use, and shortcuts that point outside your folder. |
| "There's nothing at that path" | The file or folder was moved or renamed on the laptop. Go back to Home and browse again. |
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

1. **Tools:** `winget install GoLang.Go Git.Git tailscale.tailscale Gyan.FFmpeg`, then
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
├── ashcloud.db          database: users, sessions, audit log, uploads, trash (+ -wal / -shm files while running)
├── cache\audio\         audio tracks extracted for the player (removed after 30 days unused)
└── storage\users\
    ├── 1\               your files (admin)
    │   ├── .ashcloud-uploads\   unfinished uploads (the phone never shows it)
    │   └── .ashcloud-trash\     deleted items, kept 30 days (the phone never shows it)
    └── <nano id>\       each other account's files
```

---

## Development

```powershell
go vet ./...
go test ./...
```

The media tests use the real ffmpeg when it's installed, and are skipped
otherwise. To measure how fast uploads go to disk, without the network:

```powershell
go test -run XXX -bench Write ./internal/drive
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
internal/auth/        passwords (argon2id), users, login, sessions, lockout, audit log
internal/ids/         Nano IDs for new users
internal/files/       files on disk, all through os.Root: path rules, content types, listing,
                      reading, and the low-level writes (folders, moves, trash, part files)
internal/drive/       changing files: resumable uploads, folders, moves, delete/restore,
                      trash and upload cleanup, audit log
internal/media/       ffprobe/ffmpeg: list a video's audio tracks, extract one, cache and clean up
internal/storage/     files folder: per-user folders, writability check
internal/httpapi/     routes, middleware chain, JSON errors, server start/stop
internal/webui/       the browser page, built into the binary (test page until step 6):
                      app.js (files, menus), uploads.js (upload queue), player.js (video player)
```
