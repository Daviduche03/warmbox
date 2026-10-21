package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"warmbox/internal/catalog"
	"warmbox/internal/desktop"
)

// A failure must be a shape a program can read: "ok" says it failed, "code"
// names the failure, and the request id ties it to the log. A plain sentence
// with no code leaves a client scraping prose to find out what happened.
func TestErrorsUseTheEnvelope(t *testing.T) {
	cfg := &desktop.Config{WorkDir: t.TempDir(), APIAddr: "127.0.0.1:7070"}
	s := &Server{mgr: desktop.NewManager(cfg, io.Discard), cfg: cfg, log: io.Discard}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/desktops/nope", nil)
	req = req.WithContext(withRequestIDValue(req.Context(), "req_test123"))

	// An unknown desktop is the one failure every client can hit.
	s.visibleVM(rec, req, "nope")

	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("error body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if env["ok"] != false {
		t.Errorf("ok = %v, want false", env["ok"])
	}
	if env["type"] != "error" {
		t.Errorf("type = %v, want error", env["type"])
	}
	if env["code"] != "unknown_desktop" {
		t.Errorf("code = %v, want unknown_desktop", env["code"])
	}
	if env["message"] == "" || env["message"] == nil {
		t.Error("the envelope has no message")
	}
	if env["requestId"] != "req_test123" {
		t.Errorf("requestId = %v, want the request's id", env["requestId"])
	}
	inner, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("error = %v, want the structured inner error", env["error"])
	}
	if inner["code"] != "unknown_desktop" {
		t.Errorf("inner code = %v, want unknown_desktop", inner["code"])
	}
	if inner["status"] != float64(http.StatusNotFound) {
		t.Errorf("inner status = %v, want 404", inner["status"])
	}
}

// A result says so too, so a client can branch on one field.
func TestSuccessSaysOK(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]any{"id": "abc123"})
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	if body["id"] != "abc123" {
		t.Errorf("id = %v, want abc123", body["id"])
	}
}

// Every request gets an id, and it is echoed in a header so a caller can
// quote it in a bug report.
func TestEveryRequestGetsAnID(t *testing.T) {
	rec := httptest.NewRecorder()
	var seen string
	withRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = requestID(r)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if seen == "" {
		t.Fatal("the request got no id")
	}
	if got := rec.Header().Get("X-Request-Id"); got != seen {
		t.Errorf("X-Request-Id = %q, want %q", got, seen)
	}
	if !strings.HasPrefix(seen, "req_") {
		t.Errorf("id = %q, want it to look like a request id", seen)
	}
}

// A list that grows past one page must not force a client to take the whole
// thing: the page says where it is and whether more is behind it.
func TestPaginationCutsAPageAndDescribesTheRest(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	page, info := paginate(items, 2, "", func(s string) string { return s })
	if len(page) != 2 || page[0] != "a" || page[1] != "b" {
		t.Fatalf("first page = %v, want a,b", page)
	}
	if !info.HasMore {
		t.Error("hasMore = false, want true")
	}
	if info.Limit != 2 {
		t.Errorf("limit = %d, want 2", info.Limit)
	}
	// The cursor is opaque, but it must walk the list.
	decoded, err := decodeCursor(info.NextCursor)
	if err != nil || decoded != "b" {
		t.Fatalf("cursor = %q (err %v), want it to point at b", info.NextCursor, err)
	}
	page, info = paginate(items, 2, decoded, func(s string) string { return s })
	if len(page) != 2 || page[0] != "c" {
		t.Fatalf("second page = %v, want c,d", page)
	}
	// The last page says there is nothing behind it.
	page, info = paginate(items, 2, "d", func(s string) string { return s })
	if len(page) != 1 || page[0] != "e" {
		t.Fatalf("last page = %v, want e", page)
	}
	if info.HasMore {
		t.Error("hasMore = true on the last page")
	}
	if info.NextCursor != "" {
		t.Errorf("nextCursor = %q, want empty on the last page", info.NextCursor)
	}
}

// A limit outside the allowed range is a client mistake worth naming.
func TestPaginationRejectsABadLimit(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/desktops?limit=0", nil)
	_, _, ok := readPage(rec, req)
	if ok {
		t.Fatal("limit=0 was accepted")
	}
	var env map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env["code"] != "invalid_limit" {
		t.Errorf("code = %v, want invalid_limit", env["code"])
	}
}

