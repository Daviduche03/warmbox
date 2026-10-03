package main

import (
	"testing"

	"warmbox/internal/desktop"
)

// Whether an address is reachable from the network decides whether the daemon
// refuses to start, so it has to be right for the shapes people actually type —
// including the accidental one, a bare ":7070", which is every interface.
func TestBindIsLoopback(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:7070", true},
		{"localhost:7070", true},
		{"[::1]:7070", true},
		{"127.0.0.2:7070", true}, // the whole 127/8 is loopback
		{":7070", false},         // every interface, and easy to type by mistake
		{"0.0.0.0:7070", false},
		{"[::]:7070", false},
		{"192.168.1.10:7070", false},
		{"10.0.0.5:7070", false},
		{"", false},
		{"not-an-address", false},
	}
	for _, c := range cases {
		if got := bindIsLoopback(c.addr); got != c.want {
			t.Errorf("bindIsLoopback(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

// A service has to remember the sizes it was installed with. It used to accept
// --mem and --cpus and silently drop them, leaving the daemon on the built-in
// 4 GiB and 4 vCPUs — on a small host, more memory than the machine has.
func TestServiceFlagsCarrySizing(t *testing.T) {
	cfg := desktop.DefaultConfig()
	cfg.MemMiB = 768
	cfg.CPUs = 1
	args := appendServiceFlags([]string{"daemon"}, cfg)
	if !hasPair(args, "--mem", "768") {
		t.Errorf("no --mem 768 in %v", args)
	}
	if !hasPair(args, "--cpus", "1") {
		t.Errorf("no --cpus 1 in %v", args)
	}
}

// …but it must not invent flags nobody asked for: a unit that pins every value
// freezes today's defaults for the people who never chose them.
func TestServiceFlagsLeaveDefaultsAlone(t *testing.T) {
	cfg := desktop.DefaultConfig()
	args := appendServiceFlags([]string{"daemon"}, cfg)
	for _, flag := range []string{"--mem", "--cpus", "--gpu", "--input", "--accel", "--tls-cert", "--insecure"} {
		for _, a := range args {
			if a == flag {
				t.Errorf("%s was written for an unconfigured service: %v", flag, args)
			}
		}
	}
}

// A service bound to the network needs its certificate remembered, or the unit
// would start the daemon without one and it would refuse to run.
func TestServiceFlagsCarryTLS(t *testing.T) {
	cfg := desktop.DefaultConfig()
	cfg.TLSCert = "/etc/warmbox/cert.pem"
	cfg.TLSKey = "/etc/warmbox/key.pem"
	args := appendServiceFlags([]string{"daemon"}, cfg)
	if !hasPair(args, "--tls-cert", cfg.TLSCert) {
		t.Errorf("no --tls-cert in %v", args)
	}
	if !hasPair(args, "--tls-key", cfg.TLSKey) {
		t.Errorf("no --tls-key in %v", args)
	}
}

func hasPair(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}
