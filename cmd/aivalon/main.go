// Command aivalon runs the AIvalon server: a local, single-user web app for
// AI roleplay. See docs/DESIGN.md for the full design.
package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"

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

	mux := newMux(sqlDB)

	log.Printf("AIvalon listening on http://%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func newMux(sqlDB *sql.DB) *http.ServeMux {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/chat", http.StatusFound)
	})

	// World is still an empty shell — Chat and Character now have real,
	// working slices; World gets the same treatment once Lorebook CRUD
	// exists.
	mux.HandleFunc("GET /world", renderPlaceholder("World"))

	settings := web.SettingsHandlers{DB: sqlDB}
	mux.HandleFunc("GET /settings", settings.List)
	mux.HandleFunc("POST /settings/backend-profiles", settings.Create)
	mux.HandleFunc("GET /settings/backend-profiles/{id}/edit", settings.EditForm)
	mux.HandleFunc("POST /settings/backend-profiles/{id}", settings.Update)
	mux.HandleFunc("POST /settings/backend-profiles/{id}/delete", settings.Delete)
	mux.HandleFunc("POST /settings/personas", settings.PersonaCreate)
	mux.HandleFunc("GET /settings/personas/{id}/edit", settings.PersonaEditForm)
	mux.HandleFunc("POST /settings/personas/{id}", settings.PersonaUpdate)
	mux.HandleFunc("POST /settings/personas/{id}/delete", settings.PersonaDelete)

	character := web.CharacterHandlers{DB: sqlDB}
	mux.HandleFunc("GET /characters", character.List)
	mux.HandleFunc("POST /characters", character.Create)
	mux.HandleFunc("GET /characters/{id}/edit", character.EditForm)
	mux.HandleFunc("POST /characters/{id}", character.Update)
	mux.HandleFunc("POST /characters/{id}/delete", character.Delete)

	chat := web.ChatHandlers{DB: sqlDB}
	mux.HandleFunc("GET /chat", chat.List)
	mux.HandleFunc("POST /chat", chat.Create)
	mux.HandleFunc("GET /chat/{id}", chat.Show)
	mux.HandleFunc("POST /chat/{id}/messages", chat.PostMessage)
	mux.HandleFunc("GET /chat/{id}/stream", chat.Stream)

	return mux
}

func renderPlaceholder(section string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := web.Page(section, section).Render(r.Context(), w); err != nil {
			log.Printf("render %s: %v", section, err)
		}
	}
}
