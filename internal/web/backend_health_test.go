package web

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
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

// TestTestBackendConnection_ImageKind locks in the A1111/Forge-specific
// path: no bare /models route exists on that API, so an "image" profile
// must probe /sdapi/v1/sd-models and read "title" (not "model_name") —
// title is the exact string override_settings.sd_model_checkpoint wants.
func TestTestBackendConnection_ImageKind(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode([]map[string]string{
			{"title": "Juggernaut-XL_v9_RunDiffusionPhoto_v2.safetensors", "model_name": "Juggernaut-XL_v9_RunDiffusionPhoto_v2"},
		})
	}))
	defer srv.Close()

	models, err := testBackendConnection(db.BackendProfile{Kind: "image", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("testBackendConnection: %v", err)
	}
	if gotPath != "/sdapi/v1/sd-models" {
		t.Fatalf("probed path = %q, want /sdapi/v1/sd-models", gotPath)
	}
	if len(models) != 1 || models[0] != "Juggernaut-XL_v9_RunDiffusionPhoto_v2.safetensors" {
		t.Fatalf("models = %v, want the one title", models)
	}
}

// TestTestBackendConnection_LLMKind confirms the pre-existing /models
// behaviour is untouched by the image-kind branch above.
func TestTestBackendConnection_LLMKind(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "llama3"}},
		})
	}))
	defer srv.Close()

	models, err := testBackendConnection(db.BackendProfile{Kind: "llm", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("testBackendConnection: %v", err)
	}
	if gotPath != "/models" {
		t.Fatalf("probed path = %q, want /models", gotPath)
	}
	if len(models) != 1 || models[0] != "llama3" {
		t.Fatalf("models = %v, want [llama3]", models)
	}
}
