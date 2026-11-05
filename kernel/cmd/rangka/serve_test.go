package main

import "testing"

func TestAICallbackURL(t *testing.T) {
	for _, c := range []struct{ flag, addr, want string }{
		{"", ":8080", "http://127.0.0.1:8080"},
		{"", "0.0.0.0:9000", "http://127.0.0.1:9000"},
		{"", "10.0.0.5:8080", "http://10.0.0.5:8080"},
		{"http://kernel:8080", ":8080", "http://kernel:8080"},
	} {
		if got := aiCallbackURL(c.flag, c.addr); got != c.want {
			t.Errorf("aiCallbackURL(%q, %q) = %q, want %q", c.flag, c.addr, got, c.want)
		}
	}
}
