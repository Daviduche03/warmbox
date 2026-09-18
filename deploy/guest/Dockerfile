# warmbox guest image — a minimal arm64 Linux desktop that boots under vfkit.
#
# Build (arm64 host):
#   docker build --platform linux/arm64 -t warmbox-guest:latest \
#     --build-arg BROWSER=epiphany deploy/guest
#
# BROWSER may be: none | netsurf | epiphany | firefox | chromium
# DESKTOP may be: xfce | lxqt
# THEME may be: default | win11 | ambiance (ambiance pairs with lxqt)
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

FROM golang:1.25-alpine AS agentbuild
WORKDIR /src
COPY agent/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /warmbox-agent .

FROM alpine:3.22

ARG BROWSER=chromium
ARG DESKTOP=xfce
ARG THEME=default

# Base GUI stack: XFCE (default) or LXQt-Ambiance. Both share TigerVNC +
# dbus + fonts; only the session layer differs so squashfs/overlay/VNC stay
# identical across images.
RUN set -eux; \
    apk add --no-cache \
        tigervnc \
        xsetroot xdpyinfo xprop \
        dbus dbus-x11 \
        adwaita-icon-theme hicolor-icon-theme papirus-icon-theme \
        shared-mime-info gdk-pixbuf \
        font-misc-misc font-dejavu \
        iproute2 socat bash procps coreutils \
        kmod linux-virt \
        xmessage \
        darkhttpd \
        curl zstd; \
    case "${DESKTOP}" in \
        xfce) apk add --no-cache xfce4 xfce4-terminal ;; \
        lxqt) apk add --no-cache lxqt-desktop qterminal \
                kvantum lxqt-themes obconf-qt ;; \
        *) echo "unknown DESKTOP=${DESKTOP}" >&2; exit 1 ;; \
    esac

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

# Chromium is the browser that actually works in this microVM: it runs with
# --no-sandbox (bubblewrap/user-namespaces are blocked by the kernel) and
# --disable-gpu (no GPU in the VM). Patch its launcher so the app menu, links
# and exo-open all get the working flags with Chromium's real hicolor icon, and
# make it XFCE's preferred browser.
RUN set -eux; \
    if command -v chromium >/dev/null 2>&1; then \
        sed -i 's|^Exec=/usr/bin/chromium-browser|Exec=/usr/bin/chromium-browser --no-sandbox --disable-gpu --disable-dev-shm-usage|' /usr/share/applications/chromium.desktop; \
        rm -f /usr/share/applications/xfce4-web-browser.desktop; \
        if [ -f /etc/xdg/xfce4/helpers.rc ]; then \
            sed -i 's/^WebBrowser=.*/WebBrowser=chromium/' /etc/xdg/xfce4/helpers.rc; \
        fi; \
        update-desktop-database /usr/share/applications 2>/dev/null || true; \
    fi

# warmbox-app: a tiny /opt/apps convention that turns a directory + app.json
# into a menu entry. Apps land on the writable volume, so they persist and
# clone with the machine. A sample app ships baked in.
COPY apps /apps
RUN set -eux; \
    install -m 0755 /apps/warmbox-app /usr/local/bin/warmbox-app; \
    /usr/local/bin/warmbox-app install /apps/sample; \
    /usr/local/bin/warmbox-app install /apps/dashboard; \
    rm -rf /apps

# Win11 hand-rolled theme (THEME=win11, xfce only): see below.
# Ambiance theme (THEME=ambiance, lxqt): warm dark Lubuntu-style panel +
# openbox decorations + Kvantum bridge + Papirus icons. Ships as
# themes/ambiance/ (lxqt.conf, openbox/rc.xml, kvantum theme, autostart).
COPY themes /themes
RUN set -eux; \
    if [ "${THEME}" = "ambiance" ]; then \
        mkdir -p /etc/xdg/lxqt /etc/xdg/openbox \
                  /usr/share/Kvantum /etc/xdg/autostart; \
        cp -r /themes/ambiance/lxqt/* /etc/xdg/lxqt/ 2>/dev/null || true; \
        cp /themes/ambiance/openbox/rc.xml /etc/xdg/openbox/rc.xml 2>/dev/null || true; \
        cp -r /themes/ambiance/Kvantum/* /usr/share/Kvantum/ 2>/dev/null || true; \
        cp /themes/ambiance/autostart/*.desktop /etc/xdg/autostart/ 2>/dev/null || true; \
        gtk-update-icon-cache -qtf /usr/share/icons/Papirus 2>/dev/null || true; \
    fi; \
    if [ "${THEME}" = "win11" ]; then \
        apk add --no-cache xfce4-whiskermenu-plugin xfce4-docklike-plugin; \
        # GTK + window-manager themes. The GTK theme imports Adwaita (GTK's
        # built-in resource) so every widget still renders, then overrides the
        # palette for the Fluent flat look. \
        mkdir -p /usr/share/themes/Win11/gtk-3.0 /usr/share/themes/Win11/xfwm4; \
        cp /themes/win11/gtk-3.0/* /usr/share/themes/Win11/gtk-3.0/; \
        cp /themes/win11/xfwm4/* /usr/share/themes/Win11/xfwm4/; \
        # Wallpaper + start-button icon (registered in the hicolor icon theme). \
        mkdir -p /usr/share/backgrounds \
                 /usr/share/icons/hicolor/48x48/apps; \
        cp /themes/win11/wallpaper.png /usr/share/backgrounds/warmbox-win11.png; \
        cp /themes/win11/start.png /usr/share/icons/hicolor/48x48/apps/warmbox-start.png; \
        # System-wide XFCE defaults: panel layout, theme pick, window-manager
        # behavior, desktop (wallpaper), and start menu. \
        mkdir -p /etc/xdg/xfce4/panel \
                 /etc/xdg/xfce4/xfconf/xfce-perchannel-xml \
                 /etc/xdg/autostart; \
        cp /themes/win11/panel/default.xml /etc/xdg/xfce4/panel/default.xml; \
        cp /themes/win11/panel/docklike.rc /etc/xdg/xfce4/panel/docklike.rc; \
        cp /themes/win11/xfconf/*.xml /etc/xdg/xfce4/xfconf/xfce-perchannel-xml/; \
        # Session autostart applies the wallpaper to whatever monitor the VNC
        # display reports (name is unpredictable under Xvnc). \
        install -m 0755 /themes/win11/wallpaper.sh /usr/local/bin/warmbox-wallpaper.sh; \
        cp /themes/win11/autostart/*.desktop /etc/xdg/autostart/; \
        gtk-update-icon-cache -qtf /usr/share/icons/hicolor 2>/dev/null || true; \
    fi; \
    rm -rf /themes

COPY --from=agentbuild /warmbox-agent /usr/local/bin/warmbox-agent
COPY init /init
RUN chmod +x /init /usr/local/bin/warmbox-agent

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
