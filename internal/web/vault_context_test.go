package web

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// multipartPost runs h on a multipart POST (the character/lorebook
// forms are multipart) carrying fields, path values and an optional cookie.
func multipartPost(t *testing.T, h http.HandlerFunc, fields, pathValues map[string]string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	mw.Close()
	r := httptest.NewRequest("POST", "/x", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	for k, v := range pathValues {
		r.SetPathValue(k, v)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func (f fixture) profileID(t *testing.T) int64 {
	t.Helper()
	var id int64
	if err := f.db.QueryRow(`SELECT id FROM backend_profiles LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// vignette returns the one vignette-card block named name, "" if absent.
func vignette(page, name string) string {
	i := strings.Index(page, `<div class="vignette-card" data-search-name="`+name+`"`)
	if i < 0 {
		return ""
	}
	rest := page[i+1:]
	if j := strings.Index(rest, `<div class="vignette-card"`); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

var badgeRe = regexp.MustCompile(`<span class="chat-count-visible">(\d+)</span>\s*<span class="chat-count-vaulted">(\d+)</span>`)

func TestVignetteBadgeSplitsVisibleAndVaultedChats(t *testing.T) {
	f := newFixture(t)
	p := f.profileID(t)
	// f.session (visible) already uses cardA + lorebook; add one more
	// visible and one vaulted session using both.
	for _, vaulted := range []bool{false, true} {
		id, err := db.CreateSession(f.db, db.Session{Title: "s", BackendProfileID: p, LorebookID: f.lorebook, IsVaulted: vaulted})
		if err != nil {
			t.Fatal(err)
		}
		db.AttachCharacterToSession(f.db, id, f.cardA, 0)
	}
	for _, c := range []struct {
		name string
		h    http.HandlerFunc
		item string
	}{
		{"characters", CharacterHandlers{DB: f.db, Gate: NewVaultGate()}.List, "Merlin"},
		{"worlds", WorldHandlers{DB: f.db, Gate: NewVaultGate()}.List, "Avalon"},
	} {
		v := vignette(get(c.h, "/x", nil, nil).Body.String(), c.item)
		m := badgeRe.FindStringSubmatch(v)
		if m == nil || m[1] != "2" || m[2] != "1" {
			t.Errorf("%s: badge = %v, want visible 2 / vaulted 1 in:\n%s", c.name, m, v)
		}
	}
	// The color swap lives in the CSS: check the actual rules.
	css, err := os.ReadFile("../../web/static/aivalon.css")
	if err != nil {
		t.Fatal(err)
	}
	for sel, want := range map[string][2]string{
		".chat-count-visible": {"var(--pico-primary)", "var(--pico-tertiary)"},
		".chat-count-vaulted": {"var(--pico-tertiary)", "var(--pico-primary)"},
	} {
		rule := regexp.MustCompile(regexp.QuoteMeta(sel) + ` \{([^}]*)\}`).FindSubmatch(css)
		if rule == nil || !strings.Contains(string(rule[1]), "background: "+want[0]) || !strings.Contains(string(rule[1]), "color: "+want[1]) {
			t.Errorf("%s rule = %q, want background %s / color %s", sel, rule, want[0], want[1])
		}
	}
	if !regexp.MustCompile(`--pico-tertiary: #[0-9a-f]{6};`).Match(css) {
		t.Error("--pico-tertiary not defined")
	}
}

func TestVignetteVaultIconHidesItem(t *testing.T) {
	f := newFixture(t)
	vh := VaultHandlers{DB: f.db, Gate: NewVaultGate()}
	for _, c := range []struct {
		kind, item, table string
		id                int64
		list              http.HandlerFunc
	}{
		{"characters", "Merlin", "character_cards", f.cardA, CharacterHandlers{DB: f.db, Gate: vh.Gate}.List},
		{"worlds", "Avalon", "lorebooks", f.lorebook, WorldHandlers{DB: f.db, Gate: vh.Gate}.List},
	} {
		action := fmt.Sprintf(`<form method="post" action="/vault/%s/%d/in" class="vignette-vault">`, c.kind, c.id)
		if v := vignette(get(c.list, "/x", nil, nil).Body.String(), c.item); !strings.Contains(v, action) {
			t.Fatalf("%s vignette has no vault form %s", c.kind, action)
		}
		if w := vaultPost(vh.MoveIn, c.kind, c.id, ""); w.Code != http.StatusSeeOther {
			t.Fatalf("MoveIn status %d", w.Code)
		}
		if n := f.count(t, `SELECT is_vaulted FROM `+c.table+` WHERE id = ?`, c.id); n != 1 {
			t.Errorf("%s: is_vaulted = %d after clicking the vault icon", c.kind, n)
		}
		if v := vignette(get(c.list, "/x", nil, nil).Body.String(), c.item); v != "" {
			t.Errorf("%s: %s still in the plain grid after vaulting", c.kind, c.item)
		}
	}
}

func TestVaultTabCreateMakesVaultedCharacterAndLorebook(t *testing.T) {
	f := newFixture(t)
	gate := NewVaultGate()
	ch := CharacterHandlers{DB: f.db, Gate: gate}
	wh := WorldHandlers{DB: f.db, Gate: gate}

	w := multipartPost(t, ch.Create, map[string]string{"name": "Ghost", "vault": "1"}, nil, nil)
	if n := f.count(t, `SELECT is_vaulted FROM character_cards WHERE name = 'Ghost'`); n != 1 || w.Header().Get("Location") != "/vault?tab=characters" {
		t.Errorf("vault create character: is_vaulted=%d, Location=%q", n, w.Header().Get("Location"))
	}
	w = multipartPost(t, ch.Create, map[string]string{"name": "Plain"}, nil, nil)
	if n := f.count(t, `SELECT is_vaulted FROM character_cards WHERE name = 'Plain'`); n != 0 || w.Header().Get("Location") != "/characters" {
		t.Errorf("plain create character: is_vaulted=%d, Location=%q", n, w.Header().Get("Location"))
	}
	multipartPost(t, wh.Create, map[string]string{"name": "Shadowland", "vault": "1"}, nil, nil)
	if n := f.count(t, `SELECT is_vaulted FROM lorebooks WHERE name = 'Shadowland'`); n != 1 {
		t.Errorf("vault create lorebook: is_vaulted=%d", n)
	}
	multipartPost(t, wh.Create, map[string]string{"name": "Daylight"}, nil, nil)
	if n := f.count(t, `SELECT is_vaulted FROM lorebooks WHERE name = 'Daylight'`); n != 0 {
		t.Errorf("plain create lorebook: is_vaulted=%d", n)
	}

	// The Vault page actually offers those "+" with vault=1 forms.
	v, err := loadVaultContents(f.db)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	VaultListPage(v, "salt").Render(t.Context(), &b)
	page := b.String()
	for _, id := range []string{"session-modal", "character-modal", "lorebook-modal", "character-import-modal-vault", "lorebook-import-modal-vault"} {
		if !strings.Contains(page, `data-modal-id="`+id+`"`) || !strings.Contains(page, `<dialog id="`+id+`"`) {
			t.Errorf("vault page lacks %s + button or dialog", id)
		}
	}
	// 3 create forms (session/character/lorebook) + 2 import forms
	// (computer tab + URL tab) each for Characters and Worlds = 7.
	if n := strings.Count(page, `<input type="hidden" name="vault" value="1">`); n != 7 {
		t.Errorf("want 7 vault=1 forms (3 create + 4 import), got %d", n)
	}
	// Each ImportModal has 2 forms (computer tab + URL tab) sharing
	// one action.
	if strings.Count(page, `action="/characters/import"`) != 2 || strings.Count(page, `action="/world/import"`) != 2 {
		t.Error("vault page should post its import forms to the same /characters/import and /world/import routes as the plain pages")
	}
}

func TestVaultChatCreatePickerMixesAndMarks(t *testing.T) {
	f := newFixture(t)
	plainCard, _ := db.CreateCharacterCard(f.db, db.CharacterCard{Name: "Twin"})
	hiddenCard, _ := db.CreateCharacterCard(f.db, db.CharacterCard{Name: "Twin", IsVaulted: true})
	plainBook, _ := db.CreateLorebook(f.db, db.Lorebook{Name: "Twin"})
	hiddenBook, _ := db.CreateLorebook(f.db, db.Lorebook{Name: "Twin", IsVaulted: true})

	v, err := loadVaultContents(f.db)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	VaultListPage(v, "salt").Render(t.Context(), &b)
	page := b.String()
	i := strings.Index(page, `<dialog id="session-modal">`)
	if i < 0 {
		t.Fatal("no session-modal on vault page")
	}
	modal := page[i : i+strings.Index(page[i:], "</dialog>")]

	// Each picker button is followed by its checkbox; split on those.
	pick := func(id int64) string {
		box := fmt.Sprintf(`value="%d" hidden>`, id)
		j := strings.Index(modal, box)
		if j < 0 {
			t.Fatalf("card %d missing from vault picker", id)
		}
		k := strings.LastIndex(modal[:j], `<button type="button" class="vignette-card character-pick"`)
		return modal[k:j]
	}
	if !strings.Contains(pick(hiddenCard), `class="vault-marker"`) || strings.Contains(pick(plainCard), `class="vault-marker"`) {
		t.Error("vault marker should be on the vaulted Twin card only")
	}
	if !strings.Contains(modal, fmt.Sprintf(`<option value="%d">🔒 Twin (vault)</option>`, hiddenBook)) ||
		!strings.Contains(modal, fmt.Sprintf(`<option value="%d">Twin</option>`, plainBook)) {
		t.Errorf("lorebook picker should list both Twins, vault one marked:\n%s", modal)
	}
	if !strings.Contains(modal, `<input type="hidden" name="vault" value="1">`) {
		t.Error("vault session modal doesn't post vault=1")
	}

	// The normal Chat page's dialog still shows only the plain ones.
	chat := get(ChatHandlers{DB: f.db, Gate: NewVaultGate()}.List, "/x", nil, nil).Body.String()
	if strings.Contains(chat, fmt.Sprintf(`value="%d" hidden>`, hiddenCard)) || strings.Contains(chat, fmt.Sprintf(`<option value="%d">`, hiddenBook)) ||
		strings.Contains(chat, "vault-marker") || strings.Contains(chat, `name="vault"`) {
		t.Error("plain New-session dialog leaks vaulted items or the vault flag")
	}

	// Submitting it creates a vaulted session.
	r := httptest.NewRequest("POST", "/chat", strings.NewReader(fmt.Sprintf("title=Hidden+one&vault=1&backend_profile_id=%d&lorebook_id=%d&character_ids=%d", f.profileID(t), hiddenBook, hiddenCard)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	ChatHandlers{DB: f.db, Gate: NewVaultGate()}.Create(w, r)
	if n := f.count(t, `SELECT is_vaulted FROM sessions WHERE title = 'Hidden one'`); w.Code != http.StatusSeeOther || n != 1 {
		t.Errorf("vault chat create: status %d, is_vaulted=%d", w.Code, n)
	}
}

// Part C's first question: does acting on a vaulted item clear the
// unlock? It doesn't — the re-prompt comes from GET /vault itself.
func TestUnlockSurvivesActingOnVaultedItems(t *testing.T) {
	f := newFixture(t)
	gate := NewVaultGate()
	db.SetVaultPIN(f.db, "h", "salt", 4)
	db.SetCharacterCardVaulted(f.db, f.cardA, true)
	db.SetSessionVaulted(f.db, f.session, true)
	cookie := unlockedCookie(t, gate)
	stillUnlocked := func(step string, w *httptest.ResponseRecorder) {
		t.Helper()
		for _, c := range w.Result().Cookies() {
			if c.Name == vaultCookieName {
				t.Errorf("%s: response touched the unlock cookie: %+v", step, c)
			}
		}
		r := httptest.NewRequest("GET", "/x", nil)
		r.AddCookie(cookie)
		if !gate.Unlocked(r) {
			t.Errorf("%s: gate no longer accepts the cookie", step)
		}
	}
	ch := CharacterHandlers{DB: f.db, Gate: gate}
	stillUnlocked("edit form", get(ch.EditForm, "/x", map[string]string{"id": fmt.Sprint(f.cardA)}, cookie))
	stillUnlocked("save", multipartPost(t, ch.Update, map[string]string{"name": "Merlin II"}, map[string]string{"id": fmt.Sprint(f.cardA)}, cookie))
	if w := get(ChatHandlers{DB: f.db, Gate: gate}.Show, "/x", map[string]string{"id": fmt.Sprint(f.session)}, cookie); w.Code != http.StatusOK {
		t.Fatalf("vaulted session with cookie: status %d", w.Code)
	} else {
		stillUnlocked("open vaulted chat", w)
	}

	vh := VaultHandlers{DB: f.db, Gate: gate}
	// Bare /vault re-prompts even while unlocked (Lord's "PIN every
	// opening" rule) — this is the "retype the code".
	if body := get(vh.Show, "/vault", nil, cookie).Body.String(); !strings.Contains(body, `action="/vault/unlock"`) {
		t.Error("bare /vault should still re-prompt")
	}
	// ?tab= (where vaulted saves now return) goes straight to the tab...
	body := get(vh.Show, "/vault?tab=characters", nil, cookie).Body.String()
	if strings.Contains(body, `action="/vault/unlock"`) || !strings.Contains(body, `data-tab-panel="characters" role="tabpanel">`) || !strings.Contains(body, "Merlin II") {
		t.Error("/vault?tab=characters while unlocked should show the Characters tab")
	}
	// ...but only for an unlocked browser.
	if body := get(vh.Show, "/vault?tab=characters", nil, nil).Body.String(); !strings.Contains(body, `action="/vault/unlock"`) || strings.Contains(body, "Merlin II") {
		t.Error("/vault?tab= without the cookie must still prompt")
	}
}

func TestVaultedItemsReturnToVaultTab(t *testing.T) {
	f := newFixture(t)
	gate := NewVaultGate()
	cookie := unlockedCookie(t, gate)
	ch := CharacterHandlers{DB: f.db, Gate: gate}
	wh := WorldHandlers{DB: f.db, Gate: gate}
	chat := ChatHandlers{DB: f.db, Gate: gate}
	loc := func(w *httptest.ResponseRecorder) string { return w.Header().Get("Location") }
	id := func(n int64) map[string]string { return map[string]string{"id": fmt.Sprint(n)} }

	// Non-vaulted: unchanged.
	if l := loc(multipartPost(t, ch.Update, map[string]string{"name": "Merlin"}, id(f.cardA), nil)); l != "/characters" {
		t.Errorf("plain character save → %q", l)
	}
	if body := get(chat.Show, "/x", id(f.session), nil).Body.String(); !strings.Contains(body, `<a href="/chat" class="back-link">`) {
		t.Error("plain session back-link should be /chat")
	}
	if body := get(wh.Show, "/x", id(f.lorebook), nil).Body.String(); !strings.Contains(body, `<a href="/world" class="back-link">`) {
		t.Error("plain lorebook back-link should be /world")
	}
	if l := loc(vaultPost(ch.Delete, "", f.unusedCard, "")); l != "/characters" {
		t.Errorf("plain character delete → %q", l)
	}
	if l := loc(vaultPost(wh.Delete, "", f.unusedBook, "")); l != "/world" {
		t.Errorf("plain lorebook delete → %q", l)
	}

	// Vaulted: back to the matching Vault tab.
	db.SetCharacterCardVaulted(f.db, f.cardA, true)
	db.SetLorebookVaulted(f.db, f.lorebook, true)
	db.SetSessionVaulted(f.db, f.session, true)
	if l := loc(multipartPost(t, ch.Update, map[string]string{"name": "Merlin"}, id(f.cardA), cookie)); l != "/vault?tab=characters" {
		t.Errorf("vaulted character save → %q", l)
	}
	if body := get(chat.Show, "/x", id(f.session), cookie).Body.String(); !strings.Contains(body, `<a href="/vault?tab=chats" class="back-link">`) {
		t.Error("vaulted session back-link should be /vault?tab=chats")
	}
	if body := get(wh.Show, "/x", id(f.lorebook), cookie).Body.String(); !strings.Contains(body, `<a href="/vault?tab=worlds" class="back-link">`) {
		t.Error("vaulted lorebook back-link should be /vault?tab=worlds")
	}
	// Delete-confirm (card still used by a session): Cancel goes to the vault tab.
	if body := deletePost(ch.Delete, f.cardA, cookie).Body.String(); !strings.Contains(body, `href="/vault?tab=characters"`) {
		t.Error("vaulted delete-confirm cancel should go to the vault tab")
	}
	newCard, _ := db.CreateCharacterCard(f.db, db.CharacterCard{Name: "Hidden", IsVaulted: true})
	if l := loc(deletePost(ch.Delete, newCard, cookie)); l != "/vault?tab=characters" {
		t.Errorf("vaulted character delete → %q", l)
	}
	newBook, _ := db.CreateLorebook(f.db, db.Lorebook{Name: "Hidden", IsVaulted: true})
	if l := loc(deletePost(wh.Delete, newBook, cookie)); l != "/vault?tab=worlds" {
		t.Errorf("vaulted lorebook delete → %q", l)
	}
	// The fix this test exists for: the SAME requests, minus the
	// unlock cookie, must now be refused — before the vault-mutation
	// gap was closed, these deleted the item anyway. Fresh rows:
	// newCard/newBook above are already gone from the deletes just
	// proven correct.
	freshCard, _ := db.CreateCharacterCard(f.db, db.CharacterCard{Name: "Hidden 2", IsVaulted: true})
	if w := deletePost(ch.Delete, freshCard, nil); w.Code != http.StatusForbidden {
		t.Errorf("unauthenticated vaulted character delete: status %d, want 403", w.Code)
	}
	if _, err := db.GetCharacterCard(f.db, freshCard); err != nil {
		t.Error("vaulted character was deleted without the vault unlocked")
	}
	freshBook, _ := db.CreateLorebook(f.db, db.Lorebook{Name: "Hidden 2", IsVaulted: true})
	if w := deletePost(wh.Delete, freshBook, nil); w.Code != http.StatusForbidden {
		t.Errorf("unauthenticated vaulted lorebook delete: status %d, want 403", w.Code)
	}
	if _, err := db.GetLorebook(f.db, freshBook); err != nil {
		t.Error("vaulted lorebook was deleted without the vault unlocked")
	}
	if l := loc(multipartPost(t, ch.Update, map[string]string{"name": "Should not save"}, id(freshCard), nil)); l != "" {
		t.Errorf("unauthenticated vaulted character save should 403, got redirect %q", l)
	}
	if saved, _ := db.GetCharacterCard(f.db, freshCard); saved.Name == "Should not save" {
		t.Error("vaulted character was saved without the vault unlocked")
	}
	r := httptest.NewRequest("POST", "/x", nil)
	r.SetPathValue("id", fmt.Sprint(f.session))
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	chat.Delete(w, r)
	if l := loc(w); l != "/vault?tab=chats" {
		t.Errorf("vaulted chat delete → %q", l)
	}
}
