// Package vnc implements a WebSocket-to-TCP bridge (a Go reimplementation of
// websockify) so a browser running noVNC can talk to a guest VNC server.
package vnc

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/gorilla/websocket"
)

// Upgrader negotiates the "binary" subprotocol used by noVNC.
var Upgrader = websocket.Upgrader{
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 32 * 1024,
	Subprotocols:    []string{"binary"},
	CheckOrigin:     func(*http.Request) bool { return true },
}

// Proxy upgrades the HTTP request to a WebSocket and pipes frames to the raw
// TCP target (host:port of a guest VNC server).
func Proxy(w http.ResponseWriter, r *http.Request, target string) {
	ws, err := Upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	conn, err := dial(target)
	if err != nil {
		return
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 32*1024)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if _, err := conn.Write(data); err != nil {
			break
		}
	}
	conn.Close()
	<-done
}

// dial connects to the guest VNC server. On macOS an unsigned process is denied
// local-network access to vmnet guests (connect returns EHOSTUNREACH), while
// Apple-signed binaries are allowed — so we fall back to an /usr/bin/nc child.
// On other platforms, or when the direct dial works, we use a plain TCP conn.
func dial(target string) (io.ReadWriteCloser, error) {
	c, err := net.DialTimeout("tcp", target, 4*time.Second)
	if err == nil {
		return c, nil
	}
	if runtime.GOOS != "darwin" {
		return nil, err
	}
	host, port, splitErr := net.SplitHostPort(target)
	if splitErr != nil {
		return nil, err
	}
	nc := "/usr/bin/nc"
	if _, statErr := os.Stat(nc); statErr != nil {
		return nil, err
	}
	cmd := exec.Command(nc, host, port)
	stdin, perr := cmd.StdinPipe()
	if perr != nil {
		return nil, err
	}
	stdout, perr := cmd.StdoutPipe()
	if perr != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if perr := cmd.Start(); perr != nil {
		return nil, fmt.Errorf("nc fallback: %w", perr)
	}
	return &procConn{r: stdout, w: stdin, kill: func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}}, nil
}

// procConn adapts a child process's stdin/stdout to an io.ReadWriteCloser.
type procConn struct {
	r    io.ReadCloser
	w    io.WriteCloser
	kill func()
}

func (c *procConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *procConn) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c *procConn) Close() error {
	_ = c.r.Close()
	_ = c.w.Close()
	c.kill()
	return nil
}
