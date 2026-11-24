package web

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// TestVaultedGenerateImageEncryptsAtRest is the actual complaint:
// "si on va dans static/uploads on voit tout" — a vaulted session's
// generated image must land on disk as ciphertext, not a real PNG,
// and must only be viewable through /vault/image/ with the right
// unlock + key.
func TestVaultedGenerateImageEncryptsAtRest(t *testing.T) {
	f := newFixture(t)
	t.Chdir(t.TempDir())
	srv, _ := mockA1111(t, tinyPNGBase64)
	profileID, _ := db.CreateBackendProfile(f.db, db.BackendProfile{Name: "sd", Kind: "image", BaseURL: srv.URL})
	if _, err := f.db.Exec(`UPDATE sessions SET is_vaulted = 1 WHERE id = ?`, f.session); err != nil {
		t.Fatal(err)
	}

	gate := NewVaultGate()
	key := bytes.Repeat([]byte{0x42}, vaultEncKeySize)
	cookie := unlockedCookieWithKey(t, gate, key)
	h := ChatHandlers{DB: f.db, Gate: gate}

	// Generate, with the unlock cookie attached (GenerateImage checks
	// h.Gate.Unlocked for a vaulted session before anything else).
	form := imageForm(profileID)
	req := httptest.NewRequest("POST", "/x", bytes.NewBufferString(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.SetPathValue("id", strconv.FormatInt(f.session, 10))
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.GenerateImage(w, req)
	if w.Header().Get("HX-Redirect") == "" {
		t.Fatalf("generation failed: %s", w.Body.String())
	}

	// The file on disk must be ciphertext: decoding it as the PNG we
	// know it should otherwise be must fail. Look up the real path
	// rather than assuming a message ID — newFixture already seeds
	// other messages for this session.
	var relPath string
	rows, err := f.db.Query(`SELECT image_path FROM messages WHERE session_id = ? AND role = 'image' ORDER BY id DESC LIMIT 1`, f.session)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		if err := rows.Scan(&relPath); err != nil {
			t.Fatal(err)
		}
	}
	rows.Close()
	if relPath == "" {
		t.Fatal("no image message found after generation")
	}
	if i := strings.IndexByte(relPath, '?'); i != -1 {
		relPath = relPath[:i]
	}
	if !strings.HasPrefix(relPath, "uploads/vault/") {
		t.Fatalf("image_path %q isn't under uploads/vault/ — vaulted generation didn't encrypt", relPath)
	}
	withinVault := strings.TrimPrefix(relPath, "uploads/vault/")

	wantPNG, _ := base64.StdEncoding.DecodeString(tinyPNGBase64)
	diskPath := filepath.Join("web", "static", "uploads", "vault", filepath.FromSlash(withinVault))
	onDisk, err := os.ReadFile(diskPath)
	if err != nil {
		t.Fatalf("expected the encrypted file at %s: %v", diskPath, err)
	}
	if bytes.Equal(onDisk, wantPNG) {
		t.Fatal("the file on disk is the raw PNG — not encrypted at all")
	}

	// Decrypting it by hand with the same key must recover the PNG.
	got, err := decryptVaultBytes(key, onDisk)
	if err != nil || !bytes.Equal(got, wantPNG) {
		t.Fatalf("decryptVaultBytes: got=%v err=%v", got != nil, err)
	}

	// Serving it through /vault/image/ with the right cookie decrypts
	// it on the fly.
	vh := VaultHandlers{DB: f.db, Gate: gate}
	imgReq := httptest.NewRequest("GET", "/vault/image/"+withinVault, nil)
	imgReq.SetPathValue("path", withinVault)
	imgReq.AddCookie(cookie)
	imgW := httptest.NewRecorder()
	vh.Image(imgW, imgReq)
	if imgW.Code != http.StatusOK || !bytes.Equal(imgW.Body.Bytes(), wantPNG) {
		t.Fatalf("GET /vault/image/: code=%d matches=%v", imgW.Code, bytes.Equal(imgW.Body.Bytes(), wantPNG))
	}

	// Without the cookie at all: refused, not silently served.
	noAuthReq := httptest.NewRequest("GET", "/vault/image/"+withinVault, nil)
	noAuthReq.SetPathValue("path", withinVault)
	noAuthW := httptest.NewRecorder()
	vh.Image(noAuthW, noAuthReq)
	if noAuthW.Code == http.StatusOK {
		t.Fatal("served a vaulted image with no unlock cookie at all")
	}

	// Unlocked, but with a DIFFERENT (or missing) key — e.g. the
	// Setup/PIN-change path's grantUnlock(w, nil): refused, not a
	// corrupted image.
	noKeyCookie := unlockedCookie(t, gate) // nil key
	noKeyReq := httptest.NewRequest("GET", "/vault/image/"+withinVault, nil)
	noKeyReq.SetPathValue("path", withinVault)
	noKeyReq.AddCookie(noKeyCookie)
	noKeyW := httptest.NewRecorder()
	vh.Image(noKeyW, noKeyReq)
	if noKeyW.Code == http.StatusOK {
		t.Fatal("served a vaulted image to an unlock token with no decrypt key")
	}
}

// TestVaultImagePathTraversalRejected: the {path...} wildcard route
// takes raw user input straight from the URL — must not let "../.."
// read anything outside uploads/vault/.
func TestVaultImagePathTraversalRejected(t *testing.T) {
	f := newFixture(t)
	t.Chdir(t.TempDir())
	gate := NewVaultGate()
	cookie := unlockedCookieWithKey(t, gate, bytes.Repeat([]byte{1}, vaultEncKeySize))
	vh := VaultHandlers{DB: f.db, Gate: gate}

	for _, p := range []string{"../../../../etc/passwd", "..%2f..%2fsecret", "foo/../../bar"} {
		req := httptest.NewRequest("GET", "/vault/image/"+p, nil)
		req.SetPathValue("path", p)
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		vh.Image(w, req)
		if w.Code == http.StatusOK {
			t.Fatalf("path %q was not rejected", p)
		}
	}
}
