package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// unlockedCookie returns a cookie gate accepts as "vault unlocked".
func unlockedCookie(t *testing.T, gate *VaultGate) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := gate.grantUnlock(rec); err != nil {
		t.Fatal(err)
	}
	return rec.Result().Cookies()[0]
}

func get(h http.HandlerFunc, path string, pathValues map[string]string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
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

func vaultPost(h http.HandlerFunc, kind string, id int64, pinHash string) *httptest.ResponseRecorder {
	form := url.Values{"pin_hash": {pinHash}}
	r := httptest.NewRequest("POST", "/x", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("kind", kind)
	r.SetPathValue("id", fmt.Sprint(id))
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

// deletePost is vaultPost's counterpart for the plain Character/World
// Delete handlers — those take just {id} (no {kind}/pin_hash) and, as
// of the vault-mutation-routes fix, DO care about the unlock cookie
// when the target is vaulted. vaultPost never attaches one, which is
// exactly how the earlier version of this test accidentally exercised
// the unauthenticated path instead of the real one.
func deletePost(h http.HandlerFunc, id int64, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/x", nil)
	r.SetPathValue("id", fmt.Sprint(id))
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestVaultedDirectURLsRefusedUntilUnlocked(t *testing.T) {
	f := newFixture(t)
	gate := NewVaultGate()
	if err := db.SetVaultPIN(f.db, "h", "salt", 4); err != nil {
		t.Fatal(err)
	}
	if err := db.SetCharacterCardVaulted(f.db, f.cardA, true); err != nil {
		t.Fatal(err)
	}
	if err := db.SetLorebookVaulted(f.db, f.lorebook, true); err != nil {
		t.Fatal(err)
	}
	entryID, err := db.CreateLorebookEntry(f.db, db.LorebookEntry{LorebookID: f.lorebook, Content: "secret lore", TriggerStrategy: "constant", Probability: 100, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ch := CharacterHandlers{DB: f.db, Gate: gate}
	wh := WorldHandlers{DB: f.db, Gate: gate}
	cases := []struct {
		name   string
		h      http.HandlerFunc
		vals   map[string]string
		secret string
	}{
		{"character edit", ch.EditForm, map[string]string{"id": fmt.Sprint(f.cardA)}, "Merlin"},
		{"lorebook show", wh.Show, map[string]string{"id": fmt.Sprint(f.lorebook)}, "Avalon"},
		{"lorebook entry edit", wh.EntryEditForm, map[string]string{"id": fmt.Sprint(f.lorebook), "entryID": fmt.Sprint(entryID)}, "secret lore"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := get(c.h, "/x", c.vals, nil)
			if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), c.secret) {
				t.Fatalf("locked: status %d, leaked=%v", w.Code, strings.Contains(w.Body.String(), c.secret))
			}
			w = get(c.h, "/x", c.vals, unlockedCookie(t, gate))
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), c.secret) {
				t.Fatalf("unlocked: status %d, want 200 showing %q", w.Code, c.secret)
			}
		})
	}
	// A non-vaulted card needs no unlock.
	if w := get(ch.EditForm, "/x", map[string]string{"id": fmt.Sprint(f.cardB)}, nil); w.Code != http.StatusOK {
		t.Fatalf("non-vaulted card: status %d", w.Code)
	}
}

func TestVaultMoveInOutFlipsDB(t *testing.T) {
	for _, k := range []struct {
		kind, table string
		id          func(fixture) int64
	}{
		{"chats", "sessions", func(f fixture) int64 { return f.session }},
		{"characters", "character_cards", func(f fixture) int64 { return f.cardA }},
		{"worlds", "lorebooks", func(f fixture) int64 { return f.lorebook }},
	} {
		t.Run(k.kind, func(t *testing.T) {
			f := newFixture(t)
			if err := db.SetVaultPIN(f.db, "goodhash", "salt", 4); err != nil {
				t.Fatal(err)
			}
			h := VaultHandlers{DB: f.db, Gate: NewVaultGate()}
			vaulted := func() int { return f.count(t, `SELECT is_vaulted FROM `+k.table+` WHERE id = ?`, k.id(f)) }

			if w := vaultPost(h.MoveIn, k.kind, k.id(f), ""); w.Code != http.StatusSeeOther || vaulted() != 1 {
				t.Fatalf("MoveIn: status %d, is_vaulted=%d", w.Code, vaulted())
			}
			w := vaultPost(h.MoveOut, k.kind, k.id(f), "wronghash")
			if vaulted() != 1 {
				t.Fatal("wrong PIN un-vaulted it")
			}
			// The wrong-PIN re-render reopens exactly this item's dialog.
			reopened := fmt.Sprintf(`id="unhide-modal-%s-%d" class="pin-pad-modal" data-autoopen="true"`, k.kind, k.id(f))
			if !strings.Contains(w.Body.String(), reopened) || strings.Count(w.Body.String(), `data-autoopen="true"`) != 1 {
				t.Fatalf("wrong-PIN page doesn't reopen exactly %s's dialog", k.kind)
			}
			if w := vaultPost(h.MoveOut, k.kind, k.id(f), "goodhash"); w.Code != http.StatusSeeOther || vaulted() != 0 {
				t.Fatalf("MoveOut: status %d, is_vaulted=%d", w.Code, vaulted())
			}
		})
	}
	f := newFixture(t)
	if w := vaultPost(VaultHandlers{DB: f.db, Gate: NewVaultGate()}.MoveIn, "bogus", 1, ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown kind: status %d, want 404", w.Code)
	}
}

func TestVaultPageTabsListEachKind(t *testing.T) {
	f := newFixture(t)
	db.SetSessionVaulted(f.db, f.session, true)
	db.SetCharacterCardVaulted(f.db, f.cardA, true)
	db.SetLorebookVaulted(f.db, f.lorebook, true)
	v, err := loadVaultContents(f.db)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	VaultListPage(v, "salt").Render(t.Context(), &b)
	page := b.String()

	panel := func(kind string) string {
		i := strings.Index(page, `data-tab-panel="`+kind+`"`)
		if i < 0 {
			t.Fatalf("no %s panel", kind)
		}
		rest := page[i:]
		if j := strings.Index(rest[1:], "data-tab-panel="); j >= 0 {
			rest = rest[:j+1]
		}
		return rest
	}
	for kind, want := range map[string]string{
		"chats":      fmt.Sprintf(`/vault/chats/%d/out`, f.session),
		"characters": fmt.Sprintf(`/vault/characters/%d/out`, f.cardA),
		"worlds":     fmt.Sprintf(`/vault/worlds/%d/out`, f.lorebook),
	} {
		p := panel(kind)
		if !strings.Contains(p, want) || strings.Count(p, "<li>") != 1 {
			t.Errorf("%s panel: want exactly one item posting to %s", kind, want)
		}
	}
	if !strings.Contains(page, `data-tab-panel="chats" role="tabpanel">`) || !strings.Contains(page, `data-tab-panel="characters" role="tabpanel" hidden`) {
		t.Error("Chats should be the visible default tab")
	}
	if !strings.Contains(panel("chats"), "The Quest") || !strings.Contains(panel("characters"), "Merlin") || !strings.Contains(panel("worlds"), "Avalon") {
		t.Error("tab item titles missing")
	}
}

var activeNavRe = regexp.MustCompile(`<a href="([^"]*)" aria-current="page"`)

func TestNavMarksExactlyTheRightSection(t *testing.T) {
	f := newFixture(t)
	gate := NewVaultGate()
	db.SetVaultPIN(f.db, "h", "salt", 4)
	vh := VaultHandlers{DB: f.db, Gate: gate}
	id := map[string]string{"id": fmt.Sprint(f.session)}
	cases := []struct {
		name, want string
		h          http.HandlerFunc
		vals       map[string]string
	}{
		{"chat list", "/chat", ChatHandlers{DB: f.db, Gate: gate}.List, nil},
		{"chat session", "/chat", ChatHandlers{DB: f.db, Gate: gate}.Show, id},
		{"character list", "/characters", CharacterHandlers{DB: f.db, Gate: gate}.List, nil},
		{"character edit", "/characters", CharacterHandlers{DB: f.db, Gate: gate}.EditForm, map[string]string{"id": fmt.Sprint(f.cardA)}},
		{"world list", "/world", WorldHandlers{DB: f.db, Gate: gate}.List, nil},
		{"lorebook", "/world", WorldHandlers{DB: f.db, Gate: gate}.Show, map[string]string{"id": fmt.Sprint(f.lorebook)}},
		{"vault", "/vault", vh.Show, nil},
		{"settings", "/settings", SettingsHandlers{DB: f.db}.List, nil},
		{"about", "/about", About, nil},
	}
	for _, c := range cases {
		w := get(c.h, "/x", c.vals, nil)
		m := activeNavRe.FindAllStringSubmatch(w.Body.String(), -1)
		if w.Code != http.StatusOK || len(m) != 1 || m[0][1] != c.want {
			t.Errorf("%s: status %d, active nav links %v, want exactly [%s]", c.name, w.Code, m, c.want)
		}
	}
	// The vault list page (post-unlock) too.
	v, _ := loadVaultContents(f.db)
	var b strings.Builder
	VaultListPage(v, "salt").Render(t.Context(), &b)
	if m := activeNavRe.FindAllStringSubmatch(b.String(), -1); len(m) != 1 || m[0][1] != "/vault" {
		t.Errorf("vault list: active nav links %v", m)
	}
	// Order: Chats, Characters, Worlds, Vault, Settings.
	labels := regexp.MustCompile(`<span class="nav-label">([^<]*)</span>`).FindAllStringSubmatch(b.String(), -1)
	var got []string
	for _, l := range labels {
		got = append(got, l[1])
	}
	if want := "AIvalon,Chats,Characters,Worlds,Vault,Settings,About"; strings.Join(got, ",") != want {
		t.Errorf("nav labels = %v, want %s", got, want)
	}
}
