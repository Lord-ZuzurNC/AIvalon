package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// filePost is multipartPost's counterpart for a real file field (the
// importers' "From your Computer" tab) instead of plain form fields.
func filePost(h http.HandlerFunc, filename string, fileBody []byte, extraFields map[string]string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range extraFields {
		mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", filename)
	fw.Write(fileBody)
	mw.Close()
	r := httptest.NewRequest("POST", "/x", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

// TestVaultImportLandsVaulted covers Lord's last ask: importing a
// character or lorebook JSON from the Vault page's own import dialog
// (vault=1, same convention as the "+" forms) must create the item
// already hidden and bounce back to the Vault tab, not the plain
// list.
func TestVaultImportLandsVaulted(t *testing.T) {
	f := newFixture(t)
	ch := CharacterHandlers{DB: f.db, Cache: NewImportCache()}
	wh := WorldHandlers{DB: f.db, Cache: ch.Cache}

	w := filePost(ch.ImportCharacterJSON, "merlin.json", []byte(`{"data":{"name":"Merlin From Vault"}}`), map[string]string{"vault": "1"})
	if l := w.Header().Get("Location"); l != "/vault?tab=characters" {
		t.Errorf("vaulted character import redirect → %q, want /vault?tab=characters", l)
	}
	if n := f.count(t, `SELECT is_vaulted FROM character_cards WHERE name = 'Merlin From Vault'`); n != 1 {
		t.Errorf("imported character is_vaulted = %d, want 1", n)
	}

	w = filePost(ch.ImportCharacterJSON, "arthur.json", []byte(`{"data":{"name":"Arthur Plain"}}`), nil)
	// task t_6fe4805d/4: Lord's own ask — a plain import now lands
	// back on the list, not the just-created item's own edit page.
	if l := w.Header().Get("Location"); l != "/characters" {
		t.Errorf("plain character import redirect → %q, want /characters", l)
	}
	if n := f.count(t, `SELECT is_vaulted FROM character_cards WHERE name = 'Arthur Plain'`); n != 0 {
		t.Errorf("plain imported character is_vaulted = %d, want 0", n)
	}

	silBook := `{"entries":{"0":{"key":["avalon"],"content":"A hidden isle.","order":0}}}`
	w = filePost(wh.ImportLorebookJSON, "avalon.json", []byte(silBook), map[string]string{"vault": "1"})
	if l := w.Header().Get("Location"); l != "/vault?tab=worlds" {
		t.Errorf("vaulted lorebook import redirect → %q, want /vault?tab=worlds", l)
	}
	if n := f.count(t, `SELECT is_vaulted FROM lorebooks WHERE name = 'avalon'`); n != 1 {
		t.Errorf("imported lorebook is_vaulted = %d, want 1", n)
	}

	// A character card with an embedded book, imported vaulted: the
	// embedded lorebook must land vaulted too — same hidden-ness as
	// the character it rode in with.
	cardWithBook := `{"data":{"name":"Vivien","character_book":{"name":"Vivien Lore","entries":[{"keys":["magic"],"content":"She knows magic.","enabled":true}]}}}`
	w = filePost(ch.ImportCharacterJSON, "vivien.json", []byte(cardWithBook), map[string]string{"vault": "1"})
	if l := w.Header().Get("Location"); l != "/vault?tab=characters" {
		t.Errorf("vaulted character-with-book import redirect → %q", l)
	}
	if n := f.count(t, `SELECT is_vaulted FROM character_cards WHERE name = 'Vivien'`); n != 1 {
		t.Error("Vivien should be vaulted")
	}
	if n := f.count(t, `SELECT is_vaulted FROM lorebooks WHERE name = 'Vivien Lore'`); n != 1 {
		t.Error("Vivien's embedded lorebook should be vaulted too, not just the character")
	}
}
