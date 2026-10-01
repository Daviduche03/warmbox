package desktop

import (
	"bytes"
	"sync"
)

// Excerpt keeps the tail of a process's output in memory.
//
// A hypervisor that refuses to start says why on stderr, and that sentence is
// the difference between "exit status 1" — which is all a failed exec tells you
// — and something a person can act on. The output is bounded because a backend
// that spams must not grow the daemon, and a write error must never take down
// the process it is only observing.
type Excerpt struct {
	mu  sync.Mutex
	buf []byte
	max int
}

// NewExcerpt keeps at most max bytes of the most recent output; max <= 0 means
// a sane default.
func NewExcerpt(max int) *Excerpt { return &Excerpt{max: max} }

// limit is the bound in force, so the zero value behaves like a constructed one.
func (e *Excerpt) limit() int {
	if e.max <= 0 {
		return 64 << 10 // 64 KiB
	}
	return e.max
}

// Write appends to the tail, dropping the oldest bytes past the limit. It always
// reports success: a process whose stderr we are merely watching must not see a
// short write because of us.
func (e *Excerpt) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.buf = append(e.buf, p...)
	if max := e.limit(); len(e.buf) > max {
		e.buf = e.buf[len(e.buf)-max:]
	}
	return len(p), nil
}

// String is the tail, trimmed, or "" when there is nothing to say.
func (e *Excerpt) String() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return string(bytes.TrimSpace(e.buf))
}
