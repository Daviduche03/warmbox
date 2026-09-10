# warmbox guest image — a minimal arm64 Linux desktop that boots under vfkit.
#
# Build (arm64 host):
#   docker build --platform linux/arm64 -t warmbox-guest:latest \
#     --build-arg BROWSER=epiphany deploy/guest
#
# BROWSER may be: none | netsurf | epiphany | firefox | chromium
# The resulting image contains /initramfs.zst (the whole rootfs, zstd) and
# /boot/vmlinuz-virt (compressed kernel). See `warmbox setup` for extraction.
#
# Compression: the initramfs is zstd, not gzip. The kernel has CONFIG_RD_ZSTD=y
# and decompresses zstd several times faster than gzip -9, which is most of the
# cold-boot time. We keep the file named *.zst so the format is obvious.
#
# NOTE: pinned to Alpine 3.22 on purpose. Alpine 3.24 ships gdk-pixbuf 2.44,
# which routes SVG icons through glycin (bwrap-sandboxed loaders). That sandbox
# fails inside a minimal microVM and aborts GTK apps (xfce4-panel, xfdesktop).
# 3.22 uses gdk-pixbuf 2.42 + the classic librsvg pixbuf loader.

FROM alpine:3.22

ARG BROWSER=epiphany

RUN set -eux; \
    apk add --no-cache \
        tigervnc \
        xsetroot xdpyinfo xprop \
        xfce4 xfce4-terminal \
        dbus dbus-x11 \
        adwaita-icon-theme hicolor-icon-theme \
        shared-mime-info gdk-pixbuf \
        font-misc-misc font-dejavu \
        iproute2 socat bash procps coreutils \
        kmod linux-virt \
        curl zstd

# Optional browser.
RUN set -eux; \
    case "${BROWSER}" in \
        none) ;; \
        netsurf)   apk add --no-cache netsurf ;; \
        epiphany)  apk add --no-cache epiphany ;; \
        firefox)   apk add --no-cache firefox ;; \
        chromium)  apk add --no-cache chromium ;; \
        *) echo "unknown BROWSER=${BROWSER}" >&2; exit 1 ;; \
    esac

COPY init /init
RUN chmod +x /init

# Regenerate caches that the GUI stack needs at runtime.
RUN update-mime-database /usr/share/mime 2>/dev/null || true
RUN (gdk-pixbuf-query-loaders --update-cache 2>/dev/null || \
     gdk-pixbuf-query-loaders > /usr/lib/gdk-pixbuf-2.0/2.10.0/loaders.cache 2>/dev/null || true)
RUN gtk-update-icon-cache -qtf /usr/share/icons/hicolor 2>/dev/null || true
RUN gtk-update-icon-cache -qtf /usr/share/icons/Adwaita 2>/dev/null || true

# Slim the rootfs: drop docs/man/info and translations (the image runs under the
# C locale), plus apk metadata and build scratch. Trims the initramfs and the
# amount of memory the kernel must decompress into RAM.
# Also drop autostarts that make no sense in a headless VM (power management,
# keyring) — they only produce warnings and startup work.
RUN set -eux; \
    rm -rf \
        /usr/share/man \
        /usr/share/doc \
        /usr/share/info \
        /usr/share/locale/* \
        /var/cache/apk/* \
        /tmp/* \
        /root/.cache; \
    rm -f \
        /etc/xdg/autostart/xfce4-power-manager.desktop \
        /etc/xdg/autostart/gnome-keyring-pkcs11.desktop \
        /etc/xdg/autostart/gnome-keyring-secrets.desktop

# Pack the entire rootfs as a zstd-compressed cpio initramfs.
RUN set -eux; \
    cd /; \
    find . -xdev \
        \( -path ./proc -o -path ./sys -o -path ./dev -o -path ./tmp \) -prune -o -print \
    | cpio -o -H newc --quiet \
    | zstd -19 -T0 --quiet -o /initramfs.zst; \
    ls -lh /initramfs.zst /boot/vmlinuz-virt
