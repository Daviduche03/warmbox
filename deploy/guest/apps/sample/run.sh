#!/bin/sh
# Sample warmbox app. Uses xmessage (plain X11, no GL) with a terminal fallback.
TEXT="Hello from an app installed with warmbox-app.

This app lives in /opt/apps/agent-demo and shows up in the menu.
It is on the writable volume, so it persists and clones with the machine."

if command -v xmessage >/dev/null 2>&1; then
    exec xmessage -center -title "Agent Demo" "$TEXT"
fi
exec xfce4-terminal -T "Agent Demo" -x sh -c 'printf "%s\n\n" "$0"; printf "(press enter)"; read _' "$TEXT"
