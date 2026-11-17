package web

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

type VaultHandlers struct {
	DB   *sql.DB
	Gate *VaultGate
}

// Show renders whichever of two states applies: no PIN configured yet
// (setup form, with a freshly generated salt), or a PIN entry form
// (with the vault's existing salt). Unlike the rest of the app's
// unlock state, this always re-prompts — Lord's call: "on doit taper
// le pin à chaque ouverture du vault". The Gate's unlock cookie still
// exists and still gates a vaulted session's own URL/Stream for the
// rest of this visit once Unlock below succeeds; it just isn't
// consulted here to skip the prompt on the vault's own landing page.
func (h VaultHandlers) Show(w http.ResponseWriter, r *http.Request) {
	stored, err := db.GetVaultPIN(h.DB)
	if err == db.ErrVaultNotConfigured {
		salt, err := randomHex(16)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		render(w, r, VaultSetupPage("", salt))
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, VaultUnlockPage("", stored.PINSalt))
}

// Setup creates the vault's one PIN — or changes it, but only for a
// browser that already proved it knows the current one (Gate.Unlocked):
// without that check, anyone could silently overwrite an existing PIN
// via POST /vault/setup with a PIN of their own choosing and walk
// straight into every hidden session, defeating the whole feature.
//
// The PIN itself never reaches this handler: vault.templ's
// vaultPinHashScript hashes it client-side with the salt the GET above
// embedded in the form, so pin_hash/confirm_hash are what arrive here.
// Comparing those two hashes for equality is just as good as comparing
// the raw values would have been (hashing is deterministic), and
// means the actual digits never sit in a request body a DevTools
// Network tab could show — see Lord's screenshot that started this.
func (h VaultHandlers) Setup(w http.ResponseWriter, r *http.Request) {
	existing, err := db.GetVaultPIN(h.DB)
	if err == nil && !h.Gate.Unlocked(r) {
		render(w, r, VaultUnlockPage("A PIN is already set — unlock the vault first to change it.", existing.PINSalt))
		return
	} else if err != nil && err != db.ErrVaultNotConfigured {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	r.ParseForm()
	salt := r.FormValue("salt")
	pinHash := r.FormValue("pin_hash")
	confirmHash := r.FormValue("confirm_hash")
	digits, _ := strconv.Atoi(r.FormValue("digits"))
	retry := func(msg string) {
		freshSalt, err := randomHex(16)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		render(w, r, VaultSetupPage(msg, freshSalt))
	}
	switch digits {
	case 4, 6, 8:
	default:
		retry("PIN must be 4, 6, or 8 digits.")
		return
	}
	if pinHash == "" || !verifyHash(pinHash, confirmHash) {
		retry("The two PINs didn't match.")
		return
	}
	if err := db.SetVaultPIN(h.DB, pinHash, salt, digits); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.Gate.grantUnlock(w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderList(w, r, salt)
}

// Unlock checks the submitted hash against the stored one and, on
// success, grants this browser an unlock cookie for vaultUnlockTTL.
// Like Setup, the raw PIN itself is never part of this request — see
// that handler's comment.
func (h VaultHandlers) Unlock(w http.ResponseWriter, r *http.Request) {
	stored, err := db.GetVaultPIN(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if locked, _ := h.Gate.lockedOut(); locked {
		render(w, r, VaultUnlockPage("Too many wrong attempts — wait a bit before trying again.", stored.PINSalt))
		return
	}
	r.ParseForm()
	pinHash := r.FormValue("pin_hash")
	if pinHash == "" || !verifyHash(pinHash, stored.PINHash) {
		h.Gate.recordFailure()
		render(w, r, VaultUnlockPage("Wrong PIN.", stored.PINSalt))
		return
	}
	h.Gate.recordSuccess()
	if err := h.Gate.grantUnlock(w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderList(w, r, stored.PINSalt)
}

// renderList is shared by Setup and Unlock's success path: both end
// with the vaulted session list, not a redirect to GET /vault — a
// redirect would land back on the always-re-prompt unlock form (see
// Show's comment) instead of actually showing what unlocking was for.
// salt is threaded through so the list's own per-session "remove from
// vault" forms can hash their PIN client-side too.
func (h VaultHandlers) renderList(w http.ResponseWriter, r *http.Request, salt string) {
	sessions, err := db.ListVaultedSessions(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, VaultListPage(sessions, salt))
}

// Lock clears this browser's unlock cookie — an explicit "re-lock
// now" action rather than waiting for vaultUnlockTTL to expire.
func (h VaultHandlers) Lock(w http.ResponseWriter, r *http.Request) {
	revokeUnlock(w)
	http.Redirect(w, r, "/chat", http.StatusSeeOther)
}

// MoveIn hides a session in the vault — no PIN needed going in, per
// Lord's own framing: entering is just "hide this", leaving is the
// part that needs to prove it's really you.
func (h VaultHandlers) MoveIn(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := db.SetSessionVaulted(h.DB, id, true); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/chat", http.StatusSeeOther)
}

// MoveOut requires the PIN to be retyped — checked fresh against the
// stored hash regardless of whether this browser's vault is currently
// unlocked, since "I can view the vault" and "I'm allowed to pull a
// session back out into the normal list" are deliberately two
// different confirmations per Lord's spec. Like Unlock, it's a
// pre-hashed pin_hash that arrives here, not the raw PIN.
func (h VaultHandlers) MoveOut(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	stored, err := db.GetVaultPIN(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if locked, _ := h.Gate.lockedOut(); locked {
		http.Error(w, "too many wrong attempts, try again shortly", http.StatusTooManyRequests)
		return
	}
	r.ParseForm()
	pinHash := r.FormValue("pin_hash")
	if pinHash == "" || !verifyHash(pinHash, stored.PINHash) {
		h.Gate.recordFailure()
		sessions, err := db.ListVaultedSessions(h.DB)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		render(w, r, VaultListPageWithError(sessions, stored.PINSalt, "Wrong PIN — session stays in the vault.", id))
		return
	}
	h.Gate.recordSuccess()
	if err := db.SetSessionVaulted(h.DB, id, false); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/chat", http.StatusSeeOther)
}
