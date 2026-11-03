package connlimit

import "testing"

// TestKeyGroupsIPv6By64: every address of one IPv6 /64 is one key (a host
// rotating through its /64 used to get fresh per-IP limits each time);
// IPv4 and IPv4-mapped addresses stay themselves.
func TestKeyGroupsIPv6By64(t *testing.T) {
	for in, want := range map[string]string{
		"1.2.3.4":                  "1.2.3.4",
		"::ffff:1.2.3.4":           "1.2.3.4",
		"2001:db8:1:2::1":          "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff:ffff::": "2001:db8:1:2::/64",
		" 2001:db8:1:3::9 ":        "2001:db8:1:3::/64",
		"2001:db8:1:2::/64":        "2001:db8:1:2::/64", // already a key
		"pipe":                     "pipe",
	} {
		if got := KeyString(in); got != want {
			t.Errorf("KeyString(%q) = %q, want %q", in, got, want)
		}
	}

	withConnLimits(t, 0, 2, 0)
	l := New()
	fakeClock(l)
	for _, a := range []string{"2001:db8::1", "2001:db8::2"} {
		if r, _ := l.Acquire(KeyString(a)); r == nil {
			t.Fatalf("%s should be admitted", a)
		}
	}
	if r, _ := l.Acquire(KeyString("2001:db8::3")); r != nil {
		t.Error("a third address of the same /64 must hit the per-IP limit")
	}
	if r, _ := l.Acquire(KeyString("2001:db8:0:1::1")); r == nil {
		t.Error("another /64 is another client")
	}
}
