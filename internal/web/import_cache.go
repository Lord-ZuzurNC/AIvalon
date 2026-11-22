package web

import (
	"sync"
	"time"
)

// importPendingTTL is how long a "this is actually a Character,
// import it there instead?" confirmation stays valid — long enough to
// read the question and click, short enough that an abandoned upload
// doesn't sit in memory indefinitely.
const importPendingTTL = 5 * time.Minute

type pendingImportFile struct {
	raw      []byte
	filename string
	vaulted  bool
	expiry   time.Time
}

// ImportCache holds raw upload bytes between "you dropped a Character
// JSON into World's importer" and the user's Yes/No answer on
// whether to resume the import in the right place — a plain token
// handoff (same shape as VaultGate's unlock tokens) rather than
// round-tripping a potentially large JSON blob through a hidden form
// field. One instance is shared by WorldHandlers and CharacterHandlers
// so a token stashed by one side can be redeemed by the other.
// vaulted rides along too — an import started from the Vault page
// must still land vaulted even after its own "wrong importer, finish
// it over there?" detour.
type ImportCache struct {
	mu    sync.Mutex
	files map[string]pendingImportFile
}

func NewImportCache() *ImportCache {
	return &ImportCache{files: make(map[string]pendingImportFile)}
}

// Stash saves raw/filename/vaulted under a fresh random token and returns it.
func (c *ImportCache) Stash(raw []byte, filename string, vaulted bool) (string, error) {
	token, err := randomHex(16)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.files[token] = pendingImportFile{raw: raw, filename: filename, vaulted: vaulted, expiry: time.Now().Add(importPendingTTL)}
	return token, nil
}

// Take returns the stashed bytes for token and removes them — a
// token is redeemed at most once, same as the Vault's unlock tokens
// aren't meant to be reused for unrelated purposes.
func (c *ImportCache) Take(token string) (raw []byte, filename string, vaulted bool, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, found := c.files[token]
	if !found {
		return nil, "", false, false
	}
	delete(c.files, token)
	if time.Now().After(f.expiry) {
		return nil, "", false, false
	}
	return f.raw, f.filename, f.vaulted, true
}
