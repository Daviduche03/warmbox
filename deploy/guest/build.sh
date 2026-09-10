#!/bin/sh
# Build the warmbox guest image and extract its boot artifacts.
#
#   ./deploy/guest/build.sh
#
# Produces, in $WARMBOX_HOME (default ~/.warmbox):
#   vmlinux           uncompressed arm64 kernel        (vfkit needs a raw Image)
#   initramfs.zst     zstd cpio initramfs = whole rootfs (all-RAM boot)
#   initramfs-virt    Alpine boot initramfs             (disk boot)
#   rootfs.img        ext4 image containing the rootfs  (disk boot; low RAM)
#
# Disk boot is the recommended path: the kernel pages the rootfs in on demand,
# so the VM needs ~192-256 MiB instead of ~2.5 GiB. Each VM must boot its own
# clone of rootfs.img (warmbox does this automatically).
#
# Env:
#   BROWSER       none|netsurf|epiphany|firefox|chromium   (default epiphany)
#   IMAGE         docker image tag                          (default warmbox-guest:latest)
#   WARMBOX_HOME  output directory                          (default ~/.warmbox)
#   PLATFORM      docker build platform                     (default linux/arm64)
#   WITH_DISK     1 to also produce rootfs.img              (default 1)
#   DISK_SIZE     ext4 image size, e.g. 1900M               (default 1900M)

set -eu

here=$(cd "$(dirname "$0")" && pwd)
BROWSER="${BROWSER:-epiphany}"
IMAGE="${IMAGE:-warmbox-guest:latest}"
WARMBOX_HOME="${WARMBOX_HOME:-$HOME/.warmbox}"
PLATFORM="${PLATFORM:-linux/arm64}"
WITH_DISK="${WITH_DISK:-1}"
DISK_SIZE="${DISK_SIZE:-1900M}"

echo "==> building $IMAGE (BROWSER=$BROWSER, platform=$PLATFORM)"
docker build --platform "$PLATFORM" -t "$IMAGE" --build-arg "BROWSER=$BROWSER" "$here"

mkdir -p "$WARMBOX_HOME"

echo "==> extracting kernel + initramfs"
cid=$(docker create --platform "$PLATFORM" "$IMAGE")
trap 'docker rm -f "$cid" >/dev/null 2>&1 || true' EXIT INT TERM

docker cp "$cid:/initramfs.zst" "$WARMBOX_HOME/initramfs.zst"
docker cp "$cid:/boot/initramfs-virt" "$WARMBOX_HOME/initramfs-virt"

tmpvmlinuz=$(mktemp /tmp/vmlinuz-XXXXXX)
docker cp "$cid:/boot/vmlinuz-virt" "$tmpvmlinuz"
# LC_ALL=C: BSD tr on macOS chokes on locales when scanning kernel bytes.
LC_ALL=C "$here/extract-vmlinux" "$tmpvmlinuz" > "$WARMBOX_HOME/vmlinux"
rm -f "$tmpvmlinuz"

if [ "$WITH_DISK" = "1" ]; then
    echo "==> building ext4 rootfs.img ($DISK_SIZE)"
    tmptar=$(mktemp /tmp/wb-rootfs-XXXXXX.tar)
    docker export "$cid" -o "$tmptar"
    docker run --rm -v "$WARMBOX_HOME:/host" -v "$tmptar:/rootfs.tar:ro" \
        alpine:3.22 sh -c '
            set -e
            apk add --no-cache e2fsprogs >/dev/null 2>&1
            rm -rf /rootfs && mkdir -p /rootfs
            tar -C /rootfs -xf /rootfs.tar
            # The all-RAM initramfs is redundant on the disk; drop it.
            rm -f /rootfs/initramfs.zst
            ln -sf /init /rootfs/sbin/init
            truncate -s "'"$DISK_SIZE"'" /host/rootfs.img
            mke2fs -q -t ext4 -d /rootfs -F -m 0 /host/rootfs.img
        '
    rm -f "$tmptar"
fi

ls -lh "$WARMBOX_HOME/vmlinux" "$WARMBOX_HOME/initramfs.zst" "$WARMBOX_HOME/initramfs-virt" \
    "$WARMBOX_HOME/rootfs.img" 2>/dev/null || true
echo "==> done. run: warmbox daemon --mem 256 --pool 2"
