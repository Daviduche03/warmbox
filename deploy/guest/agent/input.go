// Synthetic desktop input and screen capture.
//
// The VNC path cannot be used for typing: TigerVNC and x11vnc translate RFB
// key events into keycodes through their own XKB state, which is empty unless
// the X server itself has a keymap, so the events never match anything and are
// dropped. XTEST (xdotool) goes straight into the X server, so it always
// works and does not depend on the VNC server at all.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// The tools are called with a deadline: a wedged X server must not wedge the
// agent's request handler.
const (
	xdoTimeout     = 10 * time.Second
	captureTimeout = 30 * time.Second
)

// display returns the X display to drive. The agent inherits DISPLAY from the
// init script, but an explicit env always wins.
func display() string {
	if d := os.Getenv("DISPLAY"); d != "" {
		return d
	}
	return ":0"
}

// runTool runs a helper binary against the X display and returns its stdout.
func runTool(bin string, args ...string) ([]byte, error) {
	return runToolTimeout(xdoTimeout, bin, args...)
}

// runToolTimeout is runTool with an explicit deadline: capturing a whole
// screen takes longer than a keystroke.
func runToolTimeout(d time.Duration, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+display())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s: %s", bin, msg)
	}
	return stdout.Bytes(), nil
}

// --- POST /input ---

type inputReq struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Key    string `json:"key"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Button string `json:"button"`
}

func handleInput(w http.ResponseWriter, r *http.Request) {
	var req inputReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("invalid json: %w", err))
		return
	}
	var args []string
	switch req.Type {
	case "text":
		if req.Text == "" {
			fail(w, http.StatusBadRequest, fmt.Errorf("text required"))
			return
		}
		// --clearmodifiers drops a stuck Ctrl/Alt, --delay keeps the
		// keystrokes from arriving as one impossible burst.
		args = []string{"type", "--clearmodifiers", "--delay", "20", "--", req.Text}
	case "key":
		if req.Key == "" {
			fail(w, http.StatusBadRequest, fmt.Errorf("key required"))
			return
		}
		args = []string{"key", "--clearmodifiers", "--", req.Key}
	case "click":
		button, ok := mouseButton(req.Button)
		if !ok {
			fail(w, http.StatusBadRequest, fmt.Errorf("unknown mouse button %q", req.Button))
			return
		}
		if err := checkCoords(req.X, req.Y); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		// xdotool chains commands: mousemove then click in one call.
		args = []string{"mousemove", strconv.Itoa(req.X), strconv.Itoa(req.Y), "click", button}
	case "move":
		if err := checkCoords(req.X, req.Y); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		args = []string{"mousemove", strconv.Itoa(req.X), strconv.Itoa(req.Y)}
	default:
		fail(w, http.StatusBadRequest, fmt.Errorf("unknown input type %q", req.Type))
		return
	}
	if _, err := runTool("xdotool", args...); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// mouseButton maps a button name to the number xdotool wants: its `click`
// command parses the button with atoi, so "left" would become 0.
func mouseButton(name string) (string, bool) {
	switch name {
	case "", "left", "1":
		return "1", true
	case "middle", "2":
		return "2", true
	case "right", "3":
		return "3", true
	}
	return "", false
}

// checkCoords rejects coordinates xdotool could not parse: a negative number
// looks like an option to getopt and would abort the command.
func checkCoords(x, y int) error {
	if x < 0 || y < 0 {
		return fmt.Errorf("coordinates must not be negative")
	}
	return nil
}

// --- GET /screen ---

func handleScreen(w http.ResponseWriter, _ *http.Request) {
	if width, height, ok := screenGeometry(); ok {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"width": width, "height": height})
		return
	}
	fail(w, http.StatusInternalServerError, fmt.Errorf("cannot read screen geometry"))
}

// screenGeometry asks xdotool first and falls back to xdpyinfo, which ships
// in a different set of images.
func screenGeometry() (int, int, bool) {
	if out, err := runTool("xdotool", "getdisplaygeometry"); err == nil {
		f := strings.Fields(string(out))
		if len(f) == 2 {
			w, e1 := strconv.Atoi(f[0])
			h, e2 := strconv.Atoi(f[1])
			if e1 == nil && e2 == nil && w > 0 && h > 0 {
				return w, h, true
			}
		}
	}
	out, err := runTool("xdpyinfo")
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "dimensions") {
			continue
		}
		// "dimensions: 1280x800 pixels"
		fields := strings.FieldsFunc(line, func(r rune) bool {
			return r < '0' || r > '9'
		})
		if len(fields) >= 2 {
			w, e1 := strconv.Atoi(fields[0])
			h, e2 := strconv.Atoi(fields[1])
			if e1 == nil && e2 == nil && w > 0 && h > 0 {
				return w, h, true
			}
		}
	}
	return 0, 0, false
}

// --- GET /screenshot ---

// handleScreenshot captures the root window as a PNG. ImageMagick's `import`
// does it in one step; `xwd` is the fallback for images without it.
func handleScreenshot(w http.ResponseWriter, _ *http.Request) {
	png, err := capturePNG()
	if err != nil {
		if errors.Is(err, errNoCaptureTool) {
			fail(w, http.StatusNotImplemented, err)
			return
		}
		fail(w, http.StatusInternalServerError, err)
		return
	}
	if len(png) == 0 {
		fail(w, http.StatusInternalServerError, fmt.Errorf("empty screenshot"))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
}

// errNoCaptureTool means the image ships neither ImageMagick nor xwd.
var errNoCaptureTool = errors.New("no screenshot tool available (need imagemagick or xwd)")

// capturePNG grabs the root window, preferring ImageMagick and falling back
// to xwd plus a conversion when `import` is missing or fails.
func capturePNG() ([]byte, error) {
	if _, err := exec.LookPath("import"); err == nil {
		out, err := runToolTimeout(captureTimeout, "import", "-window", "root", "png:-")
		if err == nil {
			return out, nil
		}
		// `import` can fail on its own (an X server that refuses the
		// capture); xwd may still work.
		if _, xerr := exec.LookPath("xwd"); xerr != nil {
			return nil, err
		}
	}
	if _, err := exec.LookPath("xwd"); err != nil {
		return nil, errNoCaptureTool
	}
	tmp, err := os.CreateTemp("", "warmbox-shot-*.xwd")
	if err != nil {
		return nil, err
	}
	path := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(path)
	if _, err := runToolTimeout(captureTimeout, "xwd", "-root", "-silent", "-out", path); err != nil {
		return nil, err
	}
	out, err := runToolTimeout(captureTimeout, "convert", path, "png:-")
	if err != nil {
		return nil, err
	}
	return out, nil
}
