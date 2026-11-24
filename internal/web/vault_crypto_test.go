package web

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptVaultBytesRoundTrip(t *testing.T) {
	key := make([]byte, vaultEncKeySize)
	for i := range key {
		key[i] = byte(i)
	}
	plaintext := []byte("a secret generated image, pretend this is PNG bytes")

	ciphertext, err := encryptVaultBytes(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains(ciphertext, plaintext) {
		t.Fatal("ciphertext still contains the plaintext verbatim")
	}
	got, err := decryptVaultBytes(key, ciphertext)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("got %q, want %q", got, plaintext)
	}
}

func TestDecryptVaultBytesWrongKeyFails(t *testing.T) {
	key1 := bytes.Repeat([]byte{1}, vaultEncKeySize)
	key2 := bytes.Repeat([]byte{2}, vaultEncKeySize)
	ciphertext, err := encryptVaultBytes(key1, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decryptVaultBytes(key2, ciphertext); err == nil {
		t.Fatal("expected decryption with the wrong key to fail")
	}
}

func TestDecryptVaultBytesCorruptedFails(t *testing.T) {
	key := bytes.Repeat([]byte{7}, vaultEncKeySize)
	ciphertext, err := encryptVaultBytes(key, []byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext[len(ciphertext)-1] ^= 0xFF // flip a bit in the tag/ciphertext tail
	if _, err := decryptVaultBytes(key, ciphertext); err == nil {
		t.Fatal("expected decryption of corrupted data to fail")
	}
}

func TestEncryptVaultBytesNonceNeverRepeats(t *testing.T) {
	key := bytes.Repeat([]byte{9}, vaultEncKeySize)
	a, err := encryptVaultBytes(key, []byte("same plaintext"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := encryptVaultBytes(key, []byte("same plaintext"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a[:vaultNonceSize], b[:vaultNonceSize]) {
		t.Fatal("two encryptions reused the same nonce")
	}
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of the same plaintext produced identical ciphertext")
	}
}

func TestDecodeVaultEncKeyHex(t *testing.T) {
	t.Run("a real sha256 hex digest decodes to 32 bytes", func(t *testing.T) {
		// sha256("") hex-encoded — any valid 64-hex-char digest works.
		hexKey := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"[:64]
		key, err := decodeVaultEncKeyHex(hexKey)
		if err != nil || len(key) != vaultEncKeySize {
			t.Fatalf("key=%x err=%v", key, err)
		}
	})
	t.Run("wrong length is rejected", func(t *testing.T) {
		if _, err := decodeVaultEncKeyHex("abcd"); err == nil {
			t.Fatal("expected an error for a too-short key")
		}
	})
	t.Run("non-hex is rejected", func(t *testing.T) {
		if _, err := decodeVaultEncKeyHex("not hex at all!!"); err == nil {
			t.Fatal("expected an error for non-hex input")
		}
	})
	t.Run("empty is rejected", func(t *testing.T) {
		if _, err := decodeVaultEncKeyHex(""); err == nil {
			t.Fatal("expected an error for empty input")
		}
	})
}
