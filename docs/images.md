# Guest images

Desktops boot from guest images. Every image warmbox can build is one file in
[`deploy/images/`](../deploy/images), and that file is the source of truth. It
picks the engine that builds the image, and it generates the image's
`meta.json`, which is all the daemon reads at runtime.

The README has the list of images and how to pick one. This page covers
building, downloading, moving and adding images.

## Headless images

Headless images are built without an X server, a desktop session or a browser.
The guest still runs the agent, so `exec`, files, runs and sessions all work.
There is nothing to look at and no VNC port to connect to.

It is a property of the image and not a flag on `create`, so the guest and the
daemon cannot disagree about whether a screen exists.

## Building, downloading and moving images

The dashboard's **Images** page lists what is installed next to what this build
knows about but has not fetched, and an admin can download one from there. The
same thing from a shell:

```sh
warmbox image build                   # what can be built, and what is installed
warmbox image build lxqt              # build one from a checkout
warmbox image build --all             # every image the checkout can build
warmbox image pull headless           # download the published one for this platform
warmbox image pack lxqt -o lxqt.tar.zst   # pack one to move it, + .sha256
warmbox image list                    # local images, offline
```

An image lands in `~/.warmbox/images/<name>/`. It is one of two kinds, plus a
`meta.json`:

| Kind | Files | Notes |
|---|---|---|
| Overlay | `vmlinux`, `rootfs.squashfs`, `initramfs-overlay` | One read-only rootfs shared by every VM |
| EFI disk | `disk.raw` | Used by `omarchy` |

There is no built-in image. The daemon boots the image you name, and the
daemon's `--image` decides what an unnamed create gets.

## Adding your own image

Write a file and build it. Nothing else has to know.

```yaml
# deploy/images/epiphany.yaml
name: epiphany
engine: overlay          # overlay | efi
desktop: xfce
theme: win11
browser: epiphany
headless: false
publish: false           # true ships it on the images release
```

The `overlay` engine builds a rootfs from `deploy/guest/Dockerfile`. Adding a
desktop, a theme or a browser is a Dockerfile change, and the config is how you
say which combination is an image.

The `efi` engine (used by `omarchy`) stages a disk that was already installed on
a machine, so it needs input from the host. `warmbox image build --all` skips
those and builds the rest.

## Apps inside a desktop

`warmbox-app` turns a directory into a menu entry. Something you built, such as
a dashboard an agent generated or a tool you compiled, then behaves like an
installed app and lives on the volume with the rest of your machine.

```sh
warmbox-app new mydash          # scaffold a web app
warmbox-app install mydash      # appears in the menu, opens as an app window
warmbox-app serve mydash 8080   # prints a /p/<vm>/8080/ link you can open
```

`warmbox-app` is experimental.
