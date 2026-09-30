// Dial connects to a host:port, falling back to nc on macOS when the direct
// dial is refused (unsigned processes get EHOSTUNREACH to vmnet guests).
package vnc

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// Dial connects to a host:port, falling back to nc on macOS when the direct
// dial is refused (unsigned processes get EHOSTUNREACH to vmnet guests).
func Dial(target string) (net.Conn, error) {
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
	return &pipeConn{
		r:      stdout,
		w:      stdin,
		local:  dummyAddr("local"),
		remote: dummyAddr(target),
		kill: func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		},
	}, nil
}

// pipeConn adapts a child process's stdin/stdout to net.Conn. Deadlines are
// no-ops (the nc pipe doesn't support them).
type pipeConn struct {
	r             io.ReadCloser
	w             io.WriteCloser
	local, remote net.Addr
	kill          func()
}

type dummyAddr string

func (a dummyAddr) Network() string { return "tcp" }
func (a dummyAddr) String() string  { return string(a) }

func (c *pipeConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *pipeConn) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c *pipeConn) Close() error {
	_ = c.r.Close()
	_ = c.w.Close()
	c.kill()
	return nil
}
func (c *pipeConn) LocalAddr() net.Addr              { return c.local }
func (c *pipeConn) RemoteAddr() net.Addr             { return c.remote }
func (c *pipeConn) SetDeadline(time.Time) error      { return nil }
func (c *pipeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *pipeConn) SetWriteDeadline(time.Time) error { return nil }
