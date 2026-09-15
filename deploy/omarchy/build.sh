#!/bin/sh
# Build the Omarchy guest image for warmbox.
#
# A from-scratch Omarchy ARM build is a boot-and-install (Arch Linux ARM + the
# omarchy-mac/Quattro port) — see README.md. This script takes an already
# installed, EFI-bootable Omarchy disk, stages it as a warmbox named image, and
# (optionally) provisions it with the warmbox agent, readiness unit and VM
# tuning over the serial console.
#
# Produces, under $WARMBOX_HOME/images/omarchy:
#   disk.raw        the golden EFI disk (APFS clone of the source)
#   efi-vars.fd     EFI variable store seed (so firmware finds the boot entry)
#   meta.json       per-image boot overrides (gpu, mem, cpus, input)
#
# Then:  warmbox daemon --pool 0        # finds images: [omarchy]
#        warmbox create --image omarchy
#
# Env:
#   OMARCHY_SRC       installed Omarchy EFI disk  (default $HOME/.omarchy-vm/disk.raw)
#   OMARCHY_EFIVARS   matching EFI variable store (default $HOME/.omarchy-vm/efi-vars.fd)
#   WARMBOX_HOME      output directory            (default $HOME/.warmbox)
#   RESOLUTION        output resolution           (default 800x600)
#   VNC_FPS           wayvnc max fps              (default 60)
#   PROVISION         1 = run provisioning over the serial console (default 1)
#   OMARCHY_PASSWORD  guest login for provisioning (default omarchy)

set -eu

here=$(cd "$(dirname "$0")" && pwd)
WARMBOX_HOME="${WARMBOX_HOME:-$HOME/.warmbox}"
OMARCHY_SRC="${OMARCHY_SRC:-$HOME/.omarchy-vm/disk.raw}"
OMARCHY_EFIVARS="${OMARCHY_EFIVARS:-$HOME/.omarchy-vm/efi-vars.fd}"
RESOLUTION="${OMARCHY_VM_RESOLUTION:-800x600}"
VNC_FPS="${OMARCHY_VM_VNC_FPS:-60}"
PROVISION="${PROVISION:-1}"
OMARCHY_PASSWORD="${OMARCHY_PASSWORD:-omarchy}"

die() { echo "error: $*" >&2; exit 1; }
note() { printf '==> %s\n' "$*"; }

[ "$(uname -m)" = "arm64" ] || die "this image targets Apple Silicon (arm64)"
command -v vfkit >/dev/null 2>&1 || die "vfkit not found (brew install vfkit)"
[ -f "$OMARCHY_SRC" ] || die "no Omarchy disk at $OMARCHY_SRC (set OMARCHY_SRC)"

img="$WARMBOX_HOME/images/omarchy"
mkdir -p "$img"

note "building warmbox-agent (linux/arm64)"
( cd "$here/../guest/agent" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
    go build -trimpath -ldflags '-s -w' -o "$img/warmbox-agent" . )

note "staging provisioning assets"
cp "$here/guest/warmbox-ready" "$here/guest/warmbox-agent.service" \
   "$here/guest/warmbox-ready.service" "$here/guest/setup.sh" "$here/guest/tune.sh" "$img/"

note "cloning Omarchy disk -> $img/disk.raw"
cp -c "$OMARCHY_SRC" "$img/disk.raw" 2>/dev/null || cp "$OMARCHY_SRC" "$img/disk.raw"

if [ -f "$OMARCHY_EFIVARS" ]; then
  cp "$OMARCHY_EFIVARS" "$img/efi-vars.fd"
fi

cat > "$img/meta.json" <<EOF
{
  "gpu": "$RESOLUTION",
  "mem_mib": 3072,
  "cpus": 4,
  "input": true
}
EOF
note "wrote $img/meta.json (gpu=$RESOLUTION)"

if [ "$PROVISION" = "1" ]; then
  note "provisioning over the serial console (resolution=$RESOLUTION, fps=$VNC_FPS)"
  OMARCHY_VM_RESOLUTION="$RESOLUTION" OMARCHY_VM_VNC_FPS="$VNC_FPS" \
  python3 "$here/provision.py" "$img/disk.raw" "$img/efi-vars.fd" "$img" "$OMARCHY_PASSWORD" "$VNC_FPS"
  rm -f "$img/warmbox-agent" "$img/warmbox-ready" "$img/warmbox-agent.service" \
        "$img/warmbox-ready.service" "$img/setup.sh" "$img/tune.sh"
else
  note "skipping provisioning (PROVISION=0); run deploy/omarchy/guest/setup.sh inside the guest"
fi

note "done. run: warmbox daemon --pool 0   then: warmbox create --image omarchy"
