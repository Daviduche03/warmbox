// Package vnc implements a WebSocket-to-TCP bridge (a Go reimplementation of
// websockify) so a browser running noVNC can talk to a guest VNC server.
package vnc

import (
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
func Proxy(w http.ResponseWriter, r *http.Request, target string) {
	ws, err := Upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	conn, err := Dial(target)
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
