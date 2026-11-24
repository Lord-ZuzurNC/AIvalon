package web

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// TestVaultResetWrongPINDeletesNothing: a wrong PIN on the "delete
// the whole vault" dialog must leave every vaulted row and file
// exactly where it was — this is the single most destructive action
// in the app, so the PIN check has to actually gate it for real, not
// just decorate the button.
func TestVaultResetWrongPINDeletesNothing(t *testing.T) {
	f := newFixture(t)
	t.Chdir(t.TempDir())
	gate := NewVaultGate()
	cookie := unlockedCookie(t, gate)

	if err := db.SetVaultPIN(f.db, "right-hash", "salt", 4); err != nil {
		t.Fatal(err)
	}
	charID, err := db.CreateCharacterCard(f.db, db.CharacterCard{Name: "Stays"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetCharacterCardVaulted(f.db, charID, true); err != nil {
		t.Fatal(err)
	}

	vh := VaultHandlers{DB: f.db, Gate: gate}
	r := httptest.NewRequest("POST", "/vault/reset", strings.NewReader("pin_hash=wrong-hash"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	vh.Reset(httptest.NewRecorder(), r)

	if _, err := db.GetCharacterCard(f.db, charID); err != nil {
		t.Fatalf("character should still exist after a wrong-PIN reset attempt: %v", err)
	}
	if _, err := db.GetVaultPIN(f.db); err != nil {
		t.Fatalf("vault PIN should still be configured: %v", err)
	}
}

// TestVaultResetDeletesEverything is the real thing: a correct PIN
// wipes every vaulted session/character/lorebook, their files on
// disk, and the vault's own PIN — back to "not configured".
func TestVaultResetDeletesEverything(t *testing.T) {
	f := newFixture(t)
	t.Chdir(t.TempDir())
	gate := NewVaultGate()
	cookie := unlockedCookie(t, gate)

	if err := db.SetVaultPIN(f.db, "test-hash", "test-salt", 4); err != nil {
		t.Fatal(err)
	}
	stored, err := db.GetVaultPIN(f.db)
	if err != nil {
		t.Fatal(err)
	}

	charID, err := db.CreateCharacterCard(f.db, db.CharacterCard{Name: "Gone"})
	if err != nil {
		t.Fatal(err)
	}
	ch := CharacterHandlers{DB: f.db, Gate: gate}
	charImage := mustUploadCharacterImage(t, ch, charID)
	if err := db.SetCharacterCardVaulted(f.db, charID, true); err != nil {
		t.Fatal(err)
	}

	bookID, err := db.CreateLorebook(f.db, db.Lorebook{Name: "GoneToo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetLorebookVaulted(f.db, bookID, true); err != nil {
		t.Fatal(err)
	}

	if err := db.SetSessionVaulted(f.db, f.session, true); err != nil {
		t.Fatal(err)
	}

	vh := VaultHandlers{DB: f.db, Gate: gate}
	r := httptest.NewRequest("POST", "/vault/reset", strings.NewReader("pin_hash="+stored.PINHash))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	vh.Reset(httptest.NewRecorder(), r)

	if _, err := db.GetCharacterCard(f.db, charID); err == nil {
		t.Fatal("character should be gone")
	}
	if _, err := db.GetLorebook(f.db, bookID); err == nil {
		t.Fatal("lorebook should be gone")
	}
	if _, err := db.GetSession(f.db, f.session); err == nil {
		t.Fatal("session should be gone")
	}
	if _, err := db.GetVaultPIN(f.db); err != db.ErrVaultNotConfigured {
		t.Fatalf("vault PIN should be gone (ErrVaultNotConfigured), got %v", err)
	}
	if _, err := os.Stat(filepath.Join("web", "static", stripQuery(charImage))); err == nil {
		t.Fatal("the character's image file should have been removed from disk")
	}
}
