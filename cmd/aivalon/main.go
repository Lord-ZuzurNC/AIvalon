// Command aivalon runs the AIvalon server: a local, single-user web app for
// AI roleplay. See docs/DESIGN.md for the full design.
package main

import (
	"database/sql"
	"log"
	"net/http"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
	"github.com/Lord-ZuzurNC/AIvalon/internal/web"
)

// dbPath is a placeholder for now: a file next to the binary's working
// directory. It is already covered by .gitignore. A proper XDG data dir
// (and a --db flag) is a follow-up, not needed to prove the stack boots.
const dbPath = "aivalon.db"

// addr is 127.0.0.1-only by design — see docs/DESIGN.md ("Default bind").
const addr = "127.0.0.1:4140"

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

	// Chat/World/Character are still an empty shell — the point this first
	// pass proves is the nav, static assets, and DB wiring, not the
	// features behind each tab yet.
	mux.HandleFunc("GET /chat", renderPlaceholder("Chat"))
	mux.HandleFunc("GET /world", renderPlaceholder("World"))
	mux.HandleFunc("GET /characters", renderPlaceholder("Character"))

	settings := web.SettingsHandlers{DB: sqlDB}
	mux.HandleFunc("GET /settings", settings.List)
	mux.HandleFunc("POST /settings/backend-profiles", settings.Create)
	mux.HandleFunc("GET /settings/backend-profiles/{id}/edit", settings.EditForm)
	mux.HandleFunc("POST /settings/backend-profiles/{id}", settings.Update)
	mux.HandleFunc("POST /settings/backend-profiles/{id}/delete", settings.Delete)

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
