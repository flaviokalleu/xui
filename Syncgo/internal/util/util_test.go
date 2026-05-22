package util

import "testing"

func TestIsPublicURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"http://localhost:8080/x", false},
		{"http://127.0.0.1/x", false},
		{"http://10.0.0.1/x", false},
		{"http://192.168.1.1/x", false},
		{"http://172.16.0.1/x", false},
		{"http://172.20.0.1/x", false}, // string-prefix bug from old impl
		{"http://172.31.255.255/x", false},
		{"http://172.32.0.1/x", true}, // not RFC1918
		{"http://example.com/x", true},
		{"https://example.com:8443/x", true},
		{"http://[::1]/x", false}, // IPv6 loopback
		{"http://0.0.0.0/x", false},
		{"not-a-url", false},
	}
	for _, c := range cases {
		if got := IsPublicURL(c.url); got != c.want {
			t.Errorf("IsPublicURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestSecureHash(t *testing.T) {
	a := SecureHash(12, "secret-1")
	b := SecureHash(12, "secret-1")
	c := SecureHash(13, "secret-1")
	d := SecureHash(12, "secret-2")
	if a != b {
		t.Errorf("same input must produce same hash, got %q != %q", a, b)
	}
	if a == c {
		t.Errorf("different ID must produce different hash, both %q", a)
	}
	if a == d {
		t.Errorf("different secret must produce different hash, both %q", a)
	}
	if len(a) != 10 {
		t.Errorf("hash length = %d, want 10", len(a))
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.00 KB"},
		{1024 * 1024, "1.00 MB"},
		{1024 * 1024 * 1024, "1.00 GB"},
	}
	for _, c := range cases {
		if got := HumanBytes(c.n); got != c.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
