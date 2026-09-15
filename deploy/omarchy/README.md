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