// A create that costs money must not be billed twice when the answer to the
// first attempt is lost. The same key returns the same desktop; the same key
// with a different body is a mistake and is reported as one.
func TestIdempotencyKeyReturnsTheSameAnswer(t *testing.T) {
	db, err := catalog.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	defer db.Close()
	s := &Server{catalog: db, log: io.Discard}

	body := `{"image":"xfce"}`
	hash := bodyHash([]byte(body))

	// Nothing has answered yet.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/desktops", strings.NewReader(body))
	if _, found, err := s.idempotentAnswer(rec, req, "key-1", hash); found || err != nil {
		t.Fatalf("first ask: found=%v err=%v, want nothing yet", found, err)
	}

	// The create happens and its answer is remembered.
	if err := db.PutIdempotency(catalog.IdempotencyRecord{
		Workspace: "", Key: "key-1", BodyHash: hash, Response: `{"ok":true,"id":"abc123"}`,
	}); err != nil {
		t.Fatalf("store: %v", err)
	}

	// The retry gets that answer.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/desktops", strings.NewReader(body))
	answer, found, err := s.idempotentAnswer(rec, req, "key-1", hash)
	if !found || err != nil {
		t.Fatalf("retry: found=%v err=%v, want the stored answer", found, err)
	}
	if !strings.Contains(answer, "abc123") {
		t.Errorf("answer = %q, want the desktop that was created", answer)
	}

	// The same key with a different body is a mistake, not a second desktop.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/desktops", strings.NewReader(`{"image":"omarchy"}`))
	_, _, err = s.idempotentAnswer(rec, req, "key-1", bodyHash([]byte(`{"image":"omarchy"}`)))
	if err == nil {
		t.Fatal("the same key with a different body was accepted")
	}
	var env map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env["code"] != "idempotency_key_reused" {
		t.Errorf("code = %v, want idempotency_key_reused", env["code"])
	}
}

// A desktop can be told to stop by itself, and the deadline survives the
// daemon restarting because it lives in the catalog.
func TestDesktopDeadlineIsRemembered(t *testing.T) {
	db, err := catalog.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	defer db.Close()
	s := &Server{catalog: db, log: io.Discard}

	s.applyTTL("abc123", time.Hour)
	at, ok := db.DesktopDeadline("abc123")
	if !ok {
		t.Fatal("the deadline was not remembered")
	}
	if until := time.Until(at); until < 59*time.Minute || until > time.Hour+time.Minute {
		t.Errorf("deadline in %v, want about an hour", until)
	}

	// A desktop with no lifetime never stops by itself.
	s.applyTTL("abc123", 0)
	if _, ok := db.DesktopDeadline("abc123"); ok {
		t.Error("the deadline survived being cleared")
	}
}

// The reaper stops the desktops whose time is up, and leaves the rest alone.
func TestExpiredDesktopsAreStopped(t *testing.T) {
	db, err := catalog.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	defer db.Close()
	if err := db.SetDesktopDeadline("gone", time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if err := db.SetDesktopDeadline("later", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	ids, err := db.ExpiredDesktops(time.Now())
	if err != nil {
		t.Fatalf("expired: %v", err)
	}
	if len(ids) != 1 || ids[0] != "gone" {
		t.Errorf("expired = %v, want just the one that is due", ids)
	}
}

// A call to a desktop that is still booting waits for it rather than
// failing: the agent comes up a moment after the VM does, and a caller that
// creates a desktop and immediately runs a command should not have to retry.
func TestAgentCallsWaitForADesktopThatIsStillComingUp(t *testing.T) {
	cfg := &desktop.Config{WorkDir: t.TempDir(), APIAddr: "127.0.0.1:7070"}
	mgr := desktop.NewManager(cfg, io.Discard)
	s := &Server{mgr: mgr, cfg: cfg, log: io.Discard}

	// A desktop that does not exist is a 404, not a wait: the caller asked
	// for something that is not there.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/desktops/nope/exec", nil)
	s.agentProxy(rec, req, "nope", "/exec")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a desktop that does not exist", rec.Code)
	}
}
