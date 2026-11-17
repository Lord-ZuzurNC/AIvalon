package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// vaultCookieName is the one HttpOnly cookie proving "this browser
// already typed the PIN" — localhost-only app, so Secure isn't set
// (there's no TLS to require), but HttpOnly+SameSite=Lax still keeps
// it out of reach of any injected script and of cross-site requests.
const vaultCookieName = "aivalon_vault"

// vaultUnlockTTL is how long a successful unlock holds — short enough
// that walking away from the machine doesn't leave the vault open
// indefinitely, long enough not to be annoying mid-session.
const vaultUnlockTTL = 2 * time.Hour

// vaultLockoutThreshold/vaultLockoutDuration: a 4-digit PIN only has
// 10,000 possible values, so hashing strength doesn't matter much here
// — what actually stops brute-forcing it is this lockout. 5 wrong
// guesses in a row buys 30 seconds before another attempt is even
// checked, which turns "script tries all 10,000 combinations" into a
// multi-hour affair instead of an instant.
const (
	vaultLockoutThreshold = 5
	vaultLockoutDuration  = 30 * time.Second
)

// VaultGate is the shared PIN-check/unlock-token state behind both
// VaultHandlers (the vault's own pages) and ChatHandlers.Show (which
// must refuse a vaulted session's direct URL to a browser that never
// unlocked the vault — hiding it from the session list alone would be
// cosmetic, not real protection). One instance, injected into both
// handler structs from main.go.
type VaultGate struct {
	mu             sync.Mutex
	tokens         map[string]time.Time // unlock token -> expiry
	failedAttempts int
	lockedUntil    time.Time
}

func NewVaultGate() *VaultGate {
	return &VaultGate{tokens: make(map[string]time.Time)}
}

// Unlocked reports whether r carries a still-valid unlock cookie.
func (g *VaultGate) Unlocked(r *http.Request) bool {
	c, err := r.Cookie(vaultCookieName)
	if err != nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	expiry, ok := g.tokens[c.Value]
	return ok && time.Now().Before(expiry)
}

// grantUnlock issues a fresh token, stores its expiry, and sets the
// cookie on w.
func (g *VaultGate) grantUnlock(w http.ResponseWriter) error {
	token, err := randomHex(32)
	if err != nil {
		return err
	}
	expiry := time.Now().Add(vaultUnlockTTL)
	g.mu.Lock()
	g.tokens[token] = expiry
	g.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     vaultCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiry,
	})
	return nil
}

// revokeUnlock clears the cookie client-side; the stale token left in
// g.tokens expires on its own and is otherwise harmless.
func revokeUnlock(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: vaultCookieName, Value: "", Path: "/", MaxAge: -1})
}

// lockedOut reports whether too many recent failures are still being
// penalized, and if so how much longer.
func (g *VaultGate) lockedOut() (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if time.Now().Before(g.lockedUntil) {
		return true, time.Until(g.lockedUntil)
	}
	return false, 0
}

func (g *VaultGate) recordFailure() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failedAttempts++
	if g.failedAttempts >= vaultLockoutThreshold {
		g.lockedUntil = time.Now().Add(vaultLockoutDuration)
		g.failedAttempts = 0
	}
}

func (g *VaultGate) recordSuccess() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failedAttempts = 0
}

// hashClientSide is sha256(salt || pin), hex-encoded — kept here only
// as documentation of exactly what the client's vaultPinHashScript
// must reproduce (see vault.templ); the Go side never calls it, since
// the browser does the hashing before the PIN ever hits the network
// (Lord caught the raw PIN sitting in a Network-tab payload at
// unlock — see vault.templ's comment). Not bcrypt/argon2: a 4-8 digit
// PIN's entire keyspace is small enough that a slow hash wouldn't
// meaningfully raise the cost of an offline brute force anyway (10^4
// to 10^8 guesses is nothing to try even at bcrypt speeds) — the real
// defense against *online* guessing is the lockout above; a salted
// hash is just so a copy of the DB file doesn't hand over the PIN in
// the clear, and doing it client-side additionally keeps it out of
// the request body entirely.
//
//	sha256Hex(salt + pin) // JS: crypto.subtle.digest("SHA-256", ...)

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// verifyHash does a constant-time comparison of the client-submitted
// hash against the stored one — not that timing meaningfully leaks a
// sha256 hex digest, but there's no reason to compare it the sloppy
// way either.
func verifyHash(gotHash, wantHash string) bool {
	return subtle.ConstantTimeCompare([]byte(gotHash), []byte(wantHash)) == 1
}
