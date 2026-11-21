package web

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// fixture: one "Quest" chat using persona P, lorebook L and characters
// A+B (a group chat), with messages; plus unused persona/lorebook/card.
type fixture struct {
	db                                    *sql.DB
	persona, lorebook, cardA, cardB       int64
	unusedPersona, unusedBook, unusedCard int64
	session                               int64
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	must := func(id int64, err error) int64 {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	f := fixture{db: sqlDB}
	profile := must(db.CreateBackendProfile(sqlDB, db.BackendProfile{Name: "p", Kind: "llm", BaseURL: "http://x"}))
	f.persona = must(db.CreatePersona(sqlDB, db.Persona{Name: "Hero"}))
	f.lorebook = must(db.CreateLorebook(sqlDB, db.Lorebook{Name: "Avalon"}))
	f.cardA = must(db.CreateCharacterCard(sqlDB, db.CharacterCard{Name: "Merlin"}))
	f.cardB = must(db.CreateCharacterCard(sqlDB, db.CharacterCard{Name: "Arthur"}))
	f.unusedPersona = must(db.CreatePersona(sqlDB, db.Persona{Name: "Nobody"}))
	f.unusedBook = must(db.CreateLorebook(sqlDB, db.Lorebook{Name: "Empty"}))
	f.unusedCard = must(db.CreateCharacterCard(sqlDB, db.CharacterCard{Name: "Loner"}))
	f.session = must(db.CreateSession(sqlDB, db.Session{Title: "The Quest", BackendProfileID: profile, PersonaID: f.persona, LorebookID: f.lorebook}))
	for i, c := range []int64{f.cardA, f.cardB} {
		if err := db.AttachCharacterToSession(sqlDB, f.session, c, i); err != nil {
			t.Fatal(err)
		}
	}
	must(db.CreateMessage(sqlDB, db.ChatMessage{SessionID: f.session, Role: "user", Content: "hi"}))
	must(db.CreateMessage(sqlDB, db.ChatMessage{SessionID: f.session, Role: "character", CharacterCardID: f.cardA, Content: "hello"}))
	return f
}

func (f fixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func post(h http.HandlerFunc, id int64, confirm bool) *httptest.ResponseRecorder {
	form := url.Values{}
	if confirm {
		form.Set("confirm", "1")
	}
	r := httptest.NewRequest("POST", "/x", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", fmt.Sprint(id))
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestDeleteGuard(t *testing.T) {
	type entity struct {
		name    string
		handler func(fixture) http.HandlerFunc
		used    func(fixture) int64
		unused  func(fixture) int64
		table   string
	}
	entities := []entity{
		{"persona", func(f fixture) http.HandlerFunc { return SettingsHandlers{DB: f.db}.PersonaDelete },
			func(f fixture) int64 { return f.persona }, func(f fixture) int64 { return f.unusedPersona }, "personas"},
		{"lorebook", func(f fixture) http.HandlerFunc { return WorldHandlers{DB: f.db}.Delete },
			func(f fixture) int64 { return f.lorebook }, func(f fixture) int64 { return f.unusedBook }, "lorebooks"},
		{"character (group chat)", func(f fixture) http.HandlerFunc { return CharacterHandlers{DB: f.db}.Delete },
			func(f fixture) int64 { return f.cardA }, func(f fixture) int64 { return f.unusedCard }, "character_cards"},
	}
	for _, e := range entities {
		t.Run(e.name+"/unused deletes immediately", func(t *testing.T) {
			f := newFixture(t)
			w := post(e.handler(f), e.unused(f), false)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status %d, body %s", w.Code, w.Body)
			}
			if n := f.count(t, `SELECT COUNT(*) FROM `+e.table+` WHERE id = ?`, e.unused(f)); n != 0 {
				t.Fatalf("unused entity still exists")
			}
			if n := f.count(t, `SELECT COUNT(*) FROM sessions`); n != 1 {
				t.Fatalf("sessions = %d, want 1 (untouched)", n)
			}
		})
		t.Run(e.name+"/in use without confirm deletes nothing", func(t *testing.T) {
			f := newFixture(t)
			w := post(e.handler(f), e.used(f), false)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "The Quest") || !strings.Contains(w.Body.String(), `name="confirm"`) {
				t.Fatalf("want confirmation page listing the chat, got %d: %s", w.Code, w.Body)
			}
			if f.count(t, `SELECT COUNT(*) FROM `+e.table+` WHERE id = ?`, e.used(f)) != 1 ||
				f.count(t, `SELECT COUNT(*) FROM sessions WHERE id = ?`, f.session) != 1 ||
				f.count(t, `SELECT COUNT(*) FROM messages WHERE session_id = ?`, f.session) != 2 ||
				f.count(t, `SELECT COUNT(*) FROM session_characters WHERE session_id = ?`, f.session) != 2 {
				t.Fatal("something was deleted without confirmation")
			}
		})
		t.Run(e.name+"/in use with confirm deletes chats then entity", func(t *testing.T) {
			f := newFixture(t)
			w := post(e.handler(f), e.used(f), true)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status %d, body %s", w.Code, w.Body)
			}
			if f.count(t, `SELECT COUNT(*) FROM `+e.table+` WHERE id = ?`, e.used(f)) != 0 ||
				f.count(t, `SELECT COUNT(*) FROM sessions WHERE id = ?`, f.session) != 0 ||
				f.count(t, `SELECT COUNT(*) FROM messages WHERE session_id = ?`, f.session) != 0 ||
				f.count(t, `SELECT COUNT(*) FROM session_characters WHERE session_id = ?`, f.session) != 0 {
				t.Fatal("confirmed delete left rows behind")
			}
			// The other group-chat member survives; only the session goes.
			if f.count(t, `SELECT COUNT(*) FROM character_cards WHERE id = ?`, f.cardB) != 1 {
				t.Fatal("other character in the group chat was deleted")
			}
		})
	}
}

func TestDeleteConfirmMasksVaultedTitles(t *testing.T) {
	var buf bytes.Buffer
	DeleteConfirmPage("World", "Avalon", "/world/1/delete", "/world", []db.SessionRef{{ID: 1, Title: "Secret affair", IsVaulted: true}}).Render(context.Background(), &buf)
	if strings.Contains(buf.String(), "Secret affair") || !strings.Contains(buf.String(), "hidden chat in the Vault") {
		t.Fatalf("vaulted title leaked or missing placeholder: %s", buf.String())
	}
}

func TestChatCountRendering(t *testing.T) {
	cards := []db.CharacterCard{{ID: 1, Name: "Zero"}, {ID: 2, Name: "One", ChatCount: 1}, {ID: 3, Name: "Many", ChatCount: 12}}
	books := []db.Lorebook{{ID: 1, Name: "Zero"}, {ID: 2, Name: "One", ChatCount: 1}, {ID: 3, Name: "Many", ChatCount: 12, EntryCount: 4}}
	personas := []db.Persona{{ID: 1, Name: "Zero"}, {ID: 2, Name: "One", ChatCount: 1}, {ID: 3, Name: "Many", ChatCount: 12}}

	pages := map[string]string{}
	var buf bytes.Buffer
	CharacterPage(cards, Pager{}, nil, nil).Render(context.Background(), &buf)
	pages["character"] = buf.String()
	buf.Reset()
	WorldPage(books, Pager{}).Render(context.Background(), &buf)
	pages["world"] = buf.String()
	buf.Reset()
	SettingsPageBody(nil, nil, personas, nil, nil, nil, "", db.AppSettings{}).Render(context.Background(), &buf)
	pages["persona"] = buf.String()

	for name, html := range pages {
		for _, want := range []string{"<th>Chats</th>", "<td>0</td>", "<td>1</td>", "<td>12</td>"} {
			if !strings.Contains(html, want) {
				t.Errorf("%s list: missing %q", name, want)
			}
		}
	}
	for _, name := range []string{"character", "world"} {
		html := pages[name]
		if n := strings.Count(html, `class="chat-count-badge"`); n != 2 {
			t.Errorf("%s grid: %d badges, want 2 (none for the unused item)", name, n)
		}
		for _, want := range []string{`>1</span>`, `>12</span>`} {
			if !strings.Contains(html, want) {
				t.Errorf("%s grid: missing badge %q", name, want)
			}
		}
	}
	if strings.Contains(pages["persona"], "chat-count-badge") {
		t.Error("persona list should have no badge")
	}
}
