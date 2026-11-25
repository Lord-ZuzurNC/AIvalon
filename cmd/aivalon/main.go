// Command aivalon runs the AIvalon server: a local, single-user web app for
// AI roleplay. See docs/DESIGN.md for the full design.
package main

import (
	"database/sql"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
	"github.com/Lord-ZuzurNC/AIvalon/internal/i18n"
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

// langDir is where internal/i18n.LoadDir scans for language files —
// same "relative to cwd, run from the repo root" convention as
// web/static itself (see noListFileSystem's own registration below).
// AIVALON_I18N_DIR overrides it, same reasoning as dbPath/addr.
var langDir = "web/static/i18n"

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// demoModeDefault is baked in at link time for the release/ builds via
// -ldflags "-X main.demoModeDefault=1" (see release build instructions
// in docs/ or the release script) — 0.1 ships with image generation
// off by default (Lord: "la gen d'image n'est pas viable là" — the
// VRAM contention with llama.cpp isn't solved yet). A plain `go build`
// (Lord's own dev loop, still actively debugging image gen) leaves
// this at "0": image gen stays on unless AIVALON_DEMO=1 is set, same
// as before. AIVALON_DEMO always overrides this default either way,
// so a release binary can still be forced back on for testing.
var demoModeDefault = "0"

func main() {
	// AIVALON_DEMO=1/0 overrides demoModeDefault either way; unset
	// falls back to whatever this binary was linked with. See
	// web.DemoMode's doc comment for why the UI stays visible, just
	// inert, instead of hiding the button outright.
	demo := demoModeDefault == "1"
	if v, ok := os.LookupEnv("AIVALON_DEMO"); ok {
		demo = v == "1"
	}
	web.DemoMode = demo

	// i18n: discover every web/static/i18n/<code>.json at startup —
	// see internal/i18n's own doc comment. A missing/unreadable
	// directory isn't fatal: LoadDir still returns a usable (English-
	// only, via its builtinFallback) registry, just logged so it's
	// not silently wrong.
	langDir = envOr("AIVALON_I18N_DIR", langDir)
	reg, err := i18n.LoadDir(langDir)
	if err != nil {
		log.Printf("i18n: %v (continuing with English only)", err)
	}
	i18n.SetDefault(reg)

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

	// no-cache (not no-store): the browser still keeps a local copy
	// and can reuse it, but only after revalidating with the server
	// first (If-Modified-Since/If-None-Match, which http.FileServer
	// already answers via Last-Modified/ETag — a 304 when unchanged,
	// so this doesn't add real network cost). Without this, mobile
	// Chrome/Safari's own heuristic freshness lifetime can decide a
	// CSS/JS file is "fresh enough" for a good while and skip
	// revalidation entirely — Lord's bug report: a CSS fix only
	// showed up in private browsing (no heuristic cache), not in his
	// normal tab, until now.
	mux.Handle("GET /static/", http.StripPrefix("/static/", noCacheHeaders(http.FileServer(noListFileSystem{http.Dir("web/static")}))))

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/chat", http.StatusFound)
	})

	world := web.WorldHandlers{DB: sqlDB, Cache: importCache, Gate: vaultGate}
	mux.HandleFunc("GET /world", world.List)
	mux.HandleFunc("POST /world", world.Create)
	mux.HandleFunc("POST /world/import", world.ImportLorebookJSON)
	mux.HandleFunc("POST /world/import/resume", world.ResumeLorebookImport)
	mux.HandleFunc("POST /world/import/confirm-language", world.ConfirmLanguageImport)
	mux.HandleFunc("POST /world/import/confirm-language-batch", world.ConfirmLanguageImportBatch)
	mux.HandleFunc("GET /world/{id}", world.Show)
	mux.HandleFunc("POST /world/{id}", world.Update)
	mux.HandleFunc("POST /world/{id}/delete", world.Delete)
	mux.HandleFunc("POST /world/{id}/entries", world.EntryCreate)
	mux.HandleFunc("GET /world/{id}/entries/{entryID}/edit", world.EntryEditForm)
	mux.HandleFunc("POST /world/{id}/entries/{entryID}", world.EntryUpdate)
	mux.HandleFunc("POST /world/{id}/entries/{entryID}/delete", world.EntryDelete)
	mux.HandleFunc("POST /world/{id}/entries/{entryID}/toggle", world.EntryToggleEnabled)

	mux.HandleFunc("GET /about", web.About)

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
	mux.HandleFunc("POST /settings/language", settings.LanguageUpdate)
	mux.HandleFunc("POST /settings/import-whitelist", settings.ImportWhitelistUpdate)
	mux.HandleFunc("POST /settings/image-presets", settings.ImagePresetCreate)
	mux.HandleFunc("GET /settings/image-presets/{id}/edit", settings.ImagePresetEditForm)
	mux.HandleFunc("POST /settings/image-presets/{id}", settings.ImagePresetUpdate)
	mux.HandleFunc("POST /settings/image-presets/{id}/delete", settings.ImagePresetDelete)

	character := web.CharacterHandlers{DB: sqlDB, Cache: importCache, Gate: vaultGate}
	mux.HandleFunc("GET /characters", character.List)
	mux.HandleFunc("POST /characters", character.Create)
	mux.HandleFunc("POST /characters/import", character.ImportCharacterJSON)
	mux.HandleFunc("POST /characters/import/resume", character.ResumeCharacterImport)
	mux.HandleFunc("POST /characters/import/confirm-language", character.ConfirmLanguageImport)
	mux.HandleFunc("POST /characters/import/confirm-language-batch", character.ConfirmLanguageImportBatch)
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
	mux.HandleFunc("POST /chat/{id}/images", chat.GenerateImage)
	mux.HandleFunc("POST /chat/{id}/images/suggest-prompt", chat.SuggestImagePrompt)
	mux.HandleFunc("POST /chat/{id}/image-preset", chat.SetImagePreset)

	vault := web.VaultHandlers{DB: sqlDB, Gate: vaultGate}
	mux.HandleFunc("GET /vault", vault.Show)
	mux.HandleFunc("POST /vault/setup", vault.Setup)
	mux.HandleFunc("POST /vault/unlock", vault.Unlock)
	mux.HandleFunc("POST /vault/lock", vault.Lock)
	mux.HandleFunc("POST /vault/reset", vault.Reset)
	// {kind} is chats|characters|worlds — see vaultKinds.
	mux.HandleFunc("POST /vault/{kind}/{id}/in", vault.MoveIn)
	mux.HandleFunc("POST /vault/{kind}/{id}/out", vault.MoveOut)
	// Decrypting counterpart to the plain static file server — see
	// imageSrc/VaultHandlers.Image in vault_crypto.go.
	mux.HandleFunc("GET /vault/image/{path...}", vault.Image)

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
	return web.WithSecurityHeaders(requireLocalHost(http.NewCrossOriginProtection().Handler(withLanguage(sqlDB)(mux))))
}

