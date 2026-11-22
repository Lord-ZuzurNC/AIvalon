package web

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// TestVaultedCharacterEditShowsVaultBehindDialog covers Lord's bug
// report: opening a vaulted character then clicking outside its
// dialog (templates.templ's click-to-dismiss script just removes the
// "open" attribute, revealing whatever page was rendered underneath)
// used to land on the plain Characters page — kicking him out of the
// Vault for nothing more than a stray click. GET
// /characters/{id}/edit on a vaulted card must render the Vault page
// itself (Characters tab, PIN not re-asked) with the edit dialog open
// on top, so dismissing it reveals the Vault, not the plain list.
func TestVaultedCharacterEditShowsVaultBehindDialog(t *testing.T) {
	f := newFixture(t)
	gate := NewVaultGate()
	if err := db.SetVaultPIN(f.db, "h", "salt", 4); err != nil {
		t.Fatal(err)
	}
	if err := db.SetCharacterCardVaulted(f.db, f.cardA, true); err != nil {
		t.Fatal(err)
	}
	ch := CharacterHandlers{DB: f.db, Gate: gate}
	cookie := unlockedCookie(t, gate)

	body := get(ch.EditForm, "/x", map[string]string{"id": fmt.Sprint(f.cardA)}, cookie).Body.String()

	if !strings.Contains(body, "<h1>Vault</h1>") {
		t.Error("vaulted character edit should render the Vault page, not the plain Characters page")
	}
	if strings.Contains(body, `@PageHeaderWithViewAndCreate`) || strings.Contains(body, "Add a character</button>") {
		t.Error("should not render the plain Characters page's own header")
	}
	if !strings.Contains(body, `<dialog id="character-modal" open`) {
		t.Error("the edit dialog should already be open")
	}
	if !strings.Contains(body, "Edit character") {
		t.Error("dialog should say \"Edit character\", not the create-form's own title")
	}
	if !strings.Contains(body, `value="Merlin"`) {
		t.Error("the dialog should carry the card's own data (pre-filled form)")
	}
	// The plain card (not vaulted) is unaffected — still the normal page.
	plainBody := get(ch.EditForm, "/x", map[string]string{"id": fmt.Sprint(f.cardB)}, nil).Body.String()
	if strings.Contains(plainBody, "<h1>Vault</h1>") {
		t.Error("a non-vaulted character's edit page should stay the plain Characters page")
	}
}
