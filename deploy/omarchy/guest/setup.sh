#!/bin/bash
# Provision a booted Omarchy guest: install the warmbox agent + readiness unit
# and apply the VM tuning. Run as root, either from the mounted provision share
# or with a directory holding these files as $1.
set -euo pipefail
SRC="${1:-$(cd "$(dirname "$0")" && pwd)}"
echo "==> provisioning from $SRC"

install -m0755 "$SRC/warmbox-agent" /usr/local/bin/warmbox-agent
install -m0755 "$SRC/warmbox-ready" /usr/local/bin/warmbox-ready
install -m0644 "$SRC/warmbox-agent.service" /etc/systemd/system/warmbox-agent.service
install -m0644 "$SRC/warmbox-ready.service" /etc/systemd/system/warmbox-ready.service
systemctl daemon-reload
systemctl enable --now warmbox-agent.service warmbox-ready.service
sleep 1
echo "==> warmbox-agent: $(systemctl is-active warmbox-agent.service)  healthz: $(curl -sf localhost:7077/healthz || echo FAILED)"

bash "$SRC/tune.sh"
echo "==> provisioning complete"
