// Fetching the guest image: it is several hundred MB, so this resumes a partial
// download when the server allows it and verifies a sha256 before the image is
// allowed anywhere near the boot path. Nothing lands in the workdir until the
// whole archive is down and checksum-clean.
package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// FetchChecksum reads a "<hex>  <name>" checksum file (the format sha256sum and
// goreleaser both write) and returns the hex digest.
func FetchChecksum(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return "", fmt.Errorf("%s: empty checksum file", url)
	}
	return fields[0], nil
}

// Download fetches url into dst. An existing dst that already matches wantSHA is
// left alone; otherwise a partial dst is resumed with a Range request (and a
// server that ignores the range simply restarts from zero). A finished file that
// fails the checksum is removed so the next attempt cannot resume from it.
func Download(url, dst, wantSHA string, progress io.Writer) error {
	if wantSHA != "" {
		if got, err := SHA256File(dst); err == nil && strings.EqualFold(got, wantSHA) {
			return nil // already have it
		}
	}

	var have int64
	if st, err := os.Stat(dst); err == nil {
		have = st.Size()
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var f *os.File
	switch {
	case resp.StatusCode == http.StatusPartialContent:
		f, err = os.OpenFile(dst, os.O_WRONLY|os.O_APPEND, 0o644)
	case resp.StatusCode == http.StatusOK:
		have = 0
		f, err = os.Create(dst)
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		// We asked to resume past the end: the file is bigger than the
		// artifact (a truncated/older copy), so start over.
		if err := os.Remove(dst); err != nil {
			return err
		}
		return fmt.Errorf("%s: local copy did not match the remote; removed it, please retry", filepath.Base(dst))
	default:
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	if err != nil {
		return err
	}

	total := have + resp.ContentLength
	if progress != nil && have > 0 && total > 0 {
		fmt.Fprintf(progress, "  resuming at %s of %s\n", humanBytes(have), humanBytes(total))
	}
	if _, err := copyProgress(f, resp.Body, have, total, progress); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	if wantSHA != "" {
		got, err := SHA256File(dst)
		if err != nil {
			return err
		}
		if !strings.EqualFold(got, wantSHA) {
			_ = os.Remove(dst)
			return fmt.Errorf("checksum mismatch:\n  got  %s\n  want %s", got, wantSHA)
		}
	}
	return nil
}

// Extract expands a downloaded image archive into dir.
func Extract(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return extractTo(dir, zr)
}

// FetchBuiltin downloads the built-in image archive and installs it into
// workDir. The download and the extraction each land in a temporary path and
// only finished files are moved into place, so an interrupted run or a bad
// checksum can never leave a half-written kernel for the daemon to boot. The
// partial download is kept on failure so a retry resumes instead of restarting.
func FetchBuiltin(workDir, url, wantSHA string, progress io.Writer) error {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(workDir, ".image-download.tar.zst")
	if err := Download(url, tmp, wantSHA, progress); err != nil {
		return err // keep tmp: the next run resumes
	}

	staging := filepath.Join(workDir, ".image-staging")
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	if err := Extract(tmp, staging); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	for _, e := range entries {
		if err := os.Rename(filepath.Join(staging, e.Name()), filepath.Join(workDir, e.Name())); err != nil {
			return err
		}
	}
	_ = os.RemoveAll(staging)
	_ = os.Remove(tmp)
	return nil
}

func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyProgress copies src to dst, reporting to w at most twice a second. done is
// the offset already present (a resumed download); total may be 0 when the
// server doesn't say.
func copyProgress(dst io.Writer, src io.Reader, done, total int64, w io.Writer) (int64, error) {
	if w == nil {
		return io.Copy(dst, src)
	}
	buf := make([]byte, 1<<20)
	last := time.Now()
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return done, werr
			}
			done += int64(n)
			if time.Since(last) > 500*time.Millisecond {
				last = time.Now()
				fmt.Fprintf(w, "\r  %s", humanBytes(done))
				if total > 0 {
					fmt.Fprintf(w, " / %s (%.0f%%)", humanBytes(total), float64(done)/float64(total)*100)
				}
				fmt.Fprint(w, "   ")
			}
		}
		if rerr == io.EOF {
			fmt.Fprintf(w, "\r  %s", humanBytes(done))
			if total > 0 {
				fmt.Fprintf(w, " / %s (100%%)", humanBytes(total))
			}
			fmt.Fprintln(w)
			return done, nil
		}
		if rerr != nil {
			return done, rerr
		}
	}
}

// humanBytes renders a byte count for progress output.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
