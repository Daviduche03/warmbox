# Remote access: use warmbox from anywhere

The daemon binds loopback by default and refuses to serve plaintext to a
network. That is the safe default — keep it. These are the three ways to
reach it remotely, best first.

## Recommended: Tailscale (private mesh, no open ports)

Put your Mac, your server, and your phone on one tailnet. The daemon never
touches the public internet; everything below rides WireGuard.

1. Install Tailscale on each machine plus the iOS app, and sign into the
   same tailnet. Note each machine's MagicDNS name (`mymac`, `myserver`).
2. On the machine running the daemon, expose it with automatic TLS:
   ```sh
   tailscale serve --bg http://127.0.0.1:7070
   ```
   The dashboard (and API) is now at `https://<machine>.<tailnet>.ts.net`,
   with certificates Tailscale issues and renews itself.
3. CLI from anywhere on the tailnet:
   ```sh
   warmbox --addr https://mymac.<tailnet>.ts.net list
   warmbox --addr https://mymac.<tailnet>.ts.net connect <id>
   ```
   Authenticate once (`warmbox login`, or a user API token from the
   dashboard) and it behaves exactly like local use.
4. Phone: with the Tailscale iOS app on, Termius or Blink SSHes straight to
   tailnet names (`ssh mymac`), and mobile Safari/Chrome opens the dashboard
   URL. No tunnels to configure, no ports to remember.

Optional hardening inside Tailscale: tailnet ACLs limiting who reaches the
daemon host, and turning off key expiry for the server. None of it is
warmbox configuration — the daemon still sees loopback traffic.

## Manual TLS (public internet, you own every knob)

Bind the daemon to the network with a real certificate:

```sh
# sudo it on macOS: the service runs as a system daemon (see running.md#macos)
warmbox service install --addr 0.0.0.0:7070 \
  --tls-cert /etc/letsencrypt/live/<host>/fullchain.pem \
  --tls-key /etc/letsencrypt/live/<host>/privkey.pem
```

- The daemon refuses a non-loopback bind without TLS unless you pass
  `--insecure`. Never pass it on the internet; it exists for throwaway
  LAN tests.
- Open only the daemon port in the firewall. Everything else (guest
  networks, VNC bridges) stays host-local by construction.
- Shipped protections on this path: login is rate-limited (10/min/IP) over
  bcrypt-12, sessions are 30-day HttpOnly cookies (Secure over TLS),
  cookie sessions are CSRF-gated by Origin check, per-VM VNC passwords,
  and HSTS is served over TLS.
- Honest limits: guests run as root inside their VMs, and the guest agent
  API has no auth inside the NAT (single-user design — see
  [agent-api](agent-api.md)). The operator account owns the instance; give
  other humans Member/Viewer roles, not the owner login.

## Fallback: SSH tunnel (no new software)

```sh
ssh -L 7070:127.0.0.1:7070 <user>@<host>   # then open http://localhost:7070
```

Same for the CLI: the tunnel makes the remote daemon local. This is the
right answer for one-off access and for hosts you will never expose.

## Which to pick

|  | Tailscale | Manual TLS | SSH tunnel |
|---|---|---|---|
| Open ports | none | daemon port | 22 only |
| Certificates | automatic | yours (Let's Encrypt etc.) | none |
| Phone | native app + browser | browser (public URL) | SSH app + tunnel |
| Multi-user | tailnet ACLs | warmbox roles | shared SSH access |
| Moving parts | tailnet daemon | cert renewal, firewall | per-session command |

If you are unsure, pick Tailscale. Pick manual TLS when the dashboard must
be reachable by people (or webhooks) outside your tailnet.
