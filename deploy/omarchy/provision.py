#!/usr/bin/env python3
"""Provision an Omarchy guest over vfkit's virtio-serial console.

Boots the image headless, logs in on the serial console, mounts a virtiofs
share holding the warmbox provisioning assets, runs setup.sh, and powers off.
This is how the agent gets into an image whose only access is its console.
"""
import os
import pty
import select
import subprocess
import sys
import time

disk, efivars, share, password = (sys.argv[1:5] + ["omarchy"])[:5]
fps = sys.argv[5] if len(sys.argv) > 5 else "60"

ARGS = [
    "vfkit",
    "--cpus", "4", "--memory", "3072",
    "--bootloader", f"efi,variable-store={efivars},create",
    "--device", f"virtio-blk,path={disk}",
    "--device", "virtio-net,nat",
    "--device", "virtio-rng",
    "--device", f"virtio-fs,sharedDir={share},mountTag=warmbox-provision",
    "--device", "virtio-serial,stdio",
]

master, slave = pty.openpty()
err = open("/tmp/warmbox-provision.err", "wb", buffering=0)
proc = subprocess.Popen(ARGS, stdin=slave, stdout=slave, stderr=err)
os.close(slave)
buf = b""


def send(s):
    sys.stdout.write(f"\n>>> {s!r}\n")
    sys.stdout.flush()
    os.write(master, s.encode())


def expect(pat, timeout):
    global buf
    buf = b""
    end = time.time() + timeout
    while time.time() < end:
        r, _, _ = select.select([master], [], [], 0.5)
        if not r:
            continue
        try:
            data = os.read(master, 4096)
        except OSError:
            break
        if not data:
            break
        buf += data
        sys.stdout.write(data.decode(errors="replace"))
        sys.stdout.flush()
        if pat in buf:
            return True
    sys.stdout.write(f"\n!!! timeout waiting for {pat!r}\n")
    return False


def main():
    if not expect(b"login:", 180):
        return 1
    send("omarchy\n")
    if not expect(b"assword:", 20):
        return 1
    send(password + "\n")
    if not expect(b"$ ", 20):
        return 1
    send(f"echo {password} | sudo -S sh -c 'mkdir -p /mnt && mount -t virtiofs warmbox-provision /mnt && bash /mnt/setup.sh /mnt'\n")
    ok = expect(b"provisioning complete", 240)
    expect(b"$ ", 20)
    send(f"echo {password} | sudo -S poweroff\n")
    time.sleep(6)
    return 0 if ok else 1


if __name__ == "__main__":
    try:
        rc = main()
    finally:
        try:
            proc.terminate()
        except Exception:
            pass
    sys.exit(rc)
