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

// Show renders whichever of three states applies: no PIN configured
// yet (setup form), a PIN exists but this browser hasn't unlocked it
// (PIN entry form), or it's unlocked (the vaulted session list).
func (h VaultHandlers) Show(w http.ResponseWriter, r *http.Request) {
	_, err := db.GetVaultPIN(h.DB)
	if err == db.ErrVaultNotConfigured {
		render(w, r, VaultSetupPage(""))
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !h.Gate.Unlocked(r) {
		render(w, r, VaultUnlockPage(""))
		return
	}
	sessions, err := db.ListVaultedSessions(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, VaultListPage(sessions))
}

// Setup creates the vault's one PIN. Only reachable while none exists
// yet (Show gates the form itself); a repeat POST once a PIN is already
// set just overwrites it, same as any other first-run setting would —
// there's no "change PIN" flow separate from this in the UI.
func (h VaultHandlers) Setup(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	pin := r.FormValue("pin")
	confirm := r.FormValue("pin_confirm")
	if !isValidPINFormat(pin) {
		render(w, r, VaultSetupPage("PIN must be 4, 6, or 8 digits."))
		return
	}
	if pin != confirm {
		render(w, r, VaultSetupPage("The two PINs didn't match."))
		return
	}
	salt, err := randomHex(16)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := db.SetVaultPIN(h.DB, hashPIN(pin, salt), salt, len(pin)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.Gate.grantUnlock(w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/vault", http.StatusSeeOther)
}

// Unlock checks the PIN against the stored hash and, on success,
// grants this browser an unlock cookie for vaultUnlockTTL.
func (h VaultHandlers) Unlock(w http.ResponseWriter, r *http.Request) {
	if locked, _ := h.Gate.lockedOut(); locked {
		render(w, r, VaultUnlockPage("Too many wrong attempts — wait a bit before trying again."))
		return
	}
	stored, err := db.GetVaultPIN(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	r.ParseForm()
	pin := r.FormValue("pin")
	if !verifyPIN(pin, stored.PINSalt, stored.PINHash) {
		h.Gate.recordFailure()
		render(w, r, VaultUnlockPage("Wrong PIN."))
		return
	}
	h.Gate.recordSuccess()
	if err := h.Gate.grantUnlock(w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/vault", http.StatusSeeOther)
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
// different confirmations per Lord's spec.
func (h VaultHandlers) MoveOut(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if locked, _ := h.Gate.lockedOut(); locked {
		http.Error(w, "too many wrong attempts, try again shortly", http.StatusTooManyRequests)
		return
	}
	stored, err := db.GetVaultPIN(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	r.ParseForm()
	pin := r.FormValue("pin")
	if !verifyPIN(pin, stored.PINSalt, stored.PINHash) {
		h.Gate.recordFailure()
		sessions, err := db.ListVaultedSessions(h.DB)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		render(w, r, VaultListPageWithError(sessions, "Wrong PIN — session stays in the vault."))
		return
	}
	h.Gate.recordSuccess()
	if err := db.SetSessionVaulted(h.DB, id, false); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/chat", http.StatusSeeOther)
}
