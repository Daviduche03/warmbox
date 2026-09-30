# Omarchy guest image

Warmbox can boot a full **Omarchy** (Arch Linux ARM + Hyprland) desktop as a
named image. Every `warmbox create --image omarchy` boots a clone of the golden
disk in a few seconds — no install, no greeter.

```
warmbox daemon --pool 0                 # discovers ~/.warmbox/images/omarchy
warmbox create --image omarchy          # boots a clone, prints its noVNC URL
warmbox images                          # list named images
```

## Named images

A named image is a directory under `$WARMBOX_HOME/images/<name>/`:

```
omarchy/
  disk.raw        # an EFI-bootable disk (whole-disk image)
  efi-vars.fd     # EFI variable-store seed, so firmware finds the boot entry
  meta.json       # optional overrides: {"gpu","mem_mib","cpus","input"}
```

`meta.json` sets per-image boot parameters; e.g. `"gpu": "800x600"` sizes the
virtio-gpu device. Warmbox hands the guest its identity over a virtiofs share
(tag `warmbox-config`) because EFI boot has no kernel cmdline — the image's
`warmbox-ready` unit reads it and reports readiness back to the daemon.

## Shipping an image

Images are large raw disks, so they can be packed as a single compressed
artifact and expanded on the destination:

```
warmbox image pack omarchy                 # -> ~/.warmbox/images/omarchy.tar.zst
warmbox image pull omarchy <file-or-url>   # expand into ~/.warmbox/images/omarchy
warmbox image list                         # local images (offline)
```

The Omarchy image packs from ~8.4 GiB on disk to ~3.9 GiB (zstd; the disk is
mostly free space). `pull` writes sparsely, but note APFS only preserves *large*
holes: this image's free space is fragmented by btrfs across the device, so an
expanded copy can allocate closer to its full logical size. Pack/pull is
therefore best for moving an image between hosts; for local disk economy prefer
the built-in image (776 MB) or keep the packed artifact and expand on demand.

## Volumes

A persistent volume (a raw ext4 disk attached as `/dev/vdb`) is mounted at
`/volume` by `guest/warmbox-volume`, which the daemon triggers over the same
config share (`"volume":"1"`). Because the VM is stopped by killing vfkit,
`guest/warmbox-volume-sync` flushes every 2s so at most a couple of seconds of
writes are lost. Use it like any other warmbox volume:

```
warmbox volume create mywork --size 4G
warmbox create --volume mywork            # + --image omarchy for Omarchy
```

## Building the image

`build.sh` stages an **already installed** Omarchy disk as a named image and
provisions it (agent + readiness + VM tuning) over the serial console:

```
OMARCHY_SRC=~/.omarchy-vm/disk.raw \
OMARCHY_EFIVARS=~/.omarchy-vm/efi-vars.fd \
./deploy/omarchy/build.sh
```

A from-scratch build is a boot-and-install and is not automated here:

1. Create a blank aarch64 VM and install Arch Linux ARM from the Archboot ISO
   (see `vm/omarchy-vm.sh` in the omarchy-vm tree). `install-arch.sh` repartitions
   `/dev/vda`; set a password with `arch-chroot /mnt passwd omarchy`.
2. Boot it and install Omarchy. The community aarch64 port is the reliable path
   (it ships a consistent package set); basecamp+cua also works but may hit
   transient Arch Linux ARM package skews (e.g. `hyprland` vs `aquamarine`).
3. Freeze the disk and provision it with `build.sh`.

## Tuning (what makes it usable on AVF)

Apple's Virtualization.framework exposes virtio-gpu with no 3D (`-virgl`), so
Hyprland composites through Mesa `llvmpipe`. Every screen change is a full
software composite, and its cost is ~proportional to pixels. `guest/tune.sh`
therefore sets a small output (default 800×600), disables animations, serves
the session with `wayvnc -f 60`, and trims idle daemons. On a 4-vCPU VM that is
the difference between a laggy and a usable remote desktop. X11 (the built-in
image) is still lighter for remote display; Omarchy is a full Wayland desktop.
