package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLlamaHealthURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://127.0.0.1:9931/v1":  "http://127.0.0.1:9931/health",
		"http://127.0.0.1:9931/v1/": "http://127.0.0.1:9931/health",
		"http://localhost:8080":     "http://localhost:8080/health",
	} {
		got, _, err := llamaHealthURL(in)
		if err != nil || got != want {
			t.Errorf("llamaHealthURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, port, _ := llamaHealthURL(llamaDefaultBaseURL); port != "9931" {
		t.Errorf("default port = %q, want 9931", port)
	}
	if _, _, err := llamaHealthURL("http://127.0.0.1/v1"); err == nil {
		t.Error("expected error for base_url without port")
	}
}

func TestSidecarGate(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	s := &sidecar{}
	h := s.gate(ok)
	status := func(path string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec.Code
	}

	if got := status("/chat"); got != http.StatusServiceUnavailable {
		t.Errorf("starting: /chat = %d, want 503 loading screen", got)
	}
	if got := status("/static/aivalon.css"); got != http.StatusTeapot {
		t.Errorf("starting: /static/ = %d, want passthrough", got)
	}
	s.fail("boom")
	if got := status("/chat"); got != http.StatusServiceUnavailable {
		t.Errorf("failed: /chat = %d, want 503", got)
	}
	s.state.Store(sidecarReady)
	if got := status("/chat"); got != http.StatusTeapot {
		t.Errorf("ready: /chat = %d, want passthrough", got)
	}
	if (*sidecar)(nil).gate(ok) == nil {
		t.Error("nil sidecar gate must pass through")
	}
}

// Fake health endpoint: llamaHealthy must require 200 AND status "ok".
func TestLlamaHealthy(t *testing.T) {
	body, code := `{"status":"ok"}`, 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		w.Write([]byte(body))
	}))
	defer srv.Close()
	if !llamaHealthy(srv.URL) {
		t.Error("want healthy")
	}
	body, code = `{"error":{"message":"Loading model"}}`, 503
	if llamaHealthy(srv.URL) {
		t.Error("503 loading must not count as healthy")
	}
}
