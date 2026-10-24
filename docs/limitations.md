# Limitations

warmbox is in beta. It is tested and packaged, but it runs on a single host and
is short of real-world mileage. The defaults assume a host you trust.

## Security

The API is not open. Accounts and workspaces gate every stateful route.
Everything that reaches a desktop (console streams, the agent API, published
guest ports) is scoped to the caller's workspace, and warm-pool VMs are kept out
of every listing.

The daemon speaks plain HTTP unless given `--tls-cert` and `--tls-key`. It binds
loopback by default and refuses a network address without TLS or an explicit
`--insecure`.

Each guest's VNC server requires a per-VM password, generated inside the guest
and handed to noVNC with the console URL. The guest sits behind the hypervisor's
NAT, so that port is never on the LAN.

Guests are on QEMU user-mode networking (`-netdev user`), never tap or
vhost-net. That is deliberate: slirp ends the guest's L2/L3 in QEMU's userspace
and re-originates its traffic as ordinary host sockets, so guest-controlled
packet metadata never enters the host kernel's network stack. Hypervisor CVEs
that need a tap/vhost-net backend to reach the host — for example
CVE-2026-99999 ("PwnEscape", an IPv4 checksum-offload guest-to-host escape) —
do not have that path here. On macOS the host is not Linux at all, so the same
bug class does not apply. Keep it that way: bridged or tap networking would
trade this boundary for LAN reachability.

What remains is the guest itself. It runs as root, and none of this is hardened
against someone who already has the desktop.

## You cannot type into the browser console yet

The desktop streams to the browser, and the mouse works — but **keystrokes do
not reach the guest over VNC**. The VNC server accepts the key events and
then drops them before they reach the X server, while the same keystrokes
work over the agent's own channel.

This was checked rather than assumed: the RFB bytes on the wire are correct,
pointer events and screen updates work, `xdotool` (XTEST) types into the
guest perfectly, and the same failure reproduces in a real browser with
noVNC. `AcceptKeyEvents`, `xkeyboard-config` and TigerVNC's `RawKeyboard`
were each tried; none of them changes it.

So today the browser console is a *watch* screen, not a *drive* screen. An
agent is not blocked: it types through the agent API instead —
`POST /api/desktops/{id}/input` — which works. Fixing the human path means
either a different VNC server in the guest or a newer TigerVNC.

## EFI disk images are macOS-only

Omarchy is an EFI disk and the QEMU backend ships no OVMF firmware, so it cannot
boot there. Overlay images need no firmware and take the same path on both
backends. Omarchy is arm64 too, so it would not run on an x86_64 server even
with firmware.

## Memory checkpoints are Linux-only

`checkpoint`, `restore`, `hibernate` and `wake` work on Linux with KVM. Restore
runs in about a second where booting takes nine.

They are not available on macOS, because vfkit can't save VM state. Pause is not
a substitute. It freezes the CPUs in place and the memory stays allocated. See
[snapshots.md](snapshots.md).

## Guests have no GPU or audio

Guests get virtio-gpu without 3D, so a Wayland desktop composites through Mesa
`llvmpipe`. The Omarchy image is tuned for it, with a small output and effects
off. X11 is much lighter for remote display.

## Images are big

Images are a few hundred MB compressed and download in one piece. Omarchy is the
heavy one at about 8.4 GB on disk and 3.9 GB packed. Because btrfs fragments
free space and APFS only preserves large holes, an expanded copy may not be as
sparse as you would like.

## Single writer

A volume attaches to one VM at a time, and the lock is in-process. That is fine
for one host and not for a fleet.

## Commit cost

Committing hashes the changed extents. It is fast for a sparse disk, but still
O(used).

## Not on every host

| Host | Why not |
|---|---|
| Intel Mac | No nested virtualisation, and vfkit is ARM64-only |
| Windows (WSL2 or native) | Not built yet |
