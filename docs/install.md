# Installing and updating

The README has the three-line start. This page is the reference: what the
installer does, the package-manager routes, how to pin a version, and how
updating works.

## The installer

```sh
curl -fsSL https://raw.githubusercontent.com/Daviduche03/warmbox/master/install.sh | sh
```

Before expanding anything it downloads the release's sha256 and refuses to
install on a mismatch, so a corrupt or tampered download installs nothing. The
binary goes to `/usr/local/bin` when run as root, otherwise `~/.local/bin`, and
it never asks for sudo.

That URL picks which copy of the script runs, not which version you get: with
`WARMBOX_VERSION` unset, the script asks GitHub for the newest release tag. To
pin both, take the script from a tag and say so:

```sh
curl -fsSL https://raw.githubusercontent.com/Daviduche03/warmbox/v0.5.4/install.sh \
  | WARMBOX_VERSION=v0.5.4 sh
```

Also recognized: `WARMBOX_PREFIX` (where the binary lands) and `WARMBOX_REPO`
(which repository to install from).

## Package managers

The cask, the packages, and plain tarballs all come from the same
[releases page](https://github.com/Daviduche03/warmbox/releases):

```sh
brew install --cask Daviduche03/warmbox/warmbox   # macOS (or Linuxbrew)

sudo apt install ./warmbox_0.5.4_linux_amd64.deb # Debian/Ubuntu
sudo rpm -i warmbox_0.5.4_linux_amd64.rpm        # Fedora/RHEL
```

The `.deb` and `.rpm` deliberately do not fetch the guest image — that stays
`warmbox setup`. Or unpack the tarball for your platform and run the same
commands as everyone else: `./warmbox setup`, then
`./warmbox service install --pool 1`.

## Updating

The binary and the guest images move independently — the guest changes far less
often, and upgrading the CLI does not touch an image.

```sh
# the binary
curl -fsSL https://raw.githubusercontent.com/Daviduche03/warmbox/master/install.sh | sh

# the guest image for this platform (or the Images page in the dashboard)
warmbox setup                      # installs the default image
warmbox image pull <name>          # or a specific one

warmbox service restart            # picks up the new binary
```

The **Settings → Daemon** tab tells you when either half is behind: it compares
the running version against this repository's releases, and the installed image
against the published one.
