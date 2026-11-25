package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// frenchCharacterJSON is a Character Card V2 upload whose four
// detection-relevant fields (description/personality/scenario/
// first_mes) are confidently French — well above internal/langdetect's
// length and confidence floors (see its own tests for the exact
// numbers this wording was checked against).
const frenchCharacterJSON = `{
	"data": {
		"name": "Merlin Francais",
		"description": "Un sage sorcier vivant dans une tour isolée au cœur de la forêt enchantée.",
		"personality": "Il est calme, patient et plein de sagesse ancienne.",
		"scenario": "Il enseigne la magie à son apprentie dans la tour.",
		"first_mes": "Bonjour, jeune apprenti. Es-tu prêt pour ta première leçon de magie ?",
		"creator_notes": "Fonctionne mieux avec un modèle 13B ou plus."
	}
}`

// englishCharacterJSON is a plain English card — the "nothing to flag"
// control case, same target language as a fresh install's default.
const englishCharacterJSON = `{
	"data": {
		"name": "Merlin English",
		"description": "A wise wizard living in a remote tower at the heart of an enchanted forest.",
		"personality": "He is calm, patient, and full of ancient wisdom.",
		"scenario": "He teaches magic to his apprentice inside the tower.",
		"first_mes": "Hello, young apprentice. Are you ready for your first lesson in magic?"
	}
}`

// frenchLorebookJSON is a standalone SillyTavern World Info export
// whose entries are confidently French.
const frenchLorebookJSON = `{
	"entries": {
		"0": {"key": ["tour"], "content": "La tour de Merlin se dresse au cœur d'une forêt enchantée, loin de tout village.", "constant": false, "disable": false, "order": 0, "probability": 100},
		"1": {"key": ["forêt"], "content": "La forêt enchantée abrite des créatures magiques et des ruines oubliées depuis des siècles.", "constant": false, "disable": false, "order": 1, "probability": 100}
	}
}`

var tokenInputRe = regexp.MustCompile(`name="token" value="([^"]+)"`)

// extractToken pulls ImportLanguageConfirmPage's hidden token field
// out of a rendered response body — the only way to drive the real
// confirm→resume HTTP round trip from a test without reaching into
// ImportCache directly.
func extractToken(t *testing.T, body string) string {
	t.Helper()
	m := tokenInputRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no token field found in response body:\n%s", body)
	}
	return m[1]
}

