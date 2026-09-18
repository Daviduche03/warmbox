// Package imagepack packs and unpacks guest images as compressed tar archives
// (disk.raw + efi-vars.fd + meta.json), so an image can be downloaded as a
// single artifact and expanded into the images directory on first use.
//
// A raw disk image is mostly zeros and free space, so zstd takes the Omarchy
// image from ~8.4 GiB on disk to ~3.9 GiB that travels.
package imagepack

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// members are the files that make up a packed image: either an EFI disk
// (disk.raw [+ efi-vars.fd]) or an overlay image (vmlinux + rootfs.squashfs +
// initramfs-overlay), plus an optional meta.json. Missing members are skipped.
var members = []string{
	"disk.raw", "efi-vars.fd", "meta.json",
	"vmlinux", "rootfs.squashfs", "initramfs-overlay",
	"initramfs.zst", "initramfs-virt",
}

// isImage reports whether dir holds a bootable image.
func isImage(dir string) bool {
	if exists(filepath.Join(dir, "disk.raw")) {
		return true
	}
	return exists(filepath.Join(dir, "vmlinux")) &&
		exists(filepath.Join(dir, "rootfs.squashfs")) &&
		exists(filepath.Join(dir, "initramfs-overlay"))
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Pack writes imageDir/name as a zstd-compressed tar archive. If out is empty
// it defaults to imageDir/<name>.tar.zst.
func Pack(imageDir, name, out string) (string, error) {
	dir := filepath.Join(imageDir, name)
	if !isImage(dir) {
		return "", fmt.Errorf("image %q has no disk.raw or squashfs set at %s", name, dir)
	}
	if out == "" {
		out = filepath.Join(imageDir, name+".tar.zst")
	}

	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer f.Close()

	zw, err := zstd.NewWriter(f, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return "", err
	}
	tw := tar.NewWriter(zw)

	for _, name := range members {
		p := filepath.Join(dir, name)
		st, err := os.Stat(p)
		if err != nil {
			continue // optional member
		}
		hdr, err := tar.FileInfoHeader(st, "")
		if err != nil {
			return "", err
		}
		hdr.Name = name
		if err := tw.WriteHeader(hdr); err != nil {
			return "", err
		}
		src, err := os.Open(p)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(tw, src); err != nil {
			src.Close()
			return "", err
		}
		src.Close()
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return out, f.Close()
}

// Pull expands a packed image (a local path or an http(s) URL) into
// imageDir/name, replacing any existing files there.
func Pull(imageDir, name, src string) error {
	var r io.ReadCloser
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		resp, err := http.Get(src)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("GET %s: %s", src, resp.Status)
		}
		r = resp.Body
	} else {
		f, err := os.Open(src)
		if err != nil {
			return err
		}
		r = f
	}
	defer r.Close()

	zr, err := zstd.NewReader(r)
	if err != nil {
		return err
	}
	defer zr.Close()

	dir := filepath.Join(imageDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		base := filepath.Base(hdr.Name)
		if !allowed(base) {
			continue
		}
		out := filepath.Join(dir, base)
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		if _, err := copySparse(f, tr); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		_ = os.Chmod(out, 0o644)
	}
	return nil
}

// copySparse copies src to dst, leaving a hole (seek) for every all-zero chunk
// so a mostly-empty disk image does not allocate its full logical size.
func copySparse(dst *os.File, src io.Reader) (int64, error) {
	const chunk = 1 << 20 // 1 MiB
	buf := make([]byte, chunk)
	var total int64
	for {
		n, err := io.ReadFull(src, buf)
		if n > 0 {
			b := buf[:n]
			if allZero(b) {
				if _, serr := dst.Seek(int64(n), io.SeekCurrent); serr != nil {
					return total, serr
				}
			} else if _, werr := dst.Write(b); werr != nil {
				return total, werr
			}
			total += int64(n)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return total, err
		}
	}
	return total, dst.Truncate(total)
}

func allZero(b []byte) bool {
	return len(b) > 0 && bytes.Count(b, []byte{0}) == len(b)
}

func allowed(name string) bool {
	for _, m := range members {
		if name == m {
			return true
		}
	}
	return false
}
