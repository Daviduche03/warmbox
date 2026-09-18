#!/bin/sh
# Build the warmbox guest image and extract its boot artifacts.
#
#   ./deploy/guest/build.sh
#
# Produces, in $WARMBOX_HOME (default ~/.warmbox):
#   vmlinux             uncompressed arm64 kernel        (vfkit needs a raw Image)
#   initramfs.zst       zstd cpio initramfs = whole rootfs (all-RAM boot)
#   initramfs-virt      Alpine boot initramfs             (ext4 disk boot)
#   rootfs.squashfs     read-only rootfs base             (overlay boot)
#   initramfs-overlay   boot initramfs that layers a tmpfs overlay on the base
#
# Overlay boot is the recommended path: a single read-only squashfs base is
# shared by every VM and writes go to a tmpfs overlay in RAM, so there is no
# per-VM disk copy and RAM stays low. See deploy/guest/overlay-init.
#
# Env:
#   BROWSER       none|netsurf|epiphany|firefox|chromium   (default epiphany)
#   DESKTOP       xfce|lxqt                                (default xfce)
#   THEME         default|win11|ambiance                    (default win11;
#                 ambiance pairs with DESKTOP=lxqt)
#   VARIANT       output suffix for parallel images, e.g. lxqt-ambiance.
#                 Empty (default) writes the classic filenames; set VARIANT to
#                 keep 3 images side by side:
#                   DESKTOP=xfce THEME=win11 VARIANT=xfce    -> rootfs-xfce.squashfs ...
#                   DESKTOP=lxqt THEME=ambiance VARIANT=lxqt -> rootfs-lxqt.squashfs ...
#                 Point a daemon at a variant with
#                 --squash/--overlay-initrd/--disk/--boot-initrd/--image flags
#                 (or a separate --workdir).
#   IMAGE         docker image tag                          (default warmbox-guest:latest)
#   WARMBOX_HOME  output directory                          (default ~/.warmbox)
#   PLATFORM      docker build platform                     (default linux/arm64)

set -eu

here=$(cd "$(dirname "$0")" && pwd)
BROWSER="${BROWSER:-chromium}"
DESKTOP="${DESKTOP:-xfce}"
THEME="${THEME:-win11}"
VARIANT="${VARIANT:-}"
IMAGE="${IMAGE:-warmbox-guest:latest}"
WARMBOX_HOME="${WARMBOX_HOME:-$HOME/.warmbox}"
# A variant is written as a named image at images/<variant>/ (boot it with
# --image <variant>); without one it writes the built-in image at the top level.
SUB=""
[ -n "$VARIANT" ] && SUB="images/$VARIANT"
OUTDIR="$WARMBOX_HOME${SUB:+/$SUB}"
PLATFORM="${PLATFORM:-linux/arm64}"
# Floor size of a volume. Volumes are grow-only, so this is the smallest a
# volume can be; request larger with `warmbox volume create --size`.
VOLUME_BASE_SIZE="${VOLUME_BASE_SIZE:-2G}"

echo "==> building $IMAGE (BROWSER=$BROWSER, DESKTOP=$DESKTOP, THEME=$THEME, platform=$PLATFORM, variant=${VARIANT:-default})"
docker build --platform "$PLATFORM" -t "$IMAGE" \
    --build-arg "BROWSER=$BROWSER" \
    --build-arg "DESKTOP=$DESKTOP" \
    --build-arg "THEME=$THEME" "$here"

mkdir -p "$OUTDIR"

echo "==> extracting kernel + initramfs"
cid=$(docker create --platform "$PLATFORM" "$IMAGE")
trap 'docker rm -f "$cid" >/dev/null 2>&1 || true' EXIT INT TERM

docker cp "$cid:/initramfs.zst" "$OUTDIR/initramfs.zst"
docker cp "$cid:/boot/initramfs-virt" "$OUTDIR/initramfs-virt"

tmpvmlinuz=$(mktemp /tmp/vmlinuz-XXXXXX)
docker cp "$cid:/boot/vmlinuz-virt" "$tmpvmlinuz"
# LC_ALL=C: BSD tr on macOS chokes on locales when scanning kernel bytes.
LC_ALL=C "$here/extract-vmlinux" "$tmpvmlinuz" > "$OUTDIR/vmlinux"
rm -f "$tmpvmlinuz"

