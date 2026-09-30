#!/bin/bash
# Tune an Omarchy guest for a GPU-less VM streamed over VNC.
#
# The compositor renders through Mesa llvmpipe (Apple's Virtualization.framework
# exposes no 3D), so every screen change costs a full software composite. These
# settings cut that cost and are all CPU-neutral:
#   - a smaller output (render cost is ~proportional to pixels)
#   - animations off, blur/shadow already off upstream
#   - wayvnc serves the session at 60 fps (it defaults to 30 and discards frames
#     the compositor already rendered); cursor is left client-side
#   - trim polling-heavy shell widgets and idle daemons
#
# Run inside the guest as root. Resolution must be set before the session
# starts (a live mode change crashes quickshell), so this writes the config and
# the caller reboots.
set -euo pipefail

USER_NAME="${OMARCHY_USER:-omarchy}"
HOME_DIR="$(getent passwd "$USER_NAME" | cut -d: -f6)"
RESOLUTION="${OMARCHY_VM_RESOLUTION:-800x600}"
VNC_FPS="${OMARCHY_VM_VNC_FPS:-60}"
note() { printf '==> %s\n' "$*"; }

# 1. Output resolution. Lower = cheaper software composite per change.
cat > "$HOME_DIR/.config/hypr/monitors.lua" <<EOF
-- warmbox: headless VM served over VNC. Smaller output = cheaper llvmpipe
-- composite per screen change (the cost is ~proportional to pixels).
local omarchy_gdk_scale = 1
local omarchy_monitor_scale = 1
hl.env("GDK_SCALE", tostring(omarchy_gdk_scale))
hl.monitor({ output = "", mode = "${RESOLUTION}@60", position = "auto", scale = omarchy_monitor_scale })
EOF
note "output set to ${RESOLUTION}@60"

# 2. wayvnc on 5900, autostarted with the session.
cat > "$HOME_DIR/.config/hypr/autostart.lua" <<EOF
-- warmbox: serve the Hyprland session over VNC. -f ${VNC_FPS}: don't discard the
-- frames the compositor already renders (wayvnc defaults to 30).
o.launch_on_start("wayvnc -f ${VNC_FPS} 0.0.0.0 5900")
EOF
note "wayvnc autostart (${VNC_FPS} fps)"

# 3. Disable window animations (blur/shadow are off by default upstream).
LOOK="$HOME_DIR/.config/hypr/looknfeel.lua"
if [[ -f $LOOK ]] && ! grep -qs 'warmbox: disable animations' "$LOOK"; then
  cat >> "$LOOK" <<'EOF'

-- warmbox: disable animations for software rendering.
hl.config({ animations = { enabled = false } })
EOF
  note "animations disabled"
fi

# 4. Trim polling-heavy shell widgets and service plugins.
if command -v jq >/dev/null 2>&1; then
  cfg="$HOME_DIR/.config/omarchy/shell.json"
  if [[ -f $cfg ]]; then
    tmp=$(mktemp)
    if jq 'def heavy: ["omarchy.weather","omarchy.agents","omarchy.tray","omarchy.system-update","omarchy.bluetooth","omarchy.monitor"];
      def trim: map(select(.id as $i | (heavy | index($i)) | not));
      .version = (.version // 1)
      | .bar.layout.left   = ((.bar.layout.left   // []) | trim)
      | .bar.layout.center = ((.bar.layout.center // []) | trim)
      | .bar.layout.right  = ((.bar.layout.right  // []) | trim)
      | .disabledPlugins = ((.disabledPlugins // []) + ["omarchy.media","omarchy.nightlight","omarchy.battery"] | unique)' "$cfg" > "$tmp"; then
      mv "$tmp" "$cfg"; note "trimmed bar widgets and plugins"
    else
      rm -f "$tmp"
    fi
  fi
fi

# 5. Daemons with nothing to do in a VM. docker/containerd in particular cost
#    ~130 MB and CPU we want for the compositor.
for s in docker.socket docker.service containerd.service cups.socket cups.service \
         cups-browsed.service power-profiles-daemon.service upower.service udisks2.service; do
  systemctl disable --now "$s" >/dev/null 2>&1 || true
done
pacman -Rns --noconfirm udiskie >/dev/null 2>&1 || true
for s in gvfs-daemon.service gvfs-metadata.service at-spi-dbus-bus.service; do
  runuser -u "$USER_NAME" -- systemctl --user disable --now "$s" >/dev/null 2>&1 || true
done
note "trimmed idle daemons"

# 6. zram swap for cheap headroom on a small VM.
pacman -S --needed --noconfirm zram-generator >/dev/null 2>&1 || true
if [[ ! -f /etc/systemd/zram-generator.conf ]] && command -v zram-generator >/dev/null 2>&1; then
  printf '[zram0]\nzram-size = min(ram, 2048)\ncompression-algorithm = zstd\n' > /etc/systemd/zram-generator.conf
  systemctl daemon-reload || true
  note "zram configured"
fi

# 7. Shrink the image. The package cache, hardware firmware (a VM has no such
#    hardware) and old journals are pure weight in a guest image.
pacman -Scc --noconfirm >/dev/null 2>&1 || true
pacman -Rns --noconfirm linux-firmware linux-firmware-whence >/dev/null 2>&1 || true
journalctl --vacuum-size=16M >/dev/null 2>&1 || true
rm -rf /var/cache/pacman/pkg/* /root/aquamarine /root/install-omarchy.sh 2>/dev/null || true
note "image slimmed (cache, firmware, journals)"

chown -R "$USER_NAME:$USER_NAME" "$HOME_DIR/.config/hypr" "$HOME_DIR/.config/omarchy" 2>/dev/null || true
note "done — reboot to start the session"
