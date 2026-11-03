package server

import "testing"

func TestLoggedCommandHidesPasswords(t *testing.T) {
	for raw, want := range map[string]string{
		"login hunter2":     "login ***",
		"register pw pw":    "register ***",
		"LOGIN pw":          "LOGIN ***",
		"login":             "login",
		"tp Steve":          "tp Steve",
		"banip 1.2.3.4":     "banip 1.2.3.4",
		"loginfo something": "loginfo something",
	} {
		if got := loggedCommand(raw); got != want {
			t.Errorf("loggedCommand(%q) = %q, want %q", raw, got, want)
		}
	}
}
