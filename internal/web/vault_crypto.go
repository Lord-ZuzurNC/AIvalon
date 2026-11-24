package web

// vault_crypto.go: at-rest encryption for files that belong to the
// vault. Lord's finding: "si on va dans static/uploads on voit tout,
// il n'y a pas trop d'intérêt d'avoir un vault" — the PIN gated the
// app's own UI, but a file on disk was always plaintext, so a casual
// look in the filesystem defeated the whole feature. Convention: any
// file under web/static/uploads/vault/ is ciphertext; anything else
// is plaintext. That's a path convention, not a DB flag — whether a
// file needs decrypting is a pure function of where it lives.
//
// Scope actually wired tonight: chat-generated images for a vaulted
// session (saveVaultImageBytes in image_upload.go, served through
// VaultHandlers.Image below). Character/lorebook/persona avatars
// moved into the vault via /vault/{kind}/{id}/in, and migrating
// images that were already sitting there in the clear before this
// existed, are NOT wired yet — see the handoff notes for the exact
// next steps.
//
// Key: AES-256-GCM. The 32-byte key is never derived or stored
// server-side from the PIN alone — it only ever exists in memory,
// for the lifetime of an unlock token, because the browser computed
// it client-side (vault.templ's vaultPinHashScript, domain-separated
// from the PIN's *auth* hash — see hashClientSide's comment in
// vault_gate.go) and handed it over at /vault/unlock. Losing the
// cookie/restarting the server loses the key; the file on disk is
// still there, still ciphertext, recoverable with the PIN alone (see
// the standalone decrypt script Lord was given for exactly that case).
//
// File format: 12-byte random nonce, then AES-GCM(key, nonce,
// plaintext) — nonce prepended since GCM needs it again to decrypt
// and it isn't secret, just must never repeat for the same key (a
// fresh crypto/rand nonce per file makes that practically certain).

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// vaultEncKeySize is AES-256's key size and also exactly what
// sha256 produces — the client sends the hex of a sha256 digest, so
// decoding it is the whole "derivation".
const vaultEncKeySize = 32

// vaultNonceSize is AES-GCM's standard nonce size (12 bytes).
const vaultNonceSize = 12

// decodeVaultEncKeyHex turns the hex string the unlock form submitted
// into the raw key bytes, rejecting anything that isn't exactly a
// sha256 digest's worth of hex — a short/malformed value here would
// otherwise silently become a weak (or empty) AES key.
func decodeVaultEncKeyHex(hexKey string) ([]byte, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decoding enc_key_hash: %w", err)
	}
	if len(key) != vaultEncKeySize {
		return nil, fmt.Errorf("enc_key_hash must decode to %d bytes, got %d", vaultEncKeySize, len(key))
	}
	return key, nil
}

// encryptVaultBytes encrypts plaintext for at-rest storage under
// web/static/uploads/vault/. Returns nonce||ciphertext||tag.
func encryptVaultBytes(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("building cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("building GCM: %w", err)
	}
	nonce := make([]byte, vaultNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decryptVaultBytes reverses encryptVaultBytes. A wrong key or
// corrupted/truncated data both surface as the same generic failure
// (AES-GCM's tag check doesn't distinguish them, and shouldn't —
// confirming "wrong key" vs "corrupt file" to an attacker is its own
// leak).
func decryptVaultBytes(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("building cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("building GCM: %w", err)
	}
	if len(data) < vaultNonceSize {
		return nil, errors.New("vault file too short to contain a nonce")
	}
	nonce, ciphertext := data[:vaultNonceSize], data[vaultNonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("decryption failed — wrong PIN/key, or the file is corrupted")
	}
	return plaintext, nil
}

// imageSrc is what every template must call instead of writing
// "/static/"+path by hand for anything that might be vaulted: a
// vault-encrypted path (see the uploads/vault/ convention above)
// resolves through the decrypting "/vault/image/" route; everything
// else still goes straight to the plain static file server.
func imageSrc(relPath string) string {
	if clean, ok := strings.CutPrefix(relPath, "uploads/vault/"); ok {
		return "/vault/image/" + clean
	}
	return "/static/" + relPath
}

// saveImageForEntity picks the plaintext or vault-encrypted writer
// depending on vaulted, using whatever decrypt/encrypt key this
// request's unlock token is holding (see VaultGate.EncKey). vaulted
// but no key available (Setup-only unlock, no real /vault/unlock
// yet): falls back to plaintext rather than failing the upload — a
// real gap, but a narrow and self-healing one: reconcileVaultImages
// runs on every real unlock and catches anything a vaulted entity's
// image left in the clear, whatever the reason.
func saveImageForEntity(gate *VaultGate, r *http.Request, vaulted bool, raw []byte, subdir string, entityID int64) (string, error) {
	if vaulted {
		if key, ok := gate.EncKey(r); ok {
			return saveVaultImageBytes(key, raw, subdir, entityID)
		}
	}
	return saveImageBytes(raw, subdir, entityID)
}

// Image is the decrypting counterpart to the plain static file
// server, for anything saved under web/static/uploads/vault/ (see
// imageSrc). Requires the same unlock as any other vaulted content,
// AND an actual decrypt key bound to that unlock token — Setup's
// PIN-change path grants an unlock without one (see handlers_vault.go),
// so that combination is a real, user-facing case, not just a
// theoretical gap.
func (h VaultHandlers) Image(w http.ResponseWriter, r *http.Request) {
	if !h.Gate.Unlocked(r) {
		http.Error(w, "locked", http.StatusForbidden)
		return
	}
	encKey, ok := h.Gate.EncKey(r)
	if !ok {
		http.Error(w, "vault unlocked, but this browser has no decrypt key yet — close the vault and unlock it again", http.StatusForbidden)
		return
	}
	rel := r.PathValue("path")
	// Collapse any ".."/"."/double-slash before trusting it as a
	// filesystem path — rel comes straight from the URL.
	cleaned := filepath.ToSlash(filepath.Clean("/" + rel))[1:]
	if cleaned == "" || strings.Contains(cleaned, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	diskPath := filepath.Join("web", "static", "uploads", "vault", filepath.FromSlash(cleaned))
	ciphertext, err := os.ReadFile(diskPath)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	plaintext, err := decryptVaultBytes(encKey, ciphertext)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(plaintext))
	// Never let the browser (or an intermediary) cache the decrypted
	// bytes to its own disk cache — that would quietly recreate the
	// exact plaintext-on-disk problem this feature exists to close.
	w.Header().Set("Cache-Control", "no-store")
	w.Write(plaintext)
}
