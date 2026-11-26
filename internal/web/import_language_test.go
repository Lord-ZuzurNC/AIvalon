package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

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

// mockInfiniteTranslationServer is the test harness for task
// t_3a415d4d: Lord's real llama.cpp logs showed a translation call
// still generating at 1878+ tokens with no sign of stopping on its
// own (an "abliterated" fine-tune not reliably honoring the
// system prompt's "stay short" instruction) until translateFieldTimeout
// (45s) cut the HTTP connection — by then ~2000 tokens had already
// streamed. This mock streams synthetic tokens and stops EXACTLY
// when the request body's own "max_tokens" says to (mirroring how a
// real OpenAI-compatible backend, llama.cpp's server included, honors
// the parameter) — if the request never sent max_tokens at all, it
// stops only after a 5000-token safety valve, which would blow well
// past any sane test timeout and so reliably fails a regression where
// the cap stopped being sent. sawMaxTokens (optional) captures the
// value the request actually carried, for assertions.
func mockInfiniteTranslationServer(t *testing.T, sawMaxTokens *float64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		limit := 5000
		if mt, ok := body["max_tokens"].(float64); ok {
			if sawMaxTokens != nil {
				*sawMaxTokens = mt
			}
			limit = int(mt)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for i := 0; i < limit; i++ {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"tok \"}}]}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTranslateText_SendsMaxTokensCap(t *testing.T) {
	var sawMaxTokens float64
	srv := mockInfiniteTranslationServer(t, &sawMaxTokens)
	profile := db.BackendProfile{Kind: "llm", BaseURL: srv.URL}

	start := time.Now()
	out, err := translateText(context.Background(), profile, "hello", "French")
	if err != nil {
		t.Fatalf("translateText: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("translateText took %v — the mock's 5000-token safety valve suggests max_tokens wasn't sent at all", elapsed)
	}
	if sawMaxTokens != translateTextMaxTokens {
		t.Errorf("request body max_tokens = %v, want %v", sawMaxTokens, float64(translateTextMaxTokens))
	}
	if out == "" {
		t.Error("expected non-empty translated output")
	}
}

func TestTranslateKeywordText_SendsLowerMaxTokensCap(t *testing.T) {
	var sawMaxTokens float64
	srv := mockInfiniteTranslationServer(t, &sawMaxTokens)
	profile := db.BackendProfile{Kind: "llm", BaseURL: srv.URL}

	if _, err := translateKeywordText(context.Background(), profile, "tower", "French"); err != nil {
		t.Fatalf("translateKeywordText: %v", err)
	}
	if sawMaxTokens != translateKeywordMaxTokens {
		t.Errorf("request body max_tokens = %v, want %v", sawMaxTokens, float64(translateKeywordMaxTokens))
	}
}

// TestTranslateText_MaxTokensCapOverridesExistingProfileSetting proves
// withMaxTokens both overrides a chat-configured max_tokens for THIS
// call and leaves the original BackendProfile (and its ModelParams
// map) untouched — Lord's own chat usage of the same profile must
// keep whatever max_tokens setting it already had.
func TestTranslateText_MaxTokensCapOverridesExistingProfileSetting(t *testing.T) {
	var sawMaxTokens float64
	srv := mockInfiniteTranslationServer(t, &sawMaxTokens)
	profile := db.BackendProfile{
		Kind:        "llm",
		BaseURL:     srv.URL,
		ModelParams: map[string]float64{"temperature": 1, "max_tokens": 4096},
	}

	if _, err := translateText(context.Background(), profile, "hello", "French"); err != nil {
		t.Fatalf("translateText: %v", err)
	}
	if sawMaxTokens != translateTextMaxTokens {
		t.Errorf("translation call must override the profile's own max_tokens, got %v want %v", sawMaxTokens, float64(translateTextMaxTokens))
	}
	if profile.ModelParams["max_tokens"] != 4096 {
		t.Error("the original profile's ModelParams must stay untouched — chat usage of the same profile must keep its own setting")
	}
}

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

// TestConfirmLanguageImport_Character_FallsBackOnTranslationTimeout
// reproduces the EXACT scenario behind task t_3a415d4d, not just a
// connection-refused error (TestConfirmLanguageImport_Character_FallsBackOnTranslationError
// above already covers that one): a backend that accepts the
// connection and simply never answers, so the ONLY thing that ever
// ends the call is translateFieldTimeout's own ctx deadline — a real
// context.DeadlineExceeded, the same failure mode a genuinely stuck
// generation produces. translateFieldTimeout is shrunk for the
// duration of this test (saved/restored) so it doesn't take 45 real
// seconds; the assertion is the same as the other fallback tests —
// land on the 200 OK result page in VO-only mode, never a 500.
func TestConfirmLanguageImport_Character_FallsBackOnTranslationTimeout(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Never send any token or [DONE] — wait for the client's own
		// ctx to time out and cancel the request (mirrors
		// llmclient_test.go's TestStreamChatCancellationAbortsRequest),
		// with a 5s safety valve in case something regresses and the
		// cancellation never arrives.
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()

	orig := translateFieldTimeout
	translateFieldTimeout = 200 * time.Millisecond
	t.Cleanup(func() { translateFieldTimeout = orig })

	if _, err := db.CreateBackendProfile(sqlDB, db.BackendProfile{Name: "Hangs", Kind: "llm", BaseURL: srv.URL}); err != nil {
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
		t.Fatalf("a translation timeout must still land on the result page, not a 500 — got %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "failed") {
		t.Errorf("fallback page must explain the translation call failed, body: %s", w.Body.String())
	}
	cards, err := db.ListCharacterCards(sqlDB, -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].Name != "Merlin Francais (VO)" {
		t.Fatalf("a timed-out translation must fall back to VO-only, got %+v", cards)
	}
}

// TestConfirmLanguageImport_Character_SecondImportGetsNumberedSuffix
// covers task t_3a415d4d's dedup check directly — Lord's own repro
// was re-importing the exact same card (veyra.png) and expecting
// "(2)" suffixes rather than a crash or a silent overwrite. Both
// passes go through the full upload→confirm→ConfirmLanguageImport
// round trip (not a direct uniqueName unit test), so this exercises
// the real "(VO)"/"(EN)" -> "(VO) (2)"/"(EN) (2)" naming exactly as a
// browser would trigger it.
func TestConfirmLanguageImport_Character_SecondImportGetsNumberedSuffix(t *testing.T) {
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

	importOnce := func() {
		body := filePost(h.ImportCharacterJSON, "merlin.json", []byte(frenchCharacterJSON), nil).Body.String()
		token := extractToken(t, body)
		form := url.Values{"token": {token}}
		req := httptest.NewRequest(http.MethodPost, "/characters/import/confirm-language", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ConfirmLanguageImport(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("ConfirmLanguageImport status = %d, body: %s", w.Code, w.Body.String())
		}
	}
	importOnce() // "Merlin Francais (VO)" + "Merlin Francais (EN)"
	importOnce() // same card again — must dedup via uniqueName, not overwrite or error

	cards, err := db.ListCharacterCards(sqlDB, -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 4 {
		t.Fatalf("expected 4 cards (VO+VF x 2 imports), got %d: %+v", len(cards), cards)
	}
	want := map[string]bool{
		"Merlin Francais (VO)":     false,
		"Merlin Francais (EN)":     false,
		"Merlin Francais (VO) (2)": false,
		"Merlin Francais (EN) (2)": false,
	}
	var gotNames []string
	for _, c := range cards {
		gotNames = append(gotNames, c.Name)
		if _, ok := want[c.Name]; ok {
			want[c.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected a card named %q after re-importing the same card twice, got names: %v", name, gotNames)
		}
	}
}

// TestTranslateFieldTimeoutFromEnv covers task t_45a55587 point 2:
// AIVALON_TRANSLATE_TIMEOUT_SECONDS lets Lord raise/lower
// translateFieldTimeout without recompiling. Every "bad input" case
// must fall back to translateFieldTimeoutDefault rather than panic or
// produce a zero/negative duration that would make every translation
// call fail instantly.
func TestTranslateFieldTimeoutFromEnv(t *testing.T) {
	orig, hadOrig := os.LookupEnv(translateFieldTimeoutEnvVar)
	t.Cleanup(func() {
		if hadOrig {
			os.Setenv(translateFieldTimeoutEnvVar, orig)
		} else {
			os.Unsetenv(translateFieldTimeoutEnvVar)
		}
	})

	cases := []struct {
		name string
		val  string
		want time.Duration
	}{
		{"unset falls back to the default", "", translateFieldTimeoutDefault},
		{"a valid override wins", "90", 90 * time.Second},
		{"non-numeric falls back to the default", "banana", translateFieldTimeoutDefault},
		{"zero falls back to the default", "0", translateFieldTimeoutDefault},
		{"negative falls back to the default", "-5", translateFieldTimeoutDefault},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.val == "" {
				os.Unsetenv(translateFieldTimeoutEnvVar)
			} else {
				os.Setenv(translateFieldTimeoutEnvVar, c.val)
			}
			if got := translateFieldTimeoutFromEnv(); got != c.want {
				t.Errorf("%s=%q: got %s, want %s", translateFieldTimeoutEnvVar, c.val, got, c.want)
			}
		})
	}
}

// TestTranslateCardFields_RealisticTimeoutSurvivesASlowButFiniteBackend
// is task t_45a55587's own required proof: Lord's real llama.cpp log
// showed a translation call that was SLOW but HEALTHY (still
// generating at 44 tok/s, 1878 tokens in) when the old 45s
// translateFieldTimeout cut the connection — not a stuck model. This
// test reproduces that exact shape with a mock backend that is slow
// but DOES finish, and proves the fix has a real effect by flipping
// the SAME backend/delay from failure to success purely by changing
// translateFieldTimeout — not by swapping in a different mock or
// skipping the timeout path. Durations are scaled down to
// milliseconds (what matters is the delay-vs-timeout ratio, the same
// ratio that separates Lord's real 45s/200s numbers, not absolute
// wall-clock time) so the test runs instantly instead of for real
// minutes.
func TestTranslateCardFields_RealisticTimeoutSurvivesASlowButFiniteBackend(t *testing.T) {
	const delay = 120 * time.Millisecond
	mkSlowServer := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(delay) // the backend is slow, never stuck — it always answers eventually.
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Bonjour\"}}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
	}
	card := db.CharacterCard{Description: "Hello there"}

	orig := translateFieldTimeout
	t.Cleanup(func() { translateFieldTimeout = orig })

	// 1. Reproduce the bug: a timeout shorter than the backend's own
	//    (finite, healthy) response time fails — exactly what the old
	//    45s did against Lord's real multi-minute local inference.
	srvShort := mkSlowServer()
	defer srvShort.Close()
	translateFieldTimeout = delay / 2
	profileShort := db.BackendProfile{Kind: "llm", BaseURL: srvShort.URL}
	if _, ok := translateCardFields(context.Background(), profileShort, card, "French"); ok {
		t.Fatal("a timeout shorter than the backend's own response time must fail, same as the original 45s bug — got ok=true")
	}

	// 2. Prove the fix: a timeout longer than the backend's response
	//    time succeeds against the EXACT SAME delay — the only
	//    variable that changed between the two calls is
	//    translateFieldTimeout itself, same as raising 45s to 200s did
	//    for real.
	srvLong := mkSlowServer()
	defer srvLong.Close()
	translateFieldTimeout = delay * 4
	profileLong := db.BackendProfile{Kind: "llm", BaseURL: srvLong.URL}
	translated, ok := translateCardFields(context.Background(), profileLong, card, "French")
	if !ok {
		t.Fatal("a timeout comfortably longer than the backend's response time must succeed")
	}
	if translated.Description != "Bonjour" {
		t.Errorf("translated.Description = %q, want %q", translated.Description, "Bonjour")
	}
}
