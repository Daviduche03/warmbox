// Package vnc implements a WebSocket-to-TCP bridge (a Go reimplementation of
// websockify) so a browser running noVNC can talk to a guest VNC server.
package vnc

import (
	"fmt"
	"io"
	"net/http"

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
//
// Both failures here are reported to log, because neither is visible to the
// browser: the socket is already upgraded by the time the guest is dialled, so
// a target that refuses to connect looks to noVNC exactly like a desktop that
// is simply slow to paint — it sits on "Connecting…" forever. Without this the
// daemon said nothing at all, and a broken console left no trace anywhere.
func Proxy(w http.ResponseWriter, r *http.Request, target string, log io.Writer) {
	if log == nil {
		log = io.Discard
	}
	ws, err := Upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Fprintf(log, "vnc: %s: websocket upgrade failed: %v\n", target, err)
		return
	}
	defer ws.Close()

	conn, err := Dial(target)
	if err != nil {
		fmt.Fprintf(log, "vnc: %s: could not reach the guest's VNC server: %v\n", target, err)
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
