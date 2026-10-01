# Egress policy

Guest VMs should be able to reach the APIs and registries they need — and
nothing else. This is a first cut at that, in the spirit of Meta's Muse
"Sentinel": the **decision is made on the host boundary**, not inside the guest,
because the guest runs as root and could undo anything enforced within it.

```
guest process ──▶ egress proxy (host) ──▶ policy check ──▶ internet
                  --allow / --deny          ALLOW / DENY (logged)
```

## Usage

```sh
# default-deny: only these domains (and their subdomains) may pass
warmbox daemon --egress :8099 --allow api.openai.com,pypi.org,registry.npmjs.org

# open by default, with a denylist
warmbox daemon --egress :8099 --deny ads.example.com

# per-run override (deny beats allow)
warmbox daemon --egress :8099 --allow example.com --deny blocked.example.com
```

Patterns: an exact host (`api.openai.com`), a domain suffix (`example.com` also
matches `a.example.com`), or a wildcard (`*.example.com`). A non-empty `--allow`
makes the policy **default-deny**.

The host daemon starts an HTTP(S) forward proxy and tells each guest to route
through it:

- overlay images (`xfce`, `lxqt`, `headless`): on the kernel cmdline (`warmbox.proxy=…`),
  which `deploy/guest/init` turns into `http_proxy`/`https_proxy` for the agent
  and any program it runs.
- EFI images (`omarchy`): the host writes the same address into the config share
  (`"proxy"`), **but the image doesn't read it yet** — it only consumes `id`,
  `host`, `port` and `volume`. So on Omarchy the policy reaches the guest's
  environment not at all today; wiring the share's `proxy` key into the session
  and the agent is a follow-up.

Every decision is logged: `egress: ALLOW api.openai.com:443`, `egress: DENY …`.

## Configure per desktop

A policy can be set **at creation** and **changed afterwards**, from the API or
the dashboard:

```sh
# at creation (empty allow/deny inherits the daemon defaults)
curl -X POST localhost:7070/api/desktops -d '{"allow":["api.openai.com","pypi.org"]}'

# after creation
curl -X POST localhost:7070/api/desktops/$ID/policy -d '{"allow":["example.com"]}'
```

`GET /api/desktops/{id}` reports the desktop's `allow`/`deny`. The proxy resolves
the policy **per guest IP**, so a policy change takes effect on the next
connection without touching the guest. In the dashboard: the *New desktop* row
has allow/deny fields, and each desktop's ⋯ menu has **Egress policy…**.

## What this does and does not do

**It does**: expose a single, host-side authority for where guests may connect,
with a default-deny allowlist, and route the ordinary tool path (curl, npm, pip,
apt, git) through it.

**It does not (yet) enforce.** Setting `http_proxy` is *configuration*, not a
boundary: a process that ignores the variable, or unsets it, can still reach the
internet directly, because the guest's NAT path is otherwise open. Real
enforcement means the host **blocks all guest egress except to the proxy**:

- Linux: an nftables rule on the vmnet bridge (drop guest → internet, allow
  guest → host:proxy). This needs root; the daemon would install it on start.
- macOS: `pf` (also root).

Until that lands, treat `--allow/--deny` as **convenience + audit**, not a
security control. The correct framing is "the seam is in the right place (host
boundary) and the policy engine works; the enforcement half is the next step."

## Roadmap

1. **Host firewall enforcement** — the piece that makes it a boundary. Linux
   first (nftables); macOS via `pf`.
2. **Credential surrogation** — the agent holds a surrogate token with no real
   rights; the proxy injects the real secret only for approved destinations
   (Muse's `hatch-authd`). This is the enterprise feature.
3. **Taint tracking (lite)** — if a process has read private data, tighten its
   network. Even a coarse version captures most of the value.
4. **Denied-by-default DNS** — resolve guest DNS through the proxy so names
   that aren't allowed don't resolve at all.
5. **Proxy on image guests** — read the config share's `proxy` key in the
   Omarchy image so EFI guests get the same routing overlay images do.

See also [`deploy/omarchy/README.md`](../deploy/omarchy/README.md) for the image
side, and `docs/agent-api.md` for the guest channel that agents actually use.
