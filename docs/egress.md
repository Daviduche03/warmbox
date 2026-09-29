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

- overlay images (built-in, `lxqt`): on the kernel cmdline (`warmbox.proxy=…`),
  which `deploy/guest/init` turns into `http_proxy`/`https_proxy` for the agent
  and any program it runs.
- EFI images (`omarchy`): over the config share (`"proxy"`), a follow-up.

Every decision is logged: `egress: ALLOW api.openai.com:443`, `egress: DENY …`.

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
2. **Per-desktop policy** — the proxy already resolves policy per client IP
   (`New(…, resolve)`); wire it to each VM's policy from `warmbox create`.
3. **Credential surrogation** — the agent holds a surrogate token with no real
   rights; the proxy injects the real secret only for approved destinations
   (Muse's `hatch-authd`). This is the enterprise feature.
4. **Taint tracking (lite)** — if a process has read private data, tighten its
   network. Even a coarse version captures most of the value.
5. **Denied-by-default DNS** — resolve guest DNS through the proxy so names
   that aren't allowed don't resolve at all.

See also [`deploy/omarchy/README.md`](../deploy/omarchy/README.md) for the image
side, and `docs/agent-api.md` for the guest channel that agents actually use.
