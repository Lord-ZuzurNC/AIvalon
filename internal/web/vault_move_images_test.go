package web

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

func hexKey(key []byte) string { return hex.EncodeToString(key) }

// mustUploadCharacterImage/mustUploadLorebookImage drive the real
// saveImage path (plaintext — no vault involved) the way an actual
// "add image" multipart form post would, and return the resulting
// stored image_path.
func mustUploadCharacterImage(t *testing.T, h CharacterHandlers, characterID int64) string {
	t.Helper()
	png, _ := base64.StdEncoding.DecodeString(tinyPNGBase64)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("image", "avatar.png")
	fw.Write(png)
	mw.Close()
	r := httptest.NewRequest("POST", "/x", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	if err := h.saveImage(w, r, characterID); err != nil {
		t.Fatalf("saveImage: %v", err)
	}
	card, err := db.GetCharacterCard(h.DB, characterID)
	if err != nil {
		t.Fatal(err)
	}
	return card.ImagePath
}

func mustUploadLorebookImage(t *testing.T, h WorldHandlers, bookID int64) string {
	t.Helper()
	png, _ := base64.StdEncoding.DecodeString(tinyPNGBase64)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("image", "cover.png")
	fw.Write(png)
	mw.Close()
	r := httptest.NewRequest("POST", "/x", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	if err := h.saveImage(w, r, bookID); err != nil {
		t.Fatalf("saveImage: %v", err)
	}
	book, err := db.GetLorebook(h.DB, bookID)
	if err != nil {
		t.Fatal(err)
	}
	return book.ImagePath
}

// TestMoveInMoveOutEncryptDecryptCharacterImage: a character's avatar
// gets encrypted the moment it's hidden into the vault (when this
// browser is holding a key), and decrypted back to plaintext the
// moment it's pulled back out — Lord's "chiffrement complet" ask.
func TestMoveInMoveOutEncryptDecryptCharacterImage(t *testing.T) {
	f := newFixture(t)
	t.Chdir(t.TempDir())
	gate := NewVaultGate()
	key := bytes.Repeat([]byte{0x11}, vaultEncKeySize)
	unlockedWithKey := unlockedCookieWithKey(t, gate, key)

	charID, err := db.CreateCharacterCard(f.db, db.CharacterCard{Name: "Merlin2"})
	if err != nil {
		t.Fatal(err)
	}
	ch := CharacterHandlers{DB: f.db, Gate: gate}

	imagePath := mustUploadCharacterImage(t, ch, charID)
	if !strings.HasPrefix(imagePath, "uploads/characters/") {
		t.Fatalf("expected a plain path, got %q", imagePath)
	}
	plainDisk := filepath.Join("web", "static", stripQuery(imagePath))
	if _, err := os.Stat(plainDisk); err != nil {
		t.Fatalf("plaintext file missing: %v", err)
	}

	// MoveIn WITH a key available: encrypts immediately.
	vh := VaultHandlers{DB: f.db, Gate: gate}
	moveInReq := httptest.NewRequest("POST", "/x", nil)
	moveInReq.SetPathValue("kind", "characters")
	moveInReq.SetPathValue("id", strconv.FormatInt(charID, 10))
	moveInReq.AddCookie(unlockedWithKey)
	vh.MoveIn(httptest.NewRecorder(), moveInReq)

	card, err := db.GetCharacterCard(f.db, charID)
	if err != nil {
		t.Fatal(err)
	}
	if !card.IsVaulted {
		t.Fatal("character should be vaulted after MoveIn")
	}
	if !strings.HasPrefix(stripQuery(card.ImagePath), "uploads/vault/") {
		t.Fatalf("expected the image moved under uploads/vault/, got %q", card.ImagePath)
	}
	if _, err := os.Stat(plainDisk); err == nil {
		t.Fatal("the old plaintext file should have been removed after MoveIn encrypted it")
	}
	vaultDisk := filepath.Join("web", "static", stripQuery(card.ImagePath))
	raw, err := os.ReadFile(vaultDisk)
	if err != nil {
		t.Fatalf("encrypted file missing: %v", err)
	}
	if _, err := decryptVaultBytes(key, raw); err != nil {
		t.Fatalf("file under uploads/vault/ doesn't decrypt with the key MoveIn used: %v", err)
	}

	// MoveOut WITH the key: decrypts back to plaintext. Needs a real
	// PIN configured so MoveOut's own pin_hash check passes.
	if err := db.SetVaultPIN(f.db, "test-hash", "test-salt", 4); err != nil {
		t.Fatal(err)
	}
	stored, err := db.GetVaultPIN(f.db)
	if err != nil {
		t.Fatal(err)
	}
	moveOutReq := httptest.NewRequest("POST", "/x", strings.NewReader("pin_hash="+stored.PINHash+"&enc_key_hash="+hexKey(key)))
	moveOutReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	moveOutReq.SetPathValue("kind", "characters")
	moveOutReq.SetPathValue("id", strconv.FormatInt(charID, 10))
	moveOutReq.AddCookie(unlockedWithKey)
	vh.MoveOut(httptest.NewRecorder(), moveOutReq)

	card, err = db.GetCharacterCard(f.db, charID)
	if err != nil {
		t.Fatal(err)
	}
	if card.IsVaulted {
		t.Fatal("character should no longer be vaulted after MoveOut")
	}
	if strings.HasPrefix(stripQuery(card.ImagePath), "uploads/vault/") {
		t.Fatalf("expected the image moved back out of uploads/vault/, got %q", card.ImagePath)
	}
	backDisk := filepath.Join("web", "static", stripQuery(card.ImagePath))
	plain, err := os.ReadFile(backDisk)
	if err != nil {
		t.Fatalf("decrypted file missing: %v", err)
	}
	wantPNG, _ := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if !bytes.Equal(plain, wantPNG) {
		t.Fatal("decrypted-back file doesn't match the original PNG")
	}
}

// TestMoveInWithoutKeyThenReconcileOnUnlock is the safety net Lord's
// "no PIN to hide" MoveIn design needs: hiding something while this
// browser holds no decrypt key must still succeed (flag flips), and
// the image is picked up and encrypted the next time a REAL unlock
// happens (reconcileVaultImages), not left exposed forever.
func TestMoveInWithoutKeyThenReconcileOnUnlock(t *testing.T) {
	f := newFixture(t)
	t.Chdir(t.TempDir())
	gate := NewVaultGate()

	bookID, err := db.CreateLorebook(f.db, db.Lorebook{Name: "SecretRealm"})
	if err != nil {
		t.Fatal(err)
	}
	wh := WorldHandlers{DB: f.db, Gate: gate}
	imagePath := mustUploadLorebookImage(t, wh, bookID)
	plainDisk := filepath.Join("web", "static", stripQuery(imagePath))

	// MoveIn with NO unlock cookie at all (the documented "no PIN
	// needed going in" case).
	vh := VaultHandlers{DB: f.db, Gate: gate}
	moveInReq := httptest.NewRequest("POST", "/x", nil)
	moveInReq.SetPathValue("kind", "worlds")
	moveInReq.SetPathValue("id", strconv.FormatInt(bookID, 10))
	vh.MoveIn(httptest.NewRecorder(), moveInReq)

	book, err := db.GetLorebook(f.db, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if !book.IsVaulted {
		t.Fatal("lorebook should be vaulted even with no key available")
	}
	if book.ImagePath != imagePath {
		t.Fatalf("image should still be plaintext right after a key-less MoveIn, got %q", book.ImagePath)
	}
	if _, err := os.Stat(plainDisk); err != nil {
		t.Fatal("plaintext file should still exist — nothing to encrypt it with yet")
	}

	// Now a REAL unlock happens (with a key) — reconcileVaultImages
	// must catch this lorebook's still-plaintext cover.
	key := bytes.Repeat([]byte{0x22}, vaultEncKeySize)
	if err := reconcileVaultImages(f.db, key); err != nil {
		t.Fatal(err)
	}

	book, err = db.GetLorebook(f.db, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stripQuery(book.ImagePath), "uploads/vault/") {
		t.Fatalf("reconcile should have moved the image under uploads/vault/, got %q", book.ImagePath)
	}
	if _, err := os.Stat(plainDisk); err == nil {
		t.Fatal("reconcile should have removed the old plaintext file")
	}
	encDisk := filepath.Join("web", "static", stripQuery(book.ImagePath))
	raw, err := os.ReadFile(encDisk)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decryptVaultBytes(key, raw); err != nil {
		t.Fatalf("reconciled file doesn't decrypt with the key reconcile used: %v", err)
	}
}

// TestMoveInMoveOutSessionGeneratedImage is moveSessionImages' own
// round trip — a chat's generated image, not an avatar.
func TestMoveInMoveOutSessionGeneratedImage(t *testing.T) {
	f := newFixture(t)
	t.Chdir(t.TempDir())
	gate := NewVaultGate()
	key := bytes.Repeat([]byte{0x33}, vaultEncKeySize)
	unlockedWithKey := unlockedCookieWithKey(t, gate, key)

	png, _ := base64.StdEncoding.DecodeString(tinyPNGBase64)
	path, err := saveImageBytes(png, "generated", 999)
	if err != nil {
		t.Fatal(err)
	}
	msgID, err := db.CreateMessage(f.db, db.ChatMessage{SessionID: f.session, Role: "image", Content: "a castle"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetMessageImagePath(f.db, msgID, path); err != nil {
		t.Fatal(err)
	}

	vh := VaultHandlers{DB: f.db, Gate: gate}
	moveInReq := httptest.NewRequest("POST", "/x", nil)
	moveInReq.SetPathValue("kind", "chats")
	moveInReq.SetPathValue("id", strconv.FormatInt(f.session, 10))
	moveInReq.AddCookie(unlockedWithKey)
	vh.MoveIn(httptest.NewRecorder(), moveInReq)

	messages, err := db.ListMessages(f.db, f.session)
	if err != nil {
		t.Fatal(err)
	}
	var newPath string
	for _, m := range messages {
		if m.ID == msgID {
			newPath = m.ImagePath
		}
	}
	if !strings.HasPrefix(stripQuery(newPath), "uploads/vault/") {
		t.Fatalf("expected the generated image moved under uploads/vault/, got %q", newPath)
	}
	if _, err := os.ReadFile(filepath.Join("web", "static", stripQuery(path))); err == nil {
		t.Fatal("old plaintext generated-image file should be gone")
	}
}
