package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// pingTimeout bounds the TCP-level healthcheck — short on purpose:
// this runs every 5s per profile (see backendProfilePingScript/the
// htmx "every 5s" trigger in settings.templ), so a slow/hanging dial
// shouldn't be allowed to pile up concurrent probes against a dead
// backend.
const pingTimeout = 3 * time.Second

// testTimeout is more generous — the Test button is a one-off click,
// not a recurring probe, so it can afford to wait for a cold-starting
// local Ollama/llama.cpp server to answer.
const testTimeout = 10 * time.Second

// pingBackend is the healthcheck: a bare TCP dial to baseURL's own
// host:port (falling back to 80/443 by scheme when the URL doesn't
// name one explicitly) — "is anything listening there at all",
// deliberately not an HTTP request. That keeps it cheap enough to
// run every 5s and meaningful even for a backend whose HTTP layer
// is misconfigured (wrong path, wrong auth): the socket either
// answers or it doesn't.
func pingBackend(baseURL string) error {
	host, port, err := hostPort(baseURL)
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), pingTimeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

func hostPort(baseURL string) (host, port string, err error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid base URL: %w", err)
	}
	if u.Hostname() == "" {
		return "", "", fmt.Errorf("base URL has no host")
	}
	host = u.Hostname()
	port = u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return host, port, nil
}

// testBackendConnection is the "Test" button: an actual HTTP request,
// carrying the API key when one's set — a real exercise of the whole
// path (DNS, TCP, TLS, auth), not just the socket-level reachability
// pingBackend checks every 5s. Returns the listed model IDs (nil if
// the response's shape couldn't be parsed — still a success, since
// plenty of backends answer 200 without a standard body) so the
// caller can offer them as picks (see TestResult/modelFieldSelect)
// instead of free text.
//
// The probed path and response shape depend on profile.Kind:
//   - "llm" (default): the OpenAI-compatible /models endpoint most
//     backends (an OpenAI-style proxy, Ollama, llama.cpp's server,
//     LM Studio, ...) expose, body shaped {"data": [{"id": ...}]}.
//   - "image": A1111/Forge's /sdapi/v1/sd-models — A1111 never had a
//     bare /models route, so reusing the llm path always 404s here.
//     Body is a bare array of {"title": ...}; title (not model_name)
//     is what BackendProfile.Model/override_settings.sd_model_checkpoint
//     expects, so that's what gets returned.
func testBackendConnection(profile db.BackendProfile) (models []string, err error) {
	base := strings.TrimRight(profile.BaseURL, "/")
	path := "/models"
	if profile.Kind == "image" {
		path = "/sdapi/v1/sd-models"
	}
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	if profile.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+profile.APIKey)
	}
	client := &http.Client{Timeout: testTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if profile.Kind == "image" {
		var items []struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
			return nil, nil
		}
		for _, it := range items {
			if it.Title != "" {
				models = append(models, it.Title)
			}
		}
		return models, nil
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, nil
	}
	for _, d := range payload.Data {
		if d.ID != "" {
			models = append(models, d.ID)
		}
	}
	return models, nil
}
