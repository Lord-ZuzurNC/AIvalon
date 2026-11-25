package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// batchFile is one entry for multiFilePost — filename + raw body,
// grouped together so a test can describe a whole batch upload in
// one literal slice.
type batchFile struct {
	filename string
	body     []byte
}

// multiFilePost is filePost's multi-file counterpart: every entry in
// files is written under the SAME "file" field name, exactly what
// ImportModal's <input type="file" name="file" multiple> submits
// from a real browser — this is the one thing filePost (vault_import_
// test.go) can't do, since it only ever writes one "file" part.
func multiFilePost(h http.HandlerFunc, files []batchFile, extraFields map[string]string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range extraFields {
		mw.WriteField(k, v)
	}
	for _, f := range files {
		fw, _ := mw.CreateFormFile("file", f.filename)
		fw.Write(f.body)
	}
	mw.Close()
	r := httptest.NewRequest("POST", "/x", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

// TestImportCharacterBatch_MultipleValidFiles covers the base case:
// N valid Character Card uploads in one request, none of them
// triggering a language mismatch (all English, matching the fresh
// install's default target), must all land in one pass with a report
// naming every one of them.
func TestImportCharacterBatch_MultipleValidFiles(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	h := CharacterHandlers{DB: sqlDB, Cache: NewImportCache()}

	files := []batchFile{
		{"arthur.json", []byte(`{"data":{"name":"Arthur Batch"}}`)},
		{"lancelot.json", []byte(`{"data":{"name":"Lancelot Batch"}}`)},
		{"guinevere.json", []byte(`{"data":{"name":"Guinevere Batch"}}`)},
	}
	w := multiFilePost(h.ImportCharacterJSON, files, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (batch report), body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, name := range []string{"Arthur Batch", "Lancelot Batch", "Guinevere Batch"} {
		if !strings.Contains(body, name) {
			t.Errorf("batch report missing %q, body:\n%s", name, body)
		}
	}
	if n, err := db.CountCharacterCards(sqlDB); err != nil || n != 3 {
		t.Fatalf("expected all 3 to be created, got n=%d err=%v", n, err)
	}
}

// TestImportCharacterBatch_OnePartialFailureDoesNotAbortTheRest is
// the specific contract Lord asked for: a batch with one broken file
// in the middle must still import every other valid file, and the
// report must name the broken one and why.
func TestImportCharacterBatch_OnePartialFailureDoesNotAbortTheRest(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	h := CharacterHandlers{DB: sqlDB, Cache: NewImportCache()}

	files := []batchFile{
		{"arthur.json", []byte(`{"data":{"name":"Arthur Mixed"}}`)},
		{"broken.json", []byte(`not even json`)},
		{"lancelot.json", []byte(`{"data":{"name":"Lancelot Mixed"}}`)},
	}
	w := multiFilePost(h.ImportCharacterJSON, files, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (batch report), body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Arthur Mixed") || !strings.Contains(body, "Lancelot Mixed") {
		t.Errorf("both valid files must still be reported as imported, body:\n%s", body)
	}
	if !strings.Contains(body, "broken.json") {
		t.Errorf("the broken file must be named in the report, body:\n%s", body)
	}
	if n, err := db.CountCharacterCards(sqlDB); err != nil || n != 2 {
		t.Fatalf("expected the 2 valid files to still land despite the broken one, got n=%d err=%v", n, err)
	}
}

// TestImportCharacterBatch_SomeFilesTriggerLanguageMismatch is the
// third required case: within one batch, a subset of files trigger
// the language-mismatch gate (see internal/langdetect) and must land
// on ImportBatchLanguageConfirmPage, while the rest import
// immediately in the same pass. Confirming must then create VO+VF
// for the pending ones AND still report the already-done ones.
func TestImportCharacterBatch_SomeFilesTriggerLanguageMismatch(t *testing.T) {
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

	files := []batchFile{
		{"arthur.json", []byte(`{"data":{"name":"Arthur Plain Batch"}}`)},
		{"merlin.json", []byte(frenchCharacterJSON)},
	}
	w := multiFilePost(h.ImportCharacterJSON, files, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (batch confirm page), body: %s", w.Code, w.Body.String())
	}
	confirmBody := w.Body.String()
	if !strings.Contains(confirmBody, "Arthur Plain Batch") {
		t.Errorf("already-imported file must still be named on the pending-confirmation screen, body:\n%s", confirmBody)
	}
	if !strings.Contains(confirmBody, "Merlin Francais") {
		t.Errorf("pending file's own name must appear on the confirm screen, body:\n%s", confirmBody)
	}
	if n, err := db.CountCharacterCards(sqlDB); err != nil || n != 1 {
		t.Fatalf("exactly the non-mismatched file must already be created at this point, got n=%d err=%v", n, err)
	}

	tokenRe := regexp.MustCompile(`name="tokens" value="([^"]+)"`)
	m := tokenRe.FindStringSubmatch(confirmBody)
	if m == nil {
		t.Fatalf("no pending token found in confirm page:\n%s", confirmBody)
	}
	alreadyDoneRe := regexp.MustCompile(`name="already_done" value="([^"]+)"`)
	am := alreadyDoneRe.FindStringSubmatch(confirmBody)
	if am == nil {
		t.Fatalf("no already_done field found in confirm page:\n%s", confirmBody)
	}

	form := url.Values{"tokens": {m[1]}, "already_done": {am[1]}, "action": {"confirm"}}
	req := httptest.NewRequest(http.MethodPost, "/characters/import/confirm-language-batch", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()
	h.ConfirmLanguageImportBatch(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("ConfirmLanguageImportBatch status = %d, want 200, body: %s", rw.Code, rw.Body.String())
	}
	finalBody := rw.Body.String()
	if !strings.Contains(finalBody, "Arthur Plain Batch") {
		t.Errorf("final report must still mention the already-done file, body:\n%s", finalBody)
	}

	cards, err := db.ListCharacterCards(sqlDB, -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Arthur (plain) + Merlin VO + Merlin VF (translated) = 3.
	if len(cards) != 3 {
		t.Fatalf("expected 3 cards total (1 plain + VO + VF), got %d: %+v", len(cards), cards)
	}
}

// TestImportLorebookBatch_MultipleValidFiles mirrors the Character
// coverage above for World's bare-lorebook batch importer — Lord's
// ask covers both entities, not just cards (see docs/DESIGN.md).
func TestImportLorebookBatch_MultipleValidFiles(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	h := WorldHandlers{DB: sqlDB, Cache: NewImportCache()}

	silBook := func(key string) []byte {
		return []byte(`{"entries":{"0":{"key":["` + key + `"],"content":"A hidden isle.","order":0}}}`)
	}
	files := []batchFile{
		{"avalon.json", silBook("avalon")},
		{"camelot.json", silBook("camelot")},
	}
	w := multiFilePost(h.ImportLorebookJSON, files, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (batch report), body: %s", w.Code, w.Body.String())
	}
	if n, err := db.CountLorebooks(sqlDB); err != nil || n != 2 {
		t.Fatalf("expected both lorebooks to be created, got n=%d err=%v", n, err)
	}
}