// mockTranslationServer is a fake OpenAI-compatible chat/completions
// endpoint that deterministically "translates" by echoing the user
// message back prefixed with "[EN] " — enough to prove a real
// per-field llmclient.StreamChat round trip happened (see
// docs/DESIGN.md's "Translation backend and fallback"), without
// depending on an actual LLM being installed.
func mockTranslationServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var userText string
		for _, m := range body.Messages {
			if m.Role == "user" {
				userText = m.Content
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", "[EN] "+userText)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestImportCharacterJSON_LanguageMismatchShowsConfirmPage(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	h := CharacterHandlers{DB: sqlDB, Cache: NewImportCache()}

	w := filePost(h.ImportCharacterJSON, "merlin.json", []byte(frenchCharacterJSON), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (confirm page), body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"Français", "English"} {
		if !strings.Contains(body, want) {
			t.Errorf("confirm page missing %q in body:\n%s", want, body)
		}
	}
	extractToken(t, body) // must be present and parseable
	if n, err := db.CountCharacterCards(sqlDB); err != nil || n != 0 {
		t.Fatalf("nothing must be created before Confirm is clicked, got n=%d err=%v", n, err)
	}
}

func TestImportCharacterJSON_SameLanguageSkipsConfirmPage(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	h := CharacterHandlers{DB: sqlDB, Cache: NewImportCache()}

	w := filePost(h.ImportCharacterJSON, "merlin.json", []byte(englishCharacterJSON), nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (plain import, no mismatch), body: %s", w.Code, w.Body.String())
	}
	if n, err := db.CountCharacterCards(sqlDB); err != nil || n != 1 {
		t.Fatalf("expected the single plain import to land, got n=%d err=%v", n, err)
	}
}

// TestConfirmLanguageImport_Character_CreatesVOAndTranslatedVF is the
// end-to-end double-import path: upload → confirm page → Confirm →
// two character cards, VO untouched and VF translated via a real
// (mock) llmclient.StreamChat round trip.
func TestConfirmLanguageImport_Character_CreatesVOAndTranslatedVF(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	srv := mockTranslationServer(t)
	if _, err := db.CreateBackendProfile(sqlDB, db.BackendProfile{Name: "Local", Kind: "llm", BaseURL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	h := CharacterHandlers{DB: sqlDB, Cache: NewImportCache()}

	confirmBody := filePost(h.ImportCharacterJSON, "merlin.json", []byte(frenchCharacterJSON), nil).Body.String()
	token := extractToken(t, confirmBody)

	form := url.Values{"token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/characters/import/confirm-language", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ConfirmLanguageImport(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ConfirmLanguageImport status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	cards, err := db.ListCharacterCards(sqlDB, -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 {
		t.Fatalf("expected VO + VF, got %d cards: %+v", len(cards), cards)
	}
	// The default target language on a fresh install is English
	// ("en") — Lord's suffix rule is "VF" only when the TARGET is
	// French, the uppercased code otherwise, so the translated copy
	// here is tagged "(EN)", not "(VF)" (see vfSuffix's own doc
	// comment and TestVFSuffix for the French case specifically).
	var vo, vf *db.CharacterCard
	for i := range cards {
		switch cards[i].Name {
		case "Merlin Francais (VO)":
			vo = &cards[i]
		case "Merlin Francais (EN)":
			vf = &cards[i]
		}
	}
	if vo == nil {
		t.Fatalf("no VO card found among %+v", cards)
	}
	if vf == nil {
		t.Fatalf("no translated (EN) card found among %+v", cards)
	}
	if !strings.Contains(vo.Description, "sage sorcier") {
		t.Errorf("VO description must stay untouched, got %q", vo.Description)
	}
	if !strings.HasPrefix(vf.Description, "[EN] ") {
		t.Errorf("VF description must be the mock-translated text, got %q", vf.Description)
	}
	if vf.Description == vo.Description {
		t.Error("VF description must differ from VO — translation didn't happen")
	}
	if !strings.Contains(confirmBody, "Français") {
		t.Errorf("confirm page must have named the detected language, body: %s", confirmBody)
	}
	// task t_166318f0/E: AuthorNote must be translated on the VF
	// copy too — it ends up injected into the character's own system
	// prompt (substituteCardMacros, handlers_chat.go), so leaving it
	// French there would mix languages in that prompt.
	if !strings.Contains(vo.AuthorNote, "13B") {
		t.Errorf("VO author note must stay untouched, got %q", vo.AuthorNote)
	}
	if !strings.HasPrefix(vf.AuthorNote, "[EN] ") {
		t.Errorf("VF author note must be the mock-translated text, got %q", vf.AuthorNote)
	}
	if vf.AuthorNote == vo.AuthorNote {
		t.Error("VF author note must differ from VO — translation didn't happen")
	}
}

// TestConfirmLanguageImport_Character_FallsBackWithoutBackend proves
// the "no kind=llm backend configured" path never crashes and still
// imports the VO half alone, with a visible warning.
func TestConfirmLanguageImport_Character_FallsBackWithoutBackend(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	h := CharacterHandlers{DB: sqlDB, Cache: NewImportCache()}

	confirmBody := filePost(h.ImportCharacterJSON, "merlin.json", []byte(frenchCharacterJSON), nil).Body.String()
	token := extractToken(t, confirmBody)

	form := url.Values{"token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/characters/import/confirm-language", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ConfirmLanguageImport(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("fallback must render 200, not crash — got %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no translation-capable backend is configured") {
		t.Errorf("fallback page must explain no backend was configured, body: %s", w.Body.String())
	}

	cards, err := db.ListCharacterCards(sqlDB, -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("fallback must import VO alone, got %d cards: %+v", len(cards), cards)
	}
	if cards[0].Name != "Merlin Francais (VO)" {
		t.Errorf("got card name %q, want %q", cards[0].Name, "Merlin Francais (VO)")
	}
}

// TestConfirmLanguageImport_Character_FallsBackOnTranslationError
// covers the other half of the fallback contract: a backend IS
// configured, but the call itself fails (connection refused) — still
// VO-only, still no crash.
func TestConfirmLanguageImport_Character_FallsBackOnTranslationError(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if _, err := db.CreateBackendProfile(sqlDB, db.BackendProfile{Name: "Unreachable", Kind: "llm", BaseURL: "http://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	h := CharacterHandlers{DB: sqlDB, Cache: NewImportCache()}

	confirmBody := filePost(h.ImportCharacterJSON, "merlin.json", []byte(frenchCharacterJSON), nil).Body.String()
	token := extractToken(t, confirmBody)

	form := url.Values{"token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/characters/import/confirm-language", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ConfirmLanguageImport(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("fallback must render 200, not crash — got %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "failed") {
		t.Errorf("fallback page must explain the translation call failed, body: %s", w.Body.String())
	}
	cards, err := db.ListCharacterCards(sqlDB, -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].Name != "Merlin Francais (VO)" {
		t.Fatalf("fallback must import VO alone, got %+v", cards)
	}
}

// TestImportLorebookJSON_LanguageMismatchAndDualImport mirrors the
// Character coverage above for World's own bare-lorebook importer —
// Lord's ask covers both entities, not just cards.
func TestImportLorebookJSON_LanguageMismatchAndDualImport(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	srv := mockTranslationServer(t)
	if _, err := db.CreateBackendProfile(sqlDB, db.BackendProfile{Name: "Local", Kind: "llm", BaseURL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	h := WorldHandlers{DB: sqlDB, Cache: NewImportCache()}

	confirmW := filePost(h.ImportLorebookJSON, "book.json", []byte(frenchLorebookJSON), nil)
	if confirmW.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (confirm page), body: %s", confirmW.Code, confirmW.Body.String())
	}
	token := extractToken(t, confirmW.Body.String())

	if n, err := db.CountLorebooks(sqlDB); err != nil || n != 0 {
		t.Fatalf("nothing must be created before Confirm is clicked, got n=%d err=%v", n, err)
	}

	form := url.Values{"token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/world/import/confirm-language", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ConfirmLanguageImport(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ConfirmLanguageImport status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	books, err := db.ListLorebooks(sqlDB, -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 2 {
		t.Fatalf("expected VO + VF lorebooks, got %d: %+v", len(books), books)
	}
	var voID, vfID int64
	for _, b := range books {
		switch b.Name {
		case "book (VO)":
			voID = b.ID
		case "book (EN)":
			vfID = b.ID
		}
	}
	if voID == 0 || vfID == 0 {
		t.Fatalf("expected both 'book (VO)' and 'book (EN)' (target language is English by default — see vfSuffix), got %+v", books)
	}
	voEntries, err := db.ListLorebookEntries(sqlDB, voID)
	if err != nil {
		t.Fatal(err)
	}
	vfEntries, err := db.ListLorebookEntries(sqlDB, vfID)
	if err != nil {
		t.Fatal(err)
	}
	if len(voEntries) != 2 || len(vfEntries) != 2 {
		t.Fatalf("expected 2 entries on each side, got vo=%d vf=%d", len(voEntries), len(vfEntries))
	}
	for _, e := range vfEntries {
		if !strings.HasPrefix(e.Content, "[EN] ") {
			t.Errorf("VF entry content must be mock-translated, got %q", e.Content)
		}
	}
	// Keys must now be translated too (task t_166318f0/D, reversing
	// Part C's original call): a VF copy whose Keys stayed in the
	// source language would never trigger for a player typing in the
	// target language — see docs/DESIGN.md's superseded-reasoning
	// note. mockTranslationServer echoes "[EN] "+input regardless of
	// which system prompt sent it (translateText vs
	// translateKeywordText), so this proves the real per-key round
	// trip happened without needing a live LLM.
	for _, e := range vfEntries {
		for _, k := range e.Keys {
			if !strings.HasPrefix(k, "[EN] ") {
				t.Errorf("VF entry key %q must be mock-translated, not carried through byte-for-byte", k)
			}
		}
	}
	// And the matching mechanism itself must still work on a
	// translated-shaped key: real translateKeywordText output is a
	// bare word/short phrase (its own prompt explicitly forbids
	// brackets/preamble — see import_language.go), unlike this
	// mock's "[EN] "-prefixed marker (whose literal brackets are
	// themselves non-word characters, which would trip up \b's
	// boundary check in a way no real translated key ever would —
	// not what this is meant to verify). keysMatch is exercised
	// directly here with a plausible translated key to prove D's
	// change doesn't break whole-word triggering.
	if !keysMatch([]string{"tower"}, "I climb the tower now.") {
		t.Error("a translated key must still match as a whole word in a message containing it verbatim")
	}
	if keysMatch([]string{"tower"}, "I see no towering structure here.") {
		t.Error("a translated key must NOT match inside a longer word (word-boundary regression)")
	}
}

// TestLanguageDisplayName covers the "never a hardcoded language
// table" contract directly: an installed UI language's own i18n name
// wins, anything else falls back to the caller-supplied name (here,
// what whatlanggo itself would report).
func TestLanguageDisplayName(t *testing.T) {
	if got := languageDisplayName("fr", "French"); got != "Français" {
		t.Errorf("installed language must use its own i18n self-name, got %q", got)
	}
	if got := languageDisplayName("zh", "Mandarin"); got != "Mandarin" {
		t.Errorf("a language this install has no UI file for must fall back to the caller's name, got %q", got)
	}
	if got := languageDisplayName("zz", ""); got != "ZZ" {
		t.Errorf("an unknown code with no fallback name must at least uppercase the code, got %q", got)
	}
}

func TestVFSuffix(t *testing.T) {
	if got := vfSuffix("fr"); got != "VF" {
		t.Errorf("French must get the literal VF suffix Lord asked for, got %q", got)
	}
	if got := vfSuffix("de"); got != "DE" {
		t.Errorf("any other language must get its uppercased code, got %q", got)
	}
}
