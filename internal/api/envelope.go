package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// Every answer the API gives has the same shape, so a client never has to
// guess whether a body is a result or a failure, or scrape a sentence out of
// prose to find out what went wrong. "ok" says which it is, "code" names the
// failure in a stable way a program can branch on, and "message" stays a
// sentence for a human. The request id is echoed in the body and in a header,
// so one failure in the log can be tied to one caller's report.

type apiError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Status  int            `json:"status"`
	Details map[string]any `json:"details,omitempty"`
}

type errorEnvelope struct {
	OK        bool     `json:"ok"`
	Type      string   `json:"type"`
	Status    int      `json:"status"`
	Code      string   `json:"code"`
	Message   string   `json:"message"`
	Error     apiError `json:"error"`
	RequestID string   `json:"requestId"`
}

type apiCtxKey int

const requestIDKey apiCtxKey = 1

// requestID returns the id given to this request, or "" when the request did
// not come through withRequestID.
func requestID(r *http.Request) string {
	if r == nil {
		return ""
	}
	if v, ok := r.Context().Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(b[:])
}

// withRequestID gives every request an id, puts it in the context and echoes
// it back in a header.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withRequestIDValue puts a known id in the context, so a test can check
// that the id reaches the envelope.
func withRequestIDValue(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// writeJSON sends a result. Every object body gains "ok": true, so a client
// can branch on one field instead of on the status code alone.
func writeJSON(w http.ResponseWriter, code int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeErr(w, nil, http.StatusInternalServerError, "internal_error",
			"could not encode the response", nil)
		return
	}
	if obj, ok := asObject(body); ok {
		obj["ok"] = json.RawMessage("true")
		if body, err = json.Marshal(obj); err != nil {
			writeErr(w, nil, http.StatusInternalServerError, "internal_error",
				"could not encode the response", nil)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(body)
	_, _ = w.Write([]byte("\n"))
}

// asObject re-reads an encoded body as a JSON object, so a field can be added
// to it. A body that is not an object (an array, a bare string) is left alone.
func asObject(body []byte) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		return nil, false
	}
	return obj, true
}

// writeErr sends a failure in the envelope. The code is the machine-readable
// half; the message is for a person. details carries anything a client needs
// to act — a "retryable" flag, for instance.
func writeErr(w http.ResponseWriter, r *http.Request, status int, code, msg string, details map[string]any) {
	env := errorEnvelope{
		OK:        false,
		Type:      "error",
		Status:    status,
		Code:      code,
		Message:   msg,
		Error:     apiError{Code: code, Message: msg, Status: status, Details: details},
		RequestID: requestID(r),
	}
	body, err := json.Marshal(env)
	if err != nil {
		body = []byte(`{"ok":false,"type":"error","code":"internal_error","message":"could not encode the error"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
	_, _ = w.Write([]byte("\n"))
}

// fail reports an operational failure without echoing internals: the detail
// goes to the daemon log, the caller gets a generic message. Use it for
// store/db/fs errors; our own validation messages are safe to return verbatim.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, status int, code, msg string, err error) {
	if err != nil && s.log != nil {
		fmt.Fprintf(s.log, "api: %s: %v\n", msg, err)
	}
	writeErr(w, r, status, code, msg, nil)
}

// --- pagination ---

const (
	defaultPageLimit = 100
	maxPageLimit     = 200
)

// pageInfo tells a client where it is and whether more is behind it.
type pageInfo struct {
	NextCursor string `json:"nextCursor"`
	HasMore    bool   `json:"hasMore"`
	Limit      int    `json:"limit"`
}

// readPage reads ?limit and ?cursor. A limit outside the allowed range is a
// client error; a cursor that is not ours is ignored rather than fatal, so an
// old client that sends nothing still gets the first page.
func readPage(w http.ResponseWriter, r *http.Request) (int, string, bool) {
	limit := defaultPageLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxPageLimit {
			writeErr(w, r, http.StatusBadRequest, "invalid_limit",
				fmt.Sprintf("limit must be 1-%d", maxPageLimit), nil)
			return 0, "", false
		}
		limit = n
	}
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		return 0, "", false
	}
	return limit, cursor, true
}

func encodeCursor(id string) string {
	if id == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

func decodeCursor(c string) (string, error) {
	if c == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// paginate cuts one page out of an already-sorted list and describes what is
// behind it. The lists here are one host's desktops, volumes and snapshots —
// small enough that sorting and slicing in memory beats teaching the catalog a
// cursor query, and it keeps every list handler honest about the same
// semantics.
func paginate[T any](items []T, limit int, cursor string, idOf func(T) string) ([]T, pageInfo) {
	start := 0
	if cursor != "" {
		for i, it := range items {
			if idOf(it) == cursor {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	more := end < len(items)
	if !more {
		end = len(items)
	}
	if start > len(items) {
		start = len(items)
	}
	page := items[start:end]
	info := pageInfo{Limit: limit, HasMore: more}
	if more && len(page) > 0 {
		info.NextCursor = encodeCursor(idOf(page[len(page)-1]))
	}
	return page, info
}
