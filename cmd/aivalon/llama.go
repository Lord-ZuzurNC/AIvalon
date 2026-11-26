package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
	"github.com/Lord-ZuzurNC/AIvalon/internal/web"
)

// Optional local llama.cpp sidecar — see docs/DESIGN.md ("Local llama.cpp
// sidecar"). Both unset = no supervisor at all, AIvalon behaves exactly as
// before (user runs their own backend).
var (
	llamaBin       = envOr("AIVALON_LLAMA_BIN", "")
	llamaModelsDir = envOr("AIVALON_LLAMA_MODELS_DIR", "")
)

const (
	// llamaProfileName is the row seeded by migration 00018.
	llamaProfileName = "llama.cpp (local)"
	// llamaDefaultBaseURL mirrors migration 00018's base_url; only used
	// when that row has been deleted/renamed.
	llamaDefaultBaseURL = "http://127.0.0.1:9931/v1"
	llamaStartTimeout   = 60 * time.Second
	llamaPollInterval   = 400 * time.Millisecond
	llamaStopGrace      = 5 * time.Second
)

const (
	sidecarStarting int32 = iota
	sidecarReady
	sidecarFailed
)

// sidecar is a llama-server child this process spawned and therefore owns.
// A nil *sidecar (disabled, or something healthy was already serving) is
// valid: gate passes through and stop is a no-op.
type sidecar struct {
	cmd     *exec.Cmd
	health  string
	done    chan struct{} // closed once cmd.Wait returns
	state   atomic.Int32
	failMsg string // written before state is set to sidecarFailed
}

// llamaHealthURL turns an OpenAI-style base URL (http://host:port/v1) into
// llama-server's health endpoint (http://host:port/health) and returns the
// port to pass as --port.
func llamaHealthURL(baseURL string) (health, port string, err error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", "", fmt.Errorf("parsing base_url %q: %w", baseURL, err)
	}
	if u.Port() == "" {
		return "", "", fmt.Errorf("base_url %q has no explicit port", baseURL)
	}
	u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/v1") + "/health"
	u.RawQuery, u.Fragment = "", ""
	return u.String(), u.Port(), nil
}

// llamaHealthy reports whether GET health answers 200 {"status":"ok"}.
func llamaHealthy(health string) bool {
	client := http.Client{Timeout: time.Second}
	resp, err := client.Get(health)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var body struct{ Status string }
	return resp.StatusCode == http.StatusOK &&
		json.NewDecoder(resp.Body).Decode(&body) == nil && body.Status == "ok"
}

