package web

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// TestVaultListRowActions covers Lord's last ask: each Vault tab row
// gets an eye icon (unhide, gold) and a trash icon (delete, red) at
// its end — and the trash icon must still go through the normal
// delete route/guard (no bypassing the "used by these chats" confirm
// page just because the click came from inside the Vault).
func TestVaultListRowActions(t *testing.T) {
	f := newFixture(t)
	gate := NewVaultGate()
	vh := VaultHandlers{DB: f.db, Gate: gate}
	ch := CharacterHandlers{DB: f.db, Gate: gate}
	loc := func(w *httptest.ResponseRecorder) string { return w.Header().Get("Location") }
	if err := db.SetVaultPIN(f.db, "h", "salt", 4); err != nil {
		t.Fatal(err)
	}
	if err := db.SetCharacterCardVaulted(f.db, f.cardA, true); err != nil { // used by f.session
		t.Fatal(err)
	}
	freshCard, _ := db.CreateCharacterCard(f.db, db.CharacterCard{Name: "Lonely", IsVaulted: true})
	cookie := unlockedCookie(t, gate)

	body := get(vh.Show, "/vault?tab=characters", nil, cookie).Body.String()
	if !strings.Contains(body, `class="icon-btn icon-btn-gold"`) {
		t.Error("Vault list rows should carry the gold unhide eye icon")
	}
	if !strings.Contains(body, `action="/characters/`+fmt.Sprint(f.cardA)+`/delete"`) {
		t.Errorf("Vault list row for cardA should have a delete form pointing at /characters/%d/delete", f.cardA)
	}

	// cardA is still used by f.session: trashing it from the Vault
	// must land on the same delete-guard confirm page as everywhere
	// else, not delete outright.
	w := deletePost(ch.Delete, f.cardA, cookie)
	if !strings.Contains(w.Body.String(), "The Quest") {
		t.Error("trashing a vaulted character still in use should show the delete-guard confirm page, not delete outright")
	}
	if _, err := db.GetCharacterCard(f.db, f.cardA); err != nil {
		t.Error("cardA should not have been deleted yet — confirm page was expected")
	}

	// freshCard isn't used by anything: trashing it from the Vault
	// deletes it straight away.
	if l := loc(deletePost(ch.Delete, freshCard, cookie)); l != "/vault?tab=characters" {
		t.Errorf("trashing an unused vaulted character → %q, want /vault?tab=characters", l)
	}
	if _, err := db.GetCharacterCard(f.db, freshCard); err == nil {
		t.Error("freshCard should have been deleted")
	}
}
