// Command aivalon runs the AIvalon server: a local, single-user web app for
// AI roleplay. See docs/DESIGN.md for the full design.
package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
	"github.com/Lord-ZuzurNC/AIvalon/internal/web"
)

// dbPath is a file next to the binary's working directory by default,
// already covered by .gitignore. AIVALON_DB overrides it — mainly so two
// instances (e.g. yours and a throwaway test one) don't fight over the
// same file.
var dbPath = envOr("AIVALON_DB", "aivalon.db")

// addr is 127.0.0.1-only by design — see docs/DESIGN.md ("Default bind").
// AIVALON_ADDR overrides it for the same reason as dbPath above.
var addr = envOr("AIVALON_ADDR", "127.0.0.1:4140")

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer sqlDB.Close()

	srv := &http.Server{
		Addr:    addr,
		Handler: newHandler(sqlDB),
		// Slowloris-style attacks trickle headers in to hold a
		// connection open indefinitely; this caps how long that's
		// tolerated. WriteTimeout is deliberately left at its zero
		// value (no limit) — the chat Stream handler is a long-lived
		// SSE response that can legitimately take minutes.
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("AIvalon listening on http://%s", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func newMux(sqlDB *sql.DB) *http.ServeMux {
	mux := http.NewServeMux()
	vaultGate := web.NewVaultGate()
	importCache := web.NewImportCache()

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(noListFileSystem{http.Dir("web/static")})))

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/chat", http.StatusFound)
	})

	world := web.WorldHandlers{DB: sqlDB, Cache: importCache}
	mux.HandleFunc("GET /world", world.List)
	mux.HandleFunc("POST /world", world.Create)
	mux.HandleFunc("POST /world/import", world.ImportLorebookJSON)
	mux.HandleFunc("POST /world/import/resume", world.ResumeLorebookImport)
	mux.HandleFunc("GET /world/{id}", world.Show)
	mux.HandleFunc("POST /world/{id}", world.Update)
	mux.HandleFunc("POST /world/{id}/delete", world.Delete)
	mux.HandleFunc("POST /world/{id}/entries", world.EntryCreate)
	mux.HandleFunc("GET /world/{id}/entries/{entryID}/edit", world.EntryEditForm)
	mux.HandleFunc("POST /world/{id}/entries/{entryID}", world.EntryUpdate)
	mux.HandleFunc("POST /world/{id}/entries/{entryID}/delete", world.EntryDelete)
	mux.HandleFunc("POST /world/{id}/entries/{entryID}/toggle", world.EntryToggleEnabled)

	settings := web.SettingsHandlers{DB: sqlDB}
	mux.HandleFunc("GET /settings", settings.List)
	mux.HandleFunc("POST /settings/backend-profiles", settings.Create)
	mux.HandleFunc("GET /settings/backend-profiles/{id}/edit", settings.EditForm)
	mux.HandleFunc("POST /settings/backend-profiles/{id}", settings.Update)
	mux.HandleFunc("POST /settings/backend-profiles/{id}/delete", settings.Delete)
	mux.HandleFunc("GET /settings/backend-profiles/{id}/ping", settings.ProfilePing)
	mux.HandleFunc("POST /settings/backend-profiles/test", settings.ProfileTest)
	mux.HandleFunc("POST /settings/personas", settings.PersonaCreate)
	mux.HandleFunc("GET /settings/personas/{id}/edit", settings.PersonaEditForm)
	mux.HandleFunc("POST /settings/personas/{id}", settings.PersonaUpdate)
	mux.HandleFunc("POST /settings/personas/{id}/delete", settings.PersonaDelete)
	mux.HandleFunc("POST /settings/system-prompt", settings.SystemPromptUpdate)
	mux.HandleFunc("POST /settings/system-prompt/reset", settings.SystemPromptReset)
	mux.HandleFunc("POST /settings/context-budget", settings.ContextBudgetUpdate)
	mux.HandleFunc("POST /settings/import-whitelist", settings.ImportWhitelistUpdate)

	character := web.CharacterHandlers{DB: sqlDB, Cache: importCache}
	mux.HandleFunc("GET /characters", character.List)
	mux.HandleFunc("POST /characters", character.Create)
	mux.HandleFunc("POST /characters/import", character.ImportCharacterJSON)
	mux.HandleFunc("POST /characters/import/resume", character.ResumeCharacterImport)
	mux.HandleFunc("GET /characters/{id}/edit", character.EditForm)
	mux.HandleFunc("POST /characters/{id}", character.Update)
	mux.HandleFunc("POST /characters/{id}/delete", character.Delete)
	mux.HandleFunc("POST /characters/{id}/images/{imageID}/primary", character.PromoteImage)
	mux.HandleFunc("POST /characters/{id}/images/{imageID}/delete", character.DeleteImage)

	chat := web.ChatHandlers{DB: sqlDB, Gate: vaultGate}
	mux.HandleFunc("GET /chat", chat.List)
	mux.HandleFunc("POST /chat", chat.Create)
	mux.HandleFunc("GET /chat/{id}", chat.Show)
	mux.HandleFunc("POST /chat/{id}/delete", chat.Delete)
	mux.HandleFunc("POST /chat/{id}/duplicate", chat.Duplicate)
	mux.HandleFunc("POST /chat/{id}/messages", chat.PostMessage)
	mux.HandleFunc("POST /chat/{id}/messages/{messageID}/regenerate", chat.RegenerateMessage)
	mux.HandleFunc("POST /chat/{id}/messages/{messageID}/branch", chat.BranchMessage)
	mux.HandleFunc("POST /chat/{id}/messages/{messageID}/edit", chat.EditMessage)
	mux.HandleFunc("POST /chat/{id}/messages/{messageID}/delete", chat.DeleteMessage)
	mux.HandleFunc("POST /chat/{id}/background", chat.SetBackground)
	mux.HandleFunc("GET /chat/{id}/stream", chat.Stream)

	vault := web.VaultHandlers{DB: sqlDB, Gate: vaultGate}
	mux.HandleFunc("GET /vault", vault.Show)
	mux.HandleFunc("POST /vault/setup", vault.Setup)
	mux.HandleFunc("POST /vault/unlock", vault.Unlock)
	mux.HandleFunc("POST /vault/lock", vault.Lock)
	mux.HandleFunc("POST /vault/{id}/in", vault.MoveIn)
	mux.HandleFunc("POST /vault/{id}/out", vault.MoveOut)

	return mux
}

