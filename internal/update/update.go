// Package update answers the question the dashboard's Daemon tab asks: is
// there a newer release of warmbox — or of its guest image — than the one
// installed here?
//
// The answer is fetched in the background and cached, for two reasons. GitHub's
// API allows 60 unauthenticated requests an hour, and /api/status is polled far
// more often than that. And a settings page must never wait on the network to
// render: State returns whatever the last check found, immediately.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config is what only the caller knows: which repository to ask, what version
// is running, and where the installed guest image's checksum is recorded.
type Config struct {
	// Repo is "owner/name" on GitHub.
	Repo string
	// Current is the running version (e.g. "v0.2.1"). A value that does not
	// parse as a version — a dev build — simply never reports an update.
	Current string
	// Arch is this host's GOARCH, for the guest-image row.
	Arch string
	// ImageURL is where the guest image for this architecture lives; its
	// .sha256 sidecar is the published checksum.
	ImageURL string
	// ImageRelease is the tag the guest images are published under, used to
	// build a link to that release page.
	ImageRelease string
	// ImageSHA returns the sha256 recorded when the guest image was installed,
	// the word "local" when it was built on this machine, or "" when nothing
	// was recorded (images installed before this file existed).
	ImageSHA func() string

	// APIBase and now are injectable so the tests never touch the network.
	APIBase string
	now     func() time.Time
}

// Binary is the state of the running daemon against its repository's releases.
type Binary struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	URL       string `json:"url"`
}

// Image is the state of the installed guest image against the published one.
// Known is false when nothing was recorded at install time; Current is then
// "" (never recorded) or "local" (built here), and Available stays false.
type Image struct {
	Arch      string `json:"arch"`
	Current   string `json:"current,omitempty"`
	Latest    string `json:"latest,omitempty"`
	Known     bool   `json:"known"`
	Available bool   `json:"available"`
	URL       string `json:"url"`
}

// State is what GET /api/status reports. Both children are nil until the first
// check finishes; Error records a failed check so the dashboard can say so
// instead of implying "up to date".
type State struct {
	CheckedAt string  `json:"checked_at,omitempty"`
	Error     string  `json:"error,omitempty"`
	Binary    *Binary `json:"binary,omitempty"`
	Image     *Image  `json:"image,omitempty"`
}

// Manager runs the check on a timer and hands out the latest answer.
type Manager struct {
	cfg    Config
	client *http.Client
	ttl    time.Duration // how long a good answer stays fresh
	retry  time.Duration // how long to wait after a failed one

	mu    sync.Mutex
	state State
}

// New builds a Manager. Nothing is fetched until Start or Refresh is called.
func New(cfg Config) *Manager {
	if cfg.APIBase == "" {
		cfg.APIBase = "https://api.github.com"
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.ImageSHA == nil {
		cfg.ImageSHA = func() string { return "" }
	}
	return &Manager{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
		ttl:    6 * time.Hour,
		retry:  15 * time.Minute,
	}
}

// Start checks immediately in the background, then repeats on a timer — sooner
// after a failure, so a laptop that was offline at boot recovers on its own.
// It stops when ctx is cancelled.
func (m *Manager) Start(ctx context.Context) {
	go func() {
		wait := time.Duration(0)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if m.Refresh(ctx) {
				wait = m.ttl
			} else {
				wait = m.retry
			}
		}
	}()
}

// State returns the last known answer without blocking on anything.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Refresh asks again and stores the result. It reports whether both checks
// succeeded; a failure is stored too, so the dashboard can show "couldn't
// check" rather than a false "up to date".
func (m *Manager) Refresh(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	st := State{CheckedAt: m.cfg.now().UTC().Format(time.RFC3339)}
	ok := true

	tag, url, err := m.latestVersion(ctx)
	if err != nil {
		st.Error = err.Error()
		ok = false
	} else {
		available, comparable := newer(m.cfg.Current, tag)
		st.Binary = &Binary{
			Current:   versionOr(m.cfg.Current),
			Latest:    tag,
			Available: comparable && available,
			URL:       url,
		}
	}

	installed := strings.TrimSpace(m.cfg.ImageSHA())
	img := &Image{
		Arch:    m.cfg.Arch,
		Current: installed,
		Known:   isSHA(installed),
		URL:     fmt.Sprintf("https://github.com/%s/releases/tag/%s", m.cfg.Repo, m.cfg.ImageRelease),
	}
	published, err := m.publishedImageSHA(ctx)
	switch {
	case err != nil:
		if st.Error == "" {
			st.Error = err.Error()
		} else {
			st.Error += "; " + err.Error()
		}
		ok = false
	case img.Known && !strings.EqualFold(installed, published):
		img.Available = true
	}
	img.Latest = published
	st.Image = img

	m.mu.Lock()
	m.state = st
	m.mu.Unlock()
	return ok
}

// latestVersion returns the newest released version tag and its page. It
// deliberately does not use /releases/latest: the guest images live in a
// release tagged "images", and if that release is ever flagged Latest again,
// /releases/latest resolves to a tag with no binary behind it — the exact bug
// that made install.sh ask for warmbox_images_linux_amd64.tar.gz and 404.
func (m *Manager) latestVersion(ctx context.Context) (tag, url string, err error) {
	u := fmt.Sprintf("%s/repos/%s/releases?per_page=30", m.cfg.APIBase, m.cfg.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "warmbox/"+versionOr(m.cfg.Current))
	resp, err := m.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	var releases []struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&releases); err != nil {
		return "", "", err
	}
	// The list comes newest-first; take the first tag that looks like a
	// version and isn't a draft or a pre-release.
	for _, r := range releases {
		if r.Draft || r.Prerelease {
			continue
		}
		if _, ok := parseVersion(r.TagName); ok {
			return r.TagName, r.HTMLURL, nil
		}
	}
	return "", "", fmt.Errorf("no released version found for %s", m.cfg.Repo)
}

// publishedImageSHA reads the checksum sidecar of the published guest image —
// the same file `warmbox setup` verifies against.
func (m *Manager) publishedImageSHA(ctx context.Context) (string, error) {
	u := m.cfg.ImageURL + ".sha256"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 || !isSHA(fields[0]) {
		return "", fmt.Errorf("%s: unreadable checksum", u)
	}
	return strings.ToLower(fields[0]), nil
}

func versionOr(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}

// isSHA reports whether s is a full sha256 hex digest.
func isSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// parseVersion turns "v0.2.1" into [0 2 1]. Tags that aren't plain dotted
// numbers — "dev", "v0.3.0-rc1", "latest" — are not versions and report false.
func parseVersion(s string) ([]int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if s == "" {
		return nil, false
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// newer reports whether b is newer than a, and whether the two could be
// compared at all. Comparing 0.9 against 0.10 numerically matters: a string
// comparison would call 0.9 newer.
func newer(a, b string) (available, comparable bool) {
	av, okA := parseVersion(a)
	bv, okB := parseVersion(b)
	if !okA || !okB {
		return false, false
	}
	for i := 0; i < len(av) || i < len(bv); i++ {
		x, y := 0, 0
		if i < len(av) {
			x = av[i]
		}
		if i < len(bv) {
			y = bv[i]
		}
		if x != y {
			return y > x, true
		}
	}
	return false, true
}
