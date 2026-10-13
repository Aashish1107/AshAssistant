# Ash Cloud server

The Go server behind Ash Cloud. See [`docs/PLAN.md`](../docs/PLAN.md) for the
full plan.

**Current stage: Phase 1, step 1.** The server only answers `GET /healthz` with
`ok`. This step proves the whole path from the phone, over Tailscale, to the laptop
works before any features are built on top of it.

---

## Try it on the Windows laptop

### 1. Install the tools (once)

In PowerShell:

```powershell
winget install GoLang.Go Git.Git tailscale.tailscale
```

Close PowerShell and open a new one so `go`, `git` and `tailscale` are on the
PATH. Check with `go version`. It should print 1.24 or newer.

### 2. Get the code and build

```powershell
git clone https://github.com/Aashish1107/AshAssistant.git
cd AshAssistant
git checkout claude/local-cloud-server-setup-mmdj55
cd server
go build -o ashcloud.exe .\cmd\ashcloud
```

### 3. Run it and test it on the laptop

```powershell
.\ashcloud.exe serve
```

Leave this window open. It prints a log line for every request. In a browser
on the laptop, open <http://127.0.0.1:8080/healthz>. You should see `ok`.

The server only listens on `127.0.0.1`. Other devices on your Wi-Fi can't
reach it, so Windows Firewall won't ask about it either. The only way in from
outside is through Tailscale, set up next.

### 4. Set up Tailscale (once)

1. **Laptop:** open Tailscale from the Start menu and sign in.
2. **Phone:** install the **Tailscale** app (App Store / Play Store), sign in
   with the **same account**, and switch it on.
3. **Admin console** (<https://login.tailscale.com/admin>):
   - **DNS** page: turn on **MagicDNS**, then **Enable HTTPS** (HTTPS Certificates).
   - **Machines** page: on the laptop's `…` menu, choose **Disable key expiry**.
4. **Laptop:** Tailscale tray icon → **Preferences** → turn on
   **Run unattended**, so Tailscale stays connected after a reboot even
   before you log in.

### 5. Put the server on your tailnet

Open a **second** PowerShell window (leave the server running in the first):

```powershell
tailscale serve --bg http://127.0.0.1:8080
```

It prints your phone address, which looks like
`https://<laptop-name>.<tailnet-name>.ts.net`. If it prints a link asking you to
enable HTTPS or Serve instead, open the link, approve, and run the command
again.

`--bg` makes this stick: Tailscale keeps forwarding even after you close the
window or reboot. Useful commands:

| Command | What it does |
|---|---|
| `tailscale serve status` | Show what's being served and the URL |
| `tailscale serve reset` | Stop serving entirely |

### 6. The real test: from the phone, anywhere

1. On the phone, **turn Wi-Fi off** so you're on mobile data, not your home
   network.
2. Make sure the Tailscale app is **on**.
3. Open `https://<laptop-name>.<tailnet-name>.ts.net/healthz`.

You should see **`ok`**. In the server window on the laptop, a new log line
appears. Its `forwarded_for=100.x.y.z` is your phone's Tailscale address:

```
level=INFO msg=request method=GET path=/healthz status=200 bytes=3 duration=9µs remote=127.0.0.1:52011 forwarded_for=100.101.102.103
```

**Step 1 is done** when that works on mobile data.

### 7. Check the connection is direct (fast)

On the laptop:

```powershell
tailscale status              # find your phone's name
tailscale ping <phone-name>
```

- `pong from … via 203.0.113.5:41641`: **direct** connection, the fastest path.
- `pong from … via DERP(xxx)`: **relayed** through a Tailscale server. It
  still works but is slower. Enable UPnP on your home router (or allow UDP port
  41641 in), then ping again.

### Stopping

Press **Ctrl+C** in the server window. The server finishes any requests already
in progress, then exits. While it's stopped, the phone gets `502 Bad Gateway`.
That's expected: Tailscale is still forwarding, but nothing is answering.

---

## Troubleshooting

| Problem | Fix |
|---|---|
| `go` / `tailscale` is not recognized | Open a **new** PowerShell window after installing. |
| `can't listen on 127.0.0.1:8080` | Another program uses port 8080. Run `.\ashcloud.exe serve --listen 127.0.0.1:8081` and use `http://127.0.0.1:8081` in the `tailscale serve` command too. |
| Phone: `502 Bad Gateway` | `ashcloud.exe` isn't running. Start it (step 3). |
| Phone: site can't be found | The Tailscale app is off on the phone, or MagicDNS is off (step 4). |
| Phone: `404 page not found` | You opened the address without `/healthz`. Step 1 only has `/healthz`. |
| Works on Wi-Fi but not mobile data | The phone was reaching the laptop over your home network. Check the Tailscale app is on, and use the `ts.net` address. |

---

## Commands

```
ashcloud serve [--listen 127.0.0.1:8080]   start the server (Ctrl+C to stop)
ashcloud version                           print the version
ashcloud help                              show help
```

## Endpoints

| Endpoint | Response |
|---|---|
| `GET /healthz` | `200 ok`. The process is up. |

## Development

```powershell
go vet ./...
go test ./...
```

Build with a version stamp:

```powershell
go build -ldflags "-X main.version=v0.1.0" -o ashcloud.exe .\cmd\ashcloud
```

Code layout:

```
cmd/ashcloud/       CLI entry point (serve, version, help)
internal/httpapi/   routes, request logging, server start/stop
```
