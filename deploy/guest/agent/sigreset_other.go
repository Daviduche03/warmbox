//go:build !linux

package main

// The agent only ever runs on Linux (it is baked into the guest image).
// Anywhere else there is nothing to reset: keep building, do nothing.
func resetsignals() error { return nil }
