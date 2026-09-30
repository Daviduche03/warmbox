#!/bin/sh
# Install warmbox from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/Daviduche03/warmbox/v0.2.0/install.sh | sh
#
# The archive is verified against the release's checksums.txt *before* anything
# is written, so a corrupt or tampered download installs nothing.
#
# Environment:
#   WARMBOX_VERSION   tag to install (default: the latest release)
#   WARMBOX_PREFIX    where to put the binary (default: /usr/local/bin when
#                     running as root, otherwise ~/.local/bin)
#   WARMBOX_REPO      owner/repo to install from (default Daviduche03/warmbox)
#
# What this does NOT do: download the guest image. That is ~800 MB and warmbox
# fetches it itself, verified, on the next `warmbox setup`.

set -eu

REPO="${WARMBOX_REPO:-Daviduche03/warmbox}"
VERSION="${WARMBOX_VERSION:-}"
PREFIX="${WARMBOX_PREFIX:-}"

die() {
	printf 'install: %s\n' "$1" >&2
	exit 1
}

have() { command -v "$1" >/dev/null 2>&1; }
have curl || die "curl is required"
have tar || die "tar is required"

# --- what are we installing on? ---
os=$(uname -s)
arch=$(uname -m)
case "$os" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) die "unsupported OS: $os — warmbox runs on macOS (Apple Silicon) or Linux with KVM" ;;
esac
case "$arch" in
	arm64 | aarch64) arch=arm64 ;;
	x86_64 | amd64) arch=amd64 ;;
	*) die "unsupported architecture: $arch" ;;
esac
if [ "$os" = darwin ] && [ "$arch" = amd64 ]; then
	die "Intel Macs are not supported: vfkit needs Apple Silicon (guests are ARM64 only)"
fi

fetch() { # url dest
	curl -fSL --progress-bar "$1" -o "$2"
}

get() { # url -> stdout
	curl -fsSL "$1"
}

# --- which version? ---
if [ -z "$VERSION" ]; then
	VERSION=$(get "https://api.github.com/repos/${REPO}/releases/latest" |
		sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
	[ -n "$VERSION" ] || die "could not find the latest release of ${REPO}"
fi

# Release assets drop the leading v from the tag.
bare=${VERSION#v}
asset="warmbox_${bare}_${os}_${arch}.tar.gz"
base="https://github.com/${REPO}/releases/download/${VERSION}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "warmbox ${VERSION} for ${os}/${arch}"
fetch "${base}/${asset}" "${tmp}/${asset}" || die "download failed: ${base}/${asset}"
fetch "${base}/checksums.txt" "${tmp}/checksums.txt" || die "could not fetch checksums.txt"

# --- verify before touching the system ---
want=$(sed -n "s/^\([0-9a-f]\{64\}\)[[:space:]]\{1,\}[*]\{0,1\}${asset}\$/\1/p" "${tmp}/checksums.txt" | head -1)
[ -n "$want" ] || die "no checksum for ${asset} in checksums.txt"

if have sha256sum; then
	got=$(sha256sum "${tmp}/${asset}" | awk '{print $1}')
else
	have shasum || die "need sha256sum or shasum to verify the download"
	got=$(shasum -a 256 "${tmp}/${asset}" | awk '{print $1}')
fi

if [ "$got" != "$want" ]; then
	die "checksum mismatch for ${asset}
  got  ${got}
  want ${want}
Refusing to install."
fi
echo "checksum ok"

# --- install ---
if [ -z "$PREFIX" ]; then
	if [ "$(id -u)" = 0 ]; then
		PREFIX=/usr/local/bin
	else
		PREFIX="${HOME}/.local/bin"
	fi
fi

tar -xzf "${tmp}/${asset}" -C "$tmp"
[ -f "${tmp}/warmbox" ] || die "archive did not contain a warmbox binary"

mkdir -p "$PREFIX"
cp "${tmp}/warmbox" "${PREFIX}/warmbox"
chmod 0755 "${PREFIX}/warmbox"

# macOS kills an arm64 binary whose signature the copy invalidated.
if [ "$os" = darwin ] && have codesign; then
	codesign --force --sign - "${PREFIX}/warmbox" >/dev/null 2>&1 ||
		echo "install: warning: could not re-sign the binary; if it refuses to run, run: codesign --force --sign - ${PREFIX}/warmbox" >&2
fi

echo "installed ${PREFIX}/warmbox"

case ":${PATH}:" in
*":${PREFIX}:"*) ;;
*)
	echo
	echo "note: ${PREFIX} is not on your PATH. Add it:"
	echo "  export PATH=\"${PREFIX}:\$PATH\""
	;;
esac

cat <<'EOS'

next:
  warmbox setup                     # check the host, then noVNC + the guest image
  warmbox service install --pool 1  # run the daemon in the background

The dashboard binds 127.0.0.1:7070 — it is plaintext HTTP, so keep it off the
network. On a remote machine, tunnel to it instead:

  ssh -L 7070:127.0.0.1:7070 <user>@<this-host>     # then open http://localhost:7070
EOS
