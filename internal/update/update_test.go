package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The releases endpoint must skip anything that is not a version tag. It exists
// because the guest images are published under a tag called "images", and when
// GitHub marked that release Latest, install.sh resolved its version from
// /releases/latest and asked for warmbox_images_linux_amd64.tar.gz.
func TestLatestVersionSkipsNonVersionReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/repos/o/r/releases") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		io.WriteString(w, `[
			{"tag_name":"images","html_url":"https://github.com/o/r/releases/tag/images"},
			{"tag_name":"v0.3.0-rc1","prerelease":true,"html_url":"https://x/v0.3.0-rc1"},
			{"tag_name":"v0.2.2","html_url":"https://github.com/o/r/releases/tag/v0.2.2"},
			{"tag_name":"v0.2.1","html_url":"https://github.com/o/r/releases/tag/v0.2.1"}
		]`)
	}))
	defer srv.Close()

	m := New(Config{Repo: "o/r", APIBase: srv.URL})
	tag, url, err := m.latestVersion(context.Background())
	if err != nil {
		t.Fatalf("latestVersion: %v", err)
	}
	if tag != "v0.2.2" {
		t.Errorf("tag = %q, want v0.2.2 (the first *released version*, not the images tag or a pre-release)", tag)
	}
	if url != "https://github.com/o/r/releases/tag/v0.2.2" {
		t.Errorf("url = %q", url)
	}
}

func TestLatestVersionNoVersionAtAll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"tag_name":"images","html_url":"https://x/images"}]`)
	}))
	defer srv.Close()

	m := New(Config{Repo: "o/r", APIBase: srv.URL})
	if _, _, err := m.latestVersion(context.Background()); err == nil {
		t.Fatal("expected an error when no version release exists, got nil")
	}
}

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		current, latest  string
		want, comparable bool
	}{
		{"v0.2.1", "v0.2.2", true, true},   // the ordinary case
		{"v0.2.2", "v0.2.2", false, true},  // already there
		{"v0.2.3", "v0.2.2", false, true},  // ahead of the release (a dev build)
		{"v0.9.0", "v0.10.0", true, true},  // numerically, 10 > 9
		{"v0.10.0", "v0.9.0", false, true}, // ...and 9 < 10, not a string compare
		{"v1.0.0", "v0.9.9", false, true},
		{"dev", "v0.2.2", false, false}, // built from source: not comparable
		{"", "v0.2.2", false, false},
		{"v0.2.1", "images", false, false}, // never treat a non-version as newer
	} {
		got, comparable := newer(tc.current, tc.latest)
		if got != tc.want || comparable != tc.comparable {
			t.Errorf("newer(%q, %q) = (%v, %v), want (%v, %v)",
				tc.current, tc.latest, got, comparable, tc.want, tc.comparable)
		}
	}
}

// Refresh is the whole feature: it must compare the installed image against the
// published one, keep a locally built image quiet, and survive a bad network.
func TestRefresh(t *testing.T) {
	const installed = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const published = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	var shaBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases"):
			io.WriteString(w, `[{"tag_name":"v0.3.0","html_url":"https://github.com/o/r/releases/tag/v0.3.0"}]`)
		case strings.HasSuffix(r.URL.Path, ".sha256"):
			fmt.Fprintf(w, "%s  warmbox-image-arm64.tar.zst\n", shaBody)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	fixed := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	base := func(image func() string) *Manager {
		return New(Config{
			Repo:         "o/r",
			Current:      "v0.2.1",
			Arch:         "arm64",
			ImageURL:     srv.URL + "/o/r/releases/download/images/warmbox-image-arm64.tar.zst",
			ImageRelease: "images",
			ImageSHA:     image,
			APIBase:      srv.URL,
			now:          func() time.Time { return fixed },
		})
	}

	t.Run("installed image is behind", func(t *testing.T) {
		shaBody = published
		m := base(func() string { return installed })
		if !m.Refresh(context.Background()) {
			t.Fatal("Refresh reported failure against a healthy server")
		}
		st := m.State()
		if st.CheckedAt != "2026-10-01T12:00:00Z" {
			t.Errorf("CheckedAt = %q", st.CheckedAt)
		}
		if st.Binary == nil || !st.Binary.Available || st.Binary.Latest != "v0.3.0" {
			t.Errorf("Binary = %+v, want an available v0.3.0 update", st.Binary)
		} else if st.Binary.Current != "v0.2.1" {
			t.Errorf("Binary.Current = %q", st.Binary.Current)
		}
		if st.Image == nil || !st.Image.Available || !st.Image.Known {
			t.Errorf("Image = %+v, want a known image that is behind", st.Image)
		} else if st.Image.URL != "https://github.com/o/r/releases/tag/images" {
			t.Errorf("Image.URL = %q", st.Image.URL)
		}
	})

	t.Run("installed image is current", func(t *testing.T) {
		shaBody = published
		m := base(func() string { return strings.ToUpper(published) }) // case-insensitive
		if !m.Refresh(context.Background()) {
			t.Fatal("Refresh reported failure")
		}
		img := m.State().Image
		if img.Available {
			t.Errorf("Image.Available = true for a matching checksum")
		}
		if !img.Known {
			t.Error("Image.Known = false, but a checksum was recorded")
		}
	})

	t.Run("nothing recorded", func(t *testing.T) {
		shaBody = published
		m := base(func() string { return "" })
		if !m.Refresh(context.Background()) {
			t.Fatal("Refresh reported failure")
		}
		img := m.State().Image
		if img.Available || img.Known || img.Current != "" {
			t.Errorf("Image = %+v, want an unknown, non-updatable image", img)
		}
	})

	t.Run("built locally", func(t *testing.T) {
		shaBody = published
		m := base(func() string { return "local" })
		if !m.Refresh(context.Background()) {
			t.Fatal("Refresh reported failure")
		}
		img := m.State().Image
		if img.Available || img.Known || img.Current != "local" {
			t.Errorf("Image = %+v, want a local build flagged as such", img)
		}
	})

	t.Run("checksum endpoint is down", func(t *testing.T) {
		shaBody = ""
		m := base(func() string { return installed })
		if m.Refresh(context.Background()) {
			t.Fatal("Refresh reported success when the checksum could not be read")
		}
		st := m.State()
		if st.Error == "" {
			t.Error("Error is empty; the dashboard needs a reason to show")
		}
		if st.Image == nil || st.Image.Available {
			t.Errorf("Image = %+v, must not claim an update when the check failed", st.Image)
		}
		if st.Binary == nil {
			t.Fatal("Binary = nil; a partial success should still be reported")
		}
	})
}

// State is handed to the HTTP handler while the background loop writes it.
func TestStateIsConcurrencySafe(t *testing.T) {
	m := New(Config{Repo: "o/r", APIBase: "http://127.0.0.1:0"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			m.mu.Lock()
			m.state.Error = "x"
			m.mu.Unlock()
		}
	}()
	for i := 0; i < 200; i++ {
		_ = m.State()
	}
	<-done
}