// newHandler wraps newMux's routes with the app's two layers against a
// hostile page in the same browser talking to this app behind its back:
//   - requireLocalHost rejects DNS-rebinding (a domain that resolves
//     to 127.0.0.1 but is sent as the Host header) and bogus Host
//     headers generally — checked first since CrossOriginProtection
//     has nothing to say about Host itself.
//   - http.NewCrossOriginProtection rejects cross-origin POSTs (CSRF)
//     via Sec-Fetch-Site/Origin. It explicitly treats GET/HEAD/OPTIONS
//     as "safe" and always allows them — chat.Stream (a GET that
//     triggers a paid LLM call and a DB write) is deliberately NOT
//     "safe" by that definition, so it carries its own same-site
//     check; see ChatHandlers.Stream.
func newHandler(sqlDB *sql.DB) http.Handler {
	mux := newMux(sqlDB)
	return web.WithSecurityHeaders(requireLocalHost(http.NewCrossOriginProtection().Handler(mux)))
}

// requireLocalHost rejects any request whose Host header isn't this
// server's own bind address (in either 127.0.0.1:PORT or localhost:PORT
// form). Without it, a DNS entry that resolves to 127.0.0.1 — attacker-
// controlled, since DNS is public — lets a browser that visited that
// domain send real requests here with a Host header matching whatever
// the attacker chose, bypassing same-origin assumptions baked into the
// rest of the app's threat model (a purely local, single-user tool).
func requireLocalHost(next http.Handler) http.Handler {
	_, port, _ := strings.Cut(addr, ":")
	allowed := map[string]bool{addr: true, "localhost:" + port: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// noListFileSystem wraps http.Dir to refuse directory listings:
// http.FileServer serves an auto-generated index for any directory
// that has no index.html, which — combined with predictable upload
// filenames — would let a browser enumerate every image anyone's ever
// uploaded instead of only loading the ones a page actually links to.
type noListFileSystem struct {
	fs http.FileSystem
}

func (nfs noListFileSystem) Open(name string) (http.File, error) {
	f, err := nfs.fs.Open(name)
	if err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if stat.IsDir() {
		f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}
