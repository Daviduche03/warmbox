package api

import (
	"testing"
	"time"
)

// Guest-bound proxies must share one Transport with idle reaping: a
// Transport per request leaked every keep-alive connection (zero
// IdleConnTimeout never reaps), piling thousands of ESTABLISHED sockets
// onto daemon and agent within hours.
func TestGuestTransportReapsIdle(t *testing.T) {
	if guestTransport == nil {
		t.Fatal("guestTransport is nil")
	}
	if guestTransport.IdleConnTimeout <= 0 {
		t.Fatalf("IdleConnTimeout = %v, idle connections would never be reaped", guestTransport.IdleConnTimeout)
	}
	if guestTransport.IdleConnTimeout > 5*time.Minute {
		t.Fatalf("IdleConnTimeout = %v, too lax to bound the pool", guestTransport.IdleConnTimeout)
	}
	if guestTransport.DialContext == nil {
		t.Fatal("guestTransport needs the vnc dialer for guest addresses")
	}
}