// withLanguage resolves the request's UI/system-prompt language once
// per request and stores it on the context every nested templ render
// call reads from (see internal/i18n.WithLang/FromContext) — closest
// to the actual routes in the chain since it's purely about render
// output, nothing security-relevant like the layers around it.
//
// Order of preference: the "aivalon-lang" cookie (what the switcher
// sets client-side, so it applies instantly without waiting on a DB
// round-trip's result) then AppSettings.Language (the server-side
// value the switcher ALSO writes — covers a fresh browser/profile
// with no cookie yet) then "en".
func withLanguage(sqlDB *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			lang := "en"
			if c, err := r.Cookie(web.LanguageCookie); err == nil {
				if code := strings.ToLower(c.Value); i18n.Default().Has(code) {
					lang = code
				}
			} else if settings, err := db.GetAppSettings(sqlDB); err == nil && i18n.Default().Has(settings.Language) {
				lang = settings.Language
			}
			next.ServeHTTP(w, r.WithContext(i18n.WithLang(r.Context(), lang)))
		})
	}
}

// privateHostRanges are the networks requireLocalHost accepts a bare
// IP Host header from, besides the literal "localhost" name — Lord's
// ask: reachable from another device on the same home WiFi (his
// phone), via AIVALON_ADDR=0.0.0.0:PORT, without opening the door to
// an arbitrary public address. Loopback is included here too (not
// just via the literal addr/localhost map below) so 127.0.0.1 keeps
// working even once addr itself is "0.0.0.0:PORT" and no longer
// equals "127.0.0.1:PORT" literally. A domain name (DNS-rebinding's
// whole point) never matches here — only a Host header that's
// already a literal IP in one of these ranges does, so rebinding a
// public domain to a private address still gets rejected on the
// Host string itself, same as before.
var privateHostRanges = mustParseCIDRs(
	"127.0.0.0/8",
	"::1/128",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		nets = append(nets, n)
	}
	return nets
}

// requireLocalHost rejects any request whose Host header isn't this
// server's own bind address (127.0.0.1:PORT, localhost:PORT, or —
// only once AIVALON_ADDR itself is set to a LAN-reachable address
// like 0.0.0.0:PORT — a bare IP literal in privateHostRanges, same
// port). Without it, a DNS entry that resolves to 127.0.0.1 — attacker-
// controlled, since DNS is public — lets a browser that visited that
// domain send real requests here with a Host header matching whatever
// the attacker chose, bypassing same-origin assumptions baked into the
// rest of the app's threat model (a purely local, single-user tool).
func requireLocalHost(next http.Handler) http.Handler {
	_, port, _ := strings.Cut(addr, ":")
	allowed := map[string]bool{addr: true, "localhost:" + port: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if allowed[r.Host] {
			next.ServeHTTP(w, r)
			return
		}
		host, hostPort, err := net.SplitHostPort(r.Host)
		if err == nil && hostPort == port {
			if ip := net.ParseIP(host); ip != nil {
				for _, n := range privateHostRanges {
					if n.Contains(ip) {
						next.ServeHTTP(w, r)
						return
					}
				}
			}
		}
		http.Error(w, "forbidden host", http.StatusForbidden)
	})
}

// noCacheHeaders forces revalidation on every /static/ request (see
// its registration above for why) without disabling caching outright
// — If-Modified-Since/If-None-Match still let http.FileServer answer
// 304 when the file is actually unchanged.
func noCacheHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
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
