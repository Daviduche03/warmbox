#!/bin/sh
# Boot an image with the real daemon and wait for the desktop to come up.
#
#   ./deploy/guest/smoke.sh [name] [image-dir]
#
# Why the daemon and not QEMU/vfkit directly: the thing worth testing is what
# warmbox builds, not what a hand-written command line happens to do. A second
# copy of the device set in here would drift from the backend's, and then pass
# while the product was broken.
#
# This is the check that would have caught the bugs that shipped — a guest that
# never boots, a hypervisor flag that is refused, a readiness gate that never
# fires — before a release rather than after one. On a machine with no KVM
# (every CI runner) it falls back to QEMU's software emulation, which is slow but
# is the only way to boot an image on the machine about to publish it.
set -eu

NAME="${1:-xfce}"
IMGDIR="${2:-${WARMBOX_HOME:-$HOME/.warmbox}/images}"
BIN="${WARMBOX_BIN:-warmbox}"
TIMEOUT="${SMOKE_TIMEOUT:-420}"

die() { printf 'smoke: %s\n' "$1" >&2; exit 1; }
note() { printf 'smoke: %s\n' "$1" >&2; }

if [ ! -x "$BIN" ] && ! command -v "$BIN" >/dev/null 2>&1; then
	die "no warmbox binary at '$BIN' (set WARMBOX_BIN)"
fi
[ -d "$IMGDIR/$NAME" ] || die "no image '$NAME' under $IMGDIR"

# A free port rather than a fixed one: a busy runner should not turn into a flake.
PORT=$(python3 -c 'import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()')

# vfkit on macOS, QEMU everywhere else; QEMU needs to be told what to accelerate
# with, and `kvm` is the only honest answer when KVM is actually available.
BACKEND=""
ACCEL=""
if [ "$(uname -s)" = "Linux" ]; then
	BACKEND="--backend qemu"
	if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then
		note "accelerator: kvm"
	else
		note "no usable /dev/kvm — using QEMU software emulation (slow)"
		ACCEL="--accel tcg"
	fi
fi

WD=$(mktemp -d)
LOG="$WD/daemon.log"
DPID=""
cleanup() {
	if [ -n "$DPID" ]; then kill "$DPID" 2>/dev/null || true; fi
	wait 2>/dev/null || true
	rm -rf "$WD"
}
trap cleanup EXIT INT TERM

note "image '$NAME' from $IMGDIR, daemon on 127.0.0.1:$PORT"
# shellcheck disable=SC2086
"$BIN" daemon --workdir "$WD" --addr "127.0.0.1:$PORT" --image-dir "$IMGDIR" \
	--image "$NAME" --pool 0 $BACKEND $ACCEL >"$LOG" 2>&1 &
DPID=$!

i=0
while ! curl -sf -o /dev/null "http://127.0.0.1:$PORT/api/setup/status"; do
	i=$((i + 1))
	if [ "$i" -gt 120 ]; then
		echo "--- daemon log ---" >&2
		cat "$LOG" >&2
		die "the daemon never came up"
	fi
	sleep 0.5
done

JAR="$WD/cookies"
if ! curl -sf -c "$JAR" -X POST "http://127.0.0.1:$PORT/api/setup" \
	-H 'Content-Type: application/json' \
	-d '{"name":"Smoke","email":"smoke@example.invalid","password":"smoketest","workspace":"default"}' \
	-o /dev/null; then
	die "could not create the smoke account"
fi

note "booting (a create only answers once the guest has reported ready)"
code=$(curl -s -o "$WD/create.json" -w '%{http_code}' -b "$JAR" --max-time "$TIMEOUT" \
	-X POST "http://127.0.0.1:$PORT/api/desktops" -H 'Content-Type: application/json' \
	-d "{\"image\":\"$NAME\"}")

if [ "$code" != "201" ]; then
	echo "--- what the daemon said ---" >&2
	cat "$WD/create.json" >&2
	echo >&2
	echo "--- daemon log (last 30) ---" >&2
	tail -30 "$LOG" >&2
	die "'$NAME' did not boot (HTTP $code)"
fi

note "ok: $(cat "$WD/create.json")"
note "'$NAME' boots and reports ready"
