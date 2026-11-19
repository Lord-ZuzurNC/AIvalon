package web

import (
	"net"
	"testing"
)

func TestHostPort(t *testing.T) {
	cases := []struct {
		url      string
		wantHost string
		wantPort string
	}{
		{"http://127.0.0.1:11434/v1", "127.0.0.1", "11434"},
		{"http://127.0.0.1/v1", "127.0.0.1", "80"},
		{"https://api.example.com/v1", "api.example.com", "443"},
	}
	for _, c := range cases {
		host, port, err := hostPort(c.url)
		if err != nil {
			t.Fatalf("hostPort(%q): %v", c.url, err)
		}
		if host != c.wantHost || port != c.wantPort {
			t.Fatalf("hostPort(%q) = %q, %q; want %q, %q", c.url, host, port, c.wantHost, c.wantPort)
		}
	}
}

func TestHostPort_Invalid(t *testing.T) {
	if _, _, err := hostPort("not a url, no host at all"); err == nil {
		t.Fatal("expected a hostless string to error")
	}
}

func TestPingBackend(t *testing.T) {
	// A real listener: pingBackend must succeed against it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	if err := pingBackend("http://" + ln.Addr().String() + "/v1"); err != nil {
		t.Fatalf("expected ping to succeed against a real listener: %v", err)
	}

	// An address nothing listens on: pingBackend must fail.
	deadLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadAddr := deadLn.Addr().String()
	deadLn.Close() // free the port, nothing listens on it anymore

	if err := pingBackend("http://" + deadAddr + "/v1"); err == nil {
		t.Fatal("expected ping to fail against a closed port")
	}
}
