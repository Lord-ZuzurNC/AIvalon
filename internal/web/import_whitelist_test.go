package web

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// withImportServer serves body for every request and routes
// importHTTPClient's connections — whatever hostname the URL names — to
// it, so a "remote" whitelisted host can be exercised without the
// network. Restores the real client afterwards.
func withImportServer(t *testing.T, body []byte) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	orig := importHTTPClient
	c := *orig
	c.Transport = &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}}
	importHTTPClient = &c
	t.Cleanup(func() { importHTTPClient = orig })
}

var testPNG = append(append([]byte{}, pngSignature...), []byte("rest-of-png")...)

func TestFetchImportURL_LoopbackAllowsAnything(t *testing.T) {
	withImportServer(t, []byte(`{"data":{"name":"Merlin"}}`))
	for _, u := range []string{"http://127.0.0.1:1/card.json", "http://localhost:1/card.json", "http://[::1]:1/card.json"} {
		raw, _, err := fetchImportURL(u, nil)
		if err != nil || !strings.Contains(string(raw), "Merlin") {
			t.Fatalf("%s: expected JSON from loopback to pass, got %q, %v", u, raw, err)
		}
	}
}

func TestFetchImportURL_WhitelistedHostPNGOnly(t *testing.T) {
	whitelist := []string{"www.characterhub.org"}

	withImportServer(t, testPNG)
	if _, _, err := fetchImportURL("http://WWW.CharacterHub.org:8443/card.png", whitelist); err != nil {
		t.Fatalf("whitelisted host (case/port ignored) + PNG should pass, got %v", err)
	}

	withImportServer(t, []byte(`{"data":{"name":"Merlin"}}`))
	_, _, err := fetchImportURL("http://www.characterhub.org/card.json", whitelist)
	if err == nil || !strings.Contains(err.Error(), "PNG imports only") {
		t.Fatalf("whitelisted host + JSON must be rejected, got %v", err)
	}
}

func TestFetchImportURL_UnlistedHostRejectedBeforeConnecting(t *testing.T) {
	// Nothing listens here and the real client is in use: if the gate
	// let it through, the error would be a dial/timeout failure instead.
	_, _, err := fetchImportURL("http://10.255.255.1:9/card.png", []string{"taverncard.com"})
	if err == nil {
		t.Fatal("expected an unlisted host to be rejected")
	}
	if !strings.Contains(err.Error(), "aren't allowed") || !strings.Contains(err.Error(), "Settings") {
		t.Fatalf("expected the whitelist error, got %v", err)
	}
	if strings.Contains(err.Error(), "fetching URL") || strings.Contains(err.Error(), "dial") {
		t.Fatalf("error suggests a connection was attempted: %v", err)
	}
	// Suffix tricks don't match: exact hostnames only.
	if _, err := checkImportHost(mustParseURL(t, "https://evil-taverncard.com/x.png"), []string{"taverncard.com"}); err == nil {
		t.Fatal("expected a non-exact host match to be rejected")
	}
}

func TestFetchImportURL_RedirectToUnlistedHostBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://192.168.1.1/admin", http.StatusFound)
	}))
	defer srv.Close()
	_, _, err := fetchImportURL(srv.URL+"/card.png", nil)
	if err == nil || !strings.Contains(err.Error(), "aren't allowed") {
		t.Fatalf("expected the redirect hop to be host-gated, got %v", err)
	}
}

func mustParseURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
