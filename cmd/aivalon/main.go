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

	world := web.WorldHandlers{DB: sqlDB}
	mux.HandleFunc("GET /world", world.List)
	mux.HandleFunc("POST /world", world.Create)
	mux.HandleFunc("GET /world/{id}", world.Show)
	mux.HandleFunc("POST /world/{id}", world.Update)
	mux.HandleFunc("POST /world/{id}/delete", world.Delete)
	mux.HandleFunc("POST /world/{id}/entries", world.EntryCreate)
	mux.HandleFunc("GET /world/{id}/entries/{entryID}/edit", world.EntryEditForm)
	mux.HandleFunc("POST /world/{id}/entries/{entryID}", world.EntryUpdate)
	mux.HandleFunc("POST /world/{id}/entries/{entryID}/delete", world.EntryDelete)

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
	mux.HandleFunc("POST /settings/system-prompt", settings.SystemPromptUpdate)
	mux.HandleFunc("POST /settings/system-prompt/reset", settings.SystemPromptReset)

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
	mux.HandleFunc("POST /chat/{id}/delete", chat.Delete)
	mux.HandleFunc("POST /chat/{id}/messages", chat.PostMessage)
	mux.HandleFunc("POST /chat/{id}/messages/{messageID}/regenerate", chat.RegenerateMessage)
	mux.HandleFunc("POST /chat/{id}/messages/{messageID}/branch", chat.BranchMessage)
	mux.HandleFunc("POST /chat/{id}/messages/{messageID}/edit", chat.EditMessage)
	mux.HandleFunc("POST /chat/{id}/messages/{messageID}/delete", chat.DeleteMessage)
	mux.HandleFunc("GET /chat/{id}/stream", chat.Stream)

	return mux
}
