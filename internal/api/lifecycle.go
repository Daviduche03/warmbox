package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// A desktop can be asked to stop by itself. The deadline lives in the
// catalog, not in memory, so it survives the daemon restarting and a
// desktop that was given a lifetime keeps it.

// applyTTL remembers when a desktop should stop by itself. A lifetime of
// zero means it never does.
func (s *Server) applyTTL(id string, ttl time.Duration) {
	if s.catalog == nil || id == "" {
		return
	}
	if ttl <= 0 {
		_ = s.catalog.ClearDesktopDeadline(id)
		return
	}
	_ = s.catalog.SetDesktopDeadline(id, time.Now().Add(ttl))
}

// extendDesktop pushes a desktop's auto-stop deadline back, or clears it so
// the desktop never stops by itself.
func (s *Server) extendDesktop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	vm, ok := s.visibleVM(w, r, id)
	if !ok {
		return
	}
	var req struct {
		TTLSeconds int `json:"ttl_seconds"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.TTLSeconds < 0 {
		writeErr(w, r, http.StatusBadRequest, "invalid_ttl",
			"ttl_seconds must be 0 or more", nil)
		return
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	s.applyTTL(vm.ID, ttl)
	out := map[string]any{"id": vm.ID, "ttlSeconds": req.TTLSeconds}
	if ttl > 0 {
		out["expiresAt"] = time.Now().Add(ttl).UTC().Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, out)
}

// StartExpiryReaper stops the desktops whose lifetime has run out. It runs
// until the context is cancelled, so the daemon owns its lifetime.
func (s *Server) StartExpiryReaper(ctx context.Context) {
	if s.catalog == nil {
		return
	}
	go func() {
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				s.expireDesktops()
			}
		}
	}()
}

// expireDesktops stops the desktops whose deadline has passed. A desktop that
// is already gone is not an error: the deadline outlived it.
func (s *Server) expireDesktops() {
	ids, err := s.catalog.ExpiredDesktops(time.Now())
	if err != nil {
		return
	}
	for _, id := range ids {
		vm, ok := s.mgr.Get(id)
		if !ok {
			_ = s.catalog.ClearDesktopDeadline(id)
			continue
		}
		if vm.Info().Workspace == "" {
			// Warm-pool capacity is the daemon's, not a user's: it is
			// replaced by the pool, not stopped.
			_ = s.catalog.ClearDesktopDeadline(id)
			continue
		}
		if err := s.mgr.Destroy(id); err == nil {
			_ = s.catalog.DeleteDesktop(id)
			_ = s.catalog.ClearDesktopDeadline(id)
			if s.log != nil {
				s.log.Write([]byte("api: desktop " + id + " reached its time limit and was stopped\n"))
			}
		}
	}
	// Idempotency answers are history: a key older than a day is not worth
	// keeping, and the table would otherwise grow without bound.
	if _, err := s.catalog.PruneIdempotency(time.Now().Add(-24 * time.Hour)); err != nil {
		return
	}
}

// --- guest-side state, proxied ---

// agentHistory lists the commands the guest has run. The agent keeps the
// record on the guest's own disk, so it outlives the host's process.
func (s *Server) agentHistory(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/history")
}

// agentProcesses lists what is running in the guest right now.
func (s *Server) agentProcesses(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/processes")
}

// agentScreenshot returns a PNG of the guest's screen.
func (s *Server) agentScreenshot(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/screenshot")
}

// agentScreen returns the guest's screen size.
func (s *Server) agentScreen(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/screen")
}

// agentInput injects a keystroke, a click or a pointer move into the guest's
// desktop. The guest does it with XTEST, so it works whether or not anyone is
// watching the screen.
func (s *Server) agentInput(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/input")
}