// startLlama runs the startup checks and, if needed, spawns llama-server.
// Every misconfiguration is fatal (log.Fatalf → stderr, exit 1): silently
// carrying on would leave the user staring at a backend that never comes.
func startLlama(sqlDB *sql.DB) *sidecar {
	if llamaBin == "" && llamaModelsDir == "" {
		return nil
	}
	if llamaBin == "" || llamaModelsDir == "" {
		log.Fatalf("FATAL llama sidecar: AIVALON_LLAMA_BIN and AIVALON_LLAMA_MODELS_DIR must be set together (got bin=%q models_dir=%q)", llamaBin, llamaModelsDir)
	}

	bin, err := exec.LookPath(llamaBin)
	if err != nil {
		log.Fatalf("FATAL llama sidecar: AIVALON_LLAMA_BIN=%q is not an executable file: %v", llamaBin, err)
	}
	if st, err := os.Stat(llamaModelsDir); err != nil || !st.IsDir() {
		log.Fatalf("FATAL llama sidecar: AIVALON_LLAMA_MODELS_DIR=%q is not a directory", llamaModelsDir)
	}

	baseURL := llamaDefaultBaseURL
	found := false
	if profiles, err := db.ListBackendProfiles(sqlDB); err == nil {
		for _, p := range profiles {
			if p.Name == llamaProfileName {
				baseURL, found = p.BaseURL, true
				break
			}
		}
	}
	if !found {
		log.Printf("WARNING llama sidecar: no %q backend profile found, falling back to %s", llamaProfileName, llamaDefaultBaseURL)
	}
	health, port, err := llamaHealthURL(baseURL)
	if err != nil {
		log.Fatalf("FATAL llama sidecar: %v", err)
	}

	if llamaHealthy(health) {
		log.Printf("llama sidecar: %s already healthy, not spawning llama-server", health)
		return nil
	}
	u, _ := url.Parse(health) // llamaHealthURL just built it
	hostPort := u.Host
	if conn, err := net.DialTimeout("tcp", hostPort, 500*time.Millisecond); err == nil {
		conn.Close()
		log.Fatalf("FATAL llama sidecar: %s is already in use by something that doesn't answer %s with 200 {\"status\":\"ok\"} — stop whatever owns that port (or a llama-server still loading), or change the %q profile's base_url", hostPort, health, llamaProfileName)
	}

	logPath := filepath.Join(filepath.Dir(dbPath), "llama-server.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("FATAL llama sidecar: opening %s: %v", logPath, err)
	}
	defer logFile.Close() // the child holds its own copy of the fd

	cmd := exec.Command(bin, "-cmoe", "-rea", "off", "--sleep-idle-seconds", "15", "-c", "8192",
		"--models-dir", llamaModelsDir, "--port", port)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = sidecarSysProcAttr()
	if err := cmd.Start(); err != nil {
		log.Fatalf("FATAL llama sidecar: starting %s: %v", bin, err)
	}
	log.Printf("llama sidecar: started %s (pid %d), logging to %s", bin, cmd.Process.Pid, logPath)

	s := &sidecar{cmd: cmd, health: health, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		log.Printf("llama sidecar: llama-server exited: %v", err)
		close(s.done)
	}()
	go s.poll()
	return s
}

// poll flips state to ready once /health answers ok, or to failed on
// timeout / early child exit. Never loops forever.
func (s *sidecar) poll() {
	deadline := time.After(llamaStartTimeout)
	tick := time.NewTicker(llamaPollInterval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if llamaHealthy(s.health) {
				log.Printf("llama sidecar: %s healthy, serving normally", s.health)
				s.state.Store(sidecarReady)
				return
			}
		case <-s.done:
			s.fail("llama-server exited before becoming healthy — see llama-server.log next to the database")
			return
		case <-deadline:
			s.fail(fmt.Sprintf("llama-server did not become healthy at %s within %s — see llama-server.log next to the database", s.health, llamaStartTimeout))
			return
		}
	}
}

func (s *sidecar) fail(msg string) {
	log.Printf("ERROR llama sidecar: %s", msg)
	s.failMsg = msg
	s.state.Store(sidecarFailed)
}

// gate serves the loading screen (or the failure message) instead of the
// real routes until the sidecar is healthy. /static/ always passes so the
// loading screen's own CSS loads.
func (s *sidecar) gate(next http.Handler) http.Handler {
	if s == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := s.state.Load()
		if state == sidecarReady || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if state == sidecarFailed {
			http.Error(w, "AIvalon: local llama.cpp backend failed to start: "+s.failMsg, http.StatusServiceUnavailable)
			return
		}
		// Refresh header: browsers reload on their own, so the page
		// turns into the real app once the sidecar is up.
		w.Header().Set("Refresh", "1")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		web.LoadingScreen().Render(r.Context(), w)
	})
}

// stop terminates the whole child process group: SIGTERM, a grace period,
// then SIGKILL regardless (catches grandchildren that outlived the leader).
func (s *sidecar) stop() {
	if s == nil {
		return
	}
	pid := s.cmd.Process.Pid
	log.Printf("llama sidecar: stopping process group %d", pid)
	terminateGroup(s.cmd.Process)
	select {
	case <-s.done:
	case <-time.After(llamaStopGrace):
		log.Printf("llama sidecar: still alive after %s, killing", llamaStopGrace)
	}
	killGroup(s.cmd.Process)
	<-s.done
}
