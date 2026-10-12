# Running warmbox day to day

## Setup

`warmbox setup` installs what is missing and is safe to run again. It installs
the hypervisor for your host (`brew install vfkit`, or QEMU from your package
manager), then gets the guest image.

It downloads the published image for your architecture, verified against its
sha256 and resumable. When nothing is published for your platform it fetches the
source and builds the image locally, installing Docker first if it has to.

| Flag | What it does |
|---|---|
| `--no-install` | Report what is missing without installing it |
| `--image-url`, `--image-sha256` | Use an image archive of your own |

## The daemon

The daemon serves the dashboard and the API on `127.0.0.1:7070` by default.

Guests get a second listener on the vmnet gateway that answers nothing but the
readiness callback. The API is never reachable from the network, and guests can
still report that they booted.

```sh
warmbox service install --pool 1   # launchd on macOS, systemd on Linux
warmbox service status             # start | stop | restart | status
```

## Linux

`warmbox service install` creates a systemd unit. As root that is
`/etc/systemd/system/warmbox.service`. Otherwise it is a per-user unit under
`~/.config/systemd/user/` with lingering enabled so it starts at boot. Logs are
in the journal:

```sh
journalctl -u warmbox.service -f
```

The daemon needs `--backend qemu` on Linux, and `warmbox service install` passes
it for you.

QEMU normally asks for KVM. On a machine without it, such as a CI runner or a
container that cannot pass `/dev/kvm` through, `--accel tcg` falls back to
software emulation. That is roughly twenty times slower but it does boot.

QEMU's user-mode networking is not reachable from host to guest, so the backend
forwards a host port to each guest's VNC and agent. Everything else behaves the
same.

## Reaching it from another machine

Everything the daemon serves is plaintext: the dashboard, its session cookie,
the console and the guest API. So it binds `127.0.0.1` and refuses a network
address without a certificate.

```sh
# a tunnel needs neither TLS nor an open port
ssh -L 7070:127.0.0.1:7070 <user>@<host>

# or run it on the network, with a certificate (the service remembers it)
warmbox service install --pool 1 \
  --tls-cert /etc/letsencrypt/live/<host>/fullchain.pem \
  --tls-key  /etc/letsencrypt/live/<host>/privkey.pem

# or accept the risk knowingly
warmbox service install --pool 1 --addr 0.0.0.0:7070 --insecure
```

[remote-access.md](remote-access.md) compares Tailscale, manual TLS and an SSH
tunnel.

## Memory and the ready pool

The daemon keeps a pool of VMs that are already booted, so a new desktop does
not wait for a boot. Anything that is not the default, such as a different
image, a custom size or a volume, cold-boots in a few seconds.

Ready VMs hold RAM even when nobody is creating anything. The pool drains itself
after 15 minutes without a lease, and the next create pays a cold boot.

| Flag | What it does |
|---|---|
| `--pool-idle-timeout 30m` | Drain after 30 minutes |
| `--pool-idle-timeout 0` | Keep the pool ready forever |

## Pause, resume and destroy

Pause stops the guest's CPUs and keeps its memory. Resume picks the session up.
Destroy is what hands the RAM back. A volume survives all three.

## Where things live

Everything is under `~/.warmbox/` unless you pass `--workdir`.

```
images/<name>/      installed guest images, each with its meta.json
volumes/            local volume disks, when not backed by a bucket
vms/<id>/            per-VM state: console log, pid, control socket
novnc/              the dashboard's VNC client assets
daemon.log          the service's output
```

## Volumes on different images

On an overlay image the volume is the writable layer. The whole machine
persists, and destroying the desktop keeps every file.

On an EFI image like Omarchy the volume is a data disk mounted at `/volume`,
flushed every couple of seconds.

Volume sizes are grow-only, and the floor is `VOLUME_BASE_SIZE`.

When volumes are in a bucket, another host attaches the same chunks from the
same bucket. Nothing is cloned or exported. `warmbox cloud show` prints where
volume bytes live.
