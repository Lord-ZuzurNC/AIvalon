package web

import (
	"database/sql"
	"log"
	"net/http"
	"sort"
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
	// ?tab= is the "coming back from acting on a vaulted item" entry
	// point (see vaultHome): an already-unlocked browser lands straight
	// on that tab instead of being re-prompted. A bare /vault — the
	// sidebar link, i.e. actually *opening* the vault — still always
	// re-prompts per the rule above.
	if tab := r.URL.Query().Get("tab"); tab != "" && h.Gate.Unlocked(r) {
		if _, ok := vaultKinds[tab]; ok {
			h.renderList(w, r, stored.PINSalt, tab)
			return
		}
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
	// Setup's form never collects enc_key_hash (see vault.templ) — a
	// PIN change here doesn't re-key already-encrypted files, so
	// granting a key from this flow would just be wrong for anything
	// encrypted under the *previous* PIN. Decrypting still needs a
	// real /vault/unlock after this.
	if err := h.Gate.grantUnlock(w, nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderList(w, r, salt, "")
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
	// enc_key_hash (vault.templ's vaultPinHashScript): a second,
	// domain-separated hash of the same PIN, used as the AES key for
	// vaulted files — decoded here, never logged or stored, only kept
	// in the in-memory unlock token (see VaultGate.grantUnlock).
	// Malformed/missing isn't fatal to unlocking the vault's UI, it
	// just means this browser's session can't decrypt vaulted files
	// until a fresh, well-formed unlock.
	encKey, _ := decodeVaultEncKeyHex(r.FormValue("enc_key_hash"))
	if err := h.Gate.grantUnlock(w, encKey); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Catches anything a vaulted entity's image left in plaintext —
	// MoveIn without a key available at that moment, an edit that hit
	// the same gap, or (Lord's call: no separate migration step) a
	// file that's simply been sitting there since before this feature
	// existed. Runs on every real unlock, not just once; cheap when
	// there's nothing to do (moveImageFile no-ops on anything already
	// in the right state).
	if len(encKey) > 0 {
		if err := reconcileVaultImages(h.DB, encKey); err != nil {
			log.Printf("vault unlock: reconciling image encryption: %v", err)
		}
	}
	h.renderList(w, r, stored.PINSalt, "")
}

// renderList is shared by Setup and Unlock's success path: both end
// with the vault's contents, not a redirect to GET /vault — a
// redirect would land back on the always-re-prompt unlock form (see
// Show's comment) instead of actually showing what unlocking was for.
// salt is threaded through so the list's own per-item "remove from
// vault" forms can hash their PIN client-side too.
func (h VaultHandlers) renderList(w http.ResponseWriter, r *http.Request, salt, tab string) {
	v, err := loadVaultContents(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, VaultListPageWithError(v, salt, tab, "", "", 0))
}

// VaultContents is everything hidden in the vault, one slice per tab.
type VaultContents struct {
	Sessions   []db.Session
	Characters []db.CharacterCard
	Lorebooks  []db.Lorebook

	// For the Chats tab's "+" (a vaulted new session): the usual
	// new-session pickers, except characters/lorebooks list vaulted
	// AND non-vaulted rows together, sorted by name so same-named
	// pairs sit side by side — the vault marker tells them apart.
	Profiles       []db.BackendProfile
	Personas       []db.Persona
	PickCharacters []db.CharacterCard
	PickLorebooks  []db.Lorebook
	ImagePresets   []db.ImagePreset
}

func loadVaultContents(sqlDB *sql.DB) (VaultContents, error) {
	var v VaultContents
	var err error
	if v.Sessions, err = db.ListVaultedSessions(sqlDB); err != nil {
		return v, err
	}
	if v.Characters, err = db.ListVaultedCharacterCards(sqlDB); err != nil {
		return v, err
	}
	if v.Lorebooks, err = db.ListVaultedLorebooks(sqlDB); err != nil {
		return v, err
	}
	if profiles, err := db.ListBackendProfiles(sqlDB); err != nil {
		return v, err
	} else {
		// Same LLM-only filter as ChatHandlers.List — a vaulted session
		// still needs to chat, so an "image" kind backend has no business
		// in this picker either.
		for _, p := range profiles {
			if p.Kind == "llm" {
				v.Profiles = append(v.Profiles, p)
			}
		}
	}
	if v.Personas, err = db.ListPersonas(sqlDB); err != nil {
		return v, err
	}
	if v.ImagePresets, err = db.ListImagePresets(sqlDB); err != nil {
		return v, err
	}
	plainCards, err := db.ListCharacterCards(sqlDB, -1, 0)
	if err != nil {
		return v, err
	}
	v.PickCharacters = append(plainCards, v.Characters...)
	sort.SliceStable(v.PickCharacters, func(i, j int) bool { return v.PickCharacters[i].Name < v.PickCharacters[j].Name })
	plainBooks, err := db.ListLorebooks(sqlDB, -1, 0)
	if err != nil {
		return v, err
	}
	v.PickLorebooks = append(plainBooks, v.Lorebooks...)
	sort.SliceStable(v.PickLorebooks, func(i, j int) bool { return v.PickLorebooks[i].Name < v.PickLorebooks[j].Name })
	return v, nil
}

// vaultHome is where a handler sends the browser after saving or
// deleting a character/lorebook/session: back to the plain list as
// always, unless the item is vaulted — the plain list excludes it, so
// that would make it vanish from view; the Vault's own tab doesn't.
func vaultHome(kind string, vaulted bool) string {
	if vaulted {
		return "/vault?tab=" + kind
	}
	return vaultKinds[kind].home
}

// vaultKinds maps the {kind} path segment of /vault/{kind}/{id}/in|out
// to that entity's setter, the list page it lives on outside the vault,
// and the noun used in the wrong-PIN message.
var vaultKinds = map[string]struct {
	set  func(*sql.DB, int64, bool) error
	home string
	noun string
}{
	"chats":      {db.SetSessionVaulted, "/chat", "session"},
	"characters": {db.SetCharacterCardVaulted, "/characters", "character"},
	"worlds":     {db.SetLorebookVaulted, "/world", "lorebook"},
}

// renderVaultLocked is the refusal every vaulted item's own direct URL
// gives a browser that hasn't unlocked the vault: 403 plus the unlock
// pad — hiding an item from its browse list alone would be cosmetic,
// not real protection, if /characters/{id}/edit still opened it.
func renderVaultLocked(w http.ResponseWriter, r *http.Request, sqlDB *sql.DB) {
	salt := ""
	if stored, err := db.GetVaultPIN(sqlDB); err == nil {
		salt = stored.PINSalt
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	VaultUnlockPage("", salt).Render(r.Context(), w)
}

// Lock clears this browser's unlock cookie — an explicit "re-lock
// now" action rather than waiting for vaultUnlockTTL to expire.
func (h VaultHandlers) Lock(w http.ResponseWriter, r *http.Request) {
	revokeUnlock(w)
	http.Redirect(w, r, "/chat", http.StatusSeeOther)
}

// Reset is the nuclear option Lord asked for: delete every vaulted
// session/character/lorebook (and their images) and the vault's own
// PIN itself, back to "not configured". Requires the PIN fresh, same
// "prove it's really you" rule as MoveOut — this is far more
// destructive than any single unhide, so it gets at least that much
// friction, not less. No enc_key_hash needed: everything's being
// deleted regardless of its content, nothing here gets decrypted.
func (h VaultHandlers) Reset(w http.ResponseWriter, r *http.Request) {
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
		v, err := loadVaultContents(h.DB)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		render(w, r, VaultListPageWithError(v, stored.PINSalt, "", "Wrong PIN — nothing was deleted.", "reset", 0))
		return
	}
	h.Gate.recordSuccess()

	v, err := loadVaultContents(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Characters and lorebooks first: deleting either one already
	// deletes every session using it (db.DeleteCharacterCard/
	// DeleteLorebook's own documented cascade, including a
	// NON-vaulted session that happens to share a now-vaulted
	// character/lorebook — same behavior a normal single-item delete
	// already has; Reset doesn't special-case around it). Sessions
	// still vaulted afterwards (not swept by that cascade) are
	// deleted directly below — DeleteSession on an ID the cascade
	// already removed is a harmless no-op, not an error.
	for _, c := range v.Characters {
		images, err := db.ListCharacterCardImages(h.DB, c.ID)
		if err != nil {
			log.Printf("vault reset: listing images for character %d: %v", c.ID, err)
		}
		orphaned, err := db.DeleteCharacterCard(h.DB, c.ID)
		if err != nil {
			log.Printf("vault reset: deleting character %d: %v", c.ID, err)
			continue
		}
		for _, img := range images {
			removeUploadedFile(img.ImagePath)
		}
		for _, p := range orphaned {
			removeUploadedFile(p)
		}
	}
	for _, bk := range v.Lorebooks {
		orphaned, err := db.DeleteLorebook(h.DB, bk.ID)
		if err != nil {
			log.Printf("vault reset: deleting lorebook %d: %v", bk.ID, err)
			continue
		}
		removeUploadedFile(bk.ImagePath)
		for _, p := range orphaned {
			removeUploadedFile(p)
		}
	}
	for _, s := range v.Sessions {
		candidates, err := db.ImagePathsForSession(h.DB, s.ID)
		if err != nil {
			log.Printf("vault reset: listing images for session %d: %v", s.ID, err)
		}
		if err := db.DeleteSession(h.DB, s.ID); err != nil {
			log.Printf("vault reset: deleting session %d: %v", s.ID, err)
			continue
		}
		if orphaned, err := db.OrphanedImagePaths(h.DB, candidates); err == nil {
			for _, p := range orphaned {
				removeUploadedFile(p)
			}
		}
	}

	if err := db.DeleteVault(h.DB); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	revokeUnlock(w)
	http.Redirect(w, r, "/chat", http.StatusSeeOther)
}

// MoveIn hides a session/character/lorebook in the vault — no PIN needed going in, per
// Lord's own framing: entering is just "hide this", leaving is the
// part that needs to prove it's really you.
//
// Its image(s) are encrypted on the spot (moveEntityImages) when this
// browser happens to already hold a decrypt key — e.g. hiding
// something from inside an already-unlocked vault session. Without
// one, "no PIN to hide" stays true: the image(s) just stay plaintext
// until the next real /vault/unlock's reconcileVaultImages catches
// them, rather than blocking (or silently weakening) the hide itself.
func (h VaultHandlers) MoveIn(w http.ResponseWriter, r *http.Request) {
	kindKey := r.PathValue("kind")
	kind, ok := vaultKinds[kindKey]
	if !ok {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := kind.set(h.DB, id, true); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if key, ok := h.Gate.EncKey(r); ok {
		if err := moveEntityImages(h.DB, key, kindKey, id, true); err != nil {
			log.Printf("vault MoveIn %s/%d: encrypting image(s): %v", kindKey, id, err)
		}
	}
	http.Redirect(w, r, kind.home, http.StatusSeeOther)
}

// MoveOut requires the PIN to be retyped — checked fresh against the
// stored hash regardless of whether this browser's vault is currently
// unlocked, since "I can view the vault" and "I'm allowed to pull a
// session back out into the normal list" are deliberately two
// different confirmations per Lord's spec. Like Unlock, it's a
// pre-hashed pin_hash that arrives here, not the raw PIN.
//
// Also collects enc_key_hash (vault.templ's "Unhide" pad now sends
// one too) to decrypt the entity's image(s) back to plaintext —
// that's the whole point of pulling something OUT of the vault, so
// unlike MoveIn this one really does need the key every time, not
// best-effort. Decryption failing (wrong/missing key) doesn't block
// the unhide itself — the flag still flips — but logs it, since an
// image staying ciphertext after "it's not vaulted anymore" would be
// a confusing silent gap (reconcileVaultImages only fixes the OTHER
// direction, encrypting, never decrypts on its own).
func (h VaultHandlers) MoveOut(w http.ResponseWriter, r *http.Request) {
	kindKey := r.PathValue("kind")
	kind, ok := vaultKinds[kindKey]
	if !ok {
		http.NotFound(w, r)
		return
	}
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
		v, err := loadVaultContents(h.DB)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		render(w, r, VaultListPageWithError(v, stored.PINSalt, kindKey, "Wrong PIN — "+kind.noun+" stays in the vault.", kindKey, id))
		return
	}
	h.Gate.recordSuccess()
	if err := kind.set(h.DB, id, false); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if encKey, err := decodeVaultEncKeyHex(r.FormValue("enc_key_hash")); err == nil {
		if err := moveEntityImages(h.DB, encKey, kindKey, id, false); err != nil {
			log.Printf("vault MoveOut %s/%d: decrypting image(s): %v", kindKey, id, err)
		}
	}
	http.Redirect(w, r, kind.home, http.StatusSeeOther)
}
