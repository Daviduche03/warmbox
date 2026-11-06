package api

import "testing"

// A recording request is user input, and it decides both how long the guest
// encodes for and how long the daemon waits. An out-of-range value must not
// become "record until the browser goes away" or a zero-length clip.
func TestRecordSecondsIsBounded(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 10},
		{"nonsense", 10},
		{"0", 10},
		{"-5", 10},
		{"1", 1},
		{"10", 10},
		{"300", 300},
		{"301", 300},
		{"999999", 300},
	}
	for _, c := range cases {
		if got := recordSeconds(c.in); got != c.want {
			t.Errorf("recordSeconds(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