echo "==> building rootfs.squashfs + initramfs-overlay"
tmptar=$(mktemp /tmp/wb-rootfs-XXXXXX)
docker export "$cid" -o "$tmptar"
docker run --rm \
    -e VOLUME_BASE_SIZE="$VOLUME_BASE_SIZE" \
    -e SUB="$SUB" \
    -v "$WARMBOX_HOME:/host" \
    -v "$here:/guest:ro" \
    -v "$tmptar:/rootfs.tar:ro" \
    alpine:3.22 sh -c '
        set -e
        apk add --no-cache squashfs-tools cpio gzip zstd kmod e2fsprogs e2fsprogs-extra >/dev/null 2>&1

        D="/host${SUB:+/$SUB}"
        mkdir -p "$D"

        rm -rf /rootfs && mkdir -p /rootfs
        tar -C /rootfs -xf /rootfs.tar
        # The all-RAM initramfs is redundant in the read-only base.
        rm -f /rootfs/initramfs.zst

        # --- read-only squashfs base ---
        mksquashfs /rootfs "$D/rootfs.squashfs" \
            -comp zstd -noappend -all-root -no-progress -quiet

        # --- base volume image (cloned for each new persistent volume) ---
        if [ ! -f /host/volume-base.img ]; then
            truncate -s "${VOLUME_BASE_SIZE:-2G}" /host/volume-base.img
            mke2fs -F -t ext4 -q -m 0 /host/volume-base.img
        fi

        # --- overlay boot initramfs ---
        krel=$(ls /rootfs/lib/modules | head -1)
        rm -rf /initrd && mkdir -p /initrd && cd /initrd
        zcat /rootfs/boot/initramfs-virt | cpio -idm --quiet

        # Add the modules the base boot needs that the stock initramfs lacks.
        # Missing ones are built into the kernel, so skip rather than fail.
        for m in overlay squashfs ext4 jbd2 mbcache crc16; do
            src=$(find /rootfs/lib/modules -name "$m.ko.gz" | head -1)
            [ -n "$src" ] || { echo "note: module $m not found (built-in?)" >&2; continue; }
            rel=".${src#/rootfs}"
            mkdir -p "$(dirname "$rel")"
            cp "$src" "$rel"
        done

        # busybox modprobe does not read gzipped modules; store plain .ko.
        find lib/modules -name "*.ko.gz" -exec gunzip -f {} \;
        depmod -b /initrd "$krel" >/dev/null 2>&1 || true

        # e2fs tools so the boot initramfs can fsck and grow a volume before it
        # is mounted (no host-side resize needed).
        for b in e2fsck resize2fs dumpe2fs; do
            bin=$(command -v "$b")
            mkdir -p "/initrd$(dirname "$bin")"
            cp "$bin" "/initrd$bin"
            ldd "$bin" | grep -oE "/[^ ]+\.so[^ ]*" | while read -r lib; do
                mkdir -p "/initrd$(dirname "$lib")"
                cp "$lib" "/initrd$lib" 2>/dev/null || true
            done
        done

        cp /guest/overlay-init init
        chmod +x init
        for a in sh mount mkdir modprobe sleep switch_root find; do
            [ -e "bin/$a" ] || ln -sf busybox "bin/$a"
        done
        [ -e bin/sh ] || ln -sf busybox bin/sh

        find . | cpio -o -H newc --quiet | zstd -19 -T0 -f --quiet -o "$D/initramfs-overlay"
    '
rm -f "$tmptar"

ls -lh "$OUTDIR/vmlinux" "$OUTDIR/initramfs.zst" \
    "$OUTDIR/initramfs-virt" "$OUTDIR/rootfs.squashfs" \
    "$OUTDIR/initramfs-overlay" "$WARMBOX_HOME/volume-base.img" 2>/dev/null || true
if [ -n "$VARIANT" ]; then
  echo "==> done. variant image '$VARIANT'. boot it with: warmbox create --image $VARIANT"
else
  echo "==> done. built-in image. run: warmbox service install --pool 1"
fi
