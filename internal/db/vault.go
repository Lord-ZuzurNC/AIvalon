package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// VaultPIN is the vault's one PIN record (singleton row, id=1).
// PINHash/PINSalt let the app verify a PIN without ever storing it in
// the clear — see internal/web/vault_gate.go for the hashing itself,
// this package only persists the result.
type VaultPIN struct {
	PINHash string
	PINSalt string
	Digits  int
}

// ErrVaultNotConfigured means no PIN has been set yet — the caller
// should show a setup form instead of an unlock form.
var ErrVaultNotConfigured = errors.New("vault PIN not configured")

func GetVaultPIN(db *sql.DB) (VaultPIN, error) {
	var v VaultPIN
	err := db.QueryRow(`SELECT pin_hash, pin_salt, digits FROM vault WHERE id = 1`).Scan(&v.PINHash, &v.PINSalt, &v.Digits)
	if err == sql.ErrNoRows {
		return VaultPIN{}, ErrVaultNotConfigured
	}
	if err != nil {
		return VaultPIN{}, fmt.Errorf("getting vault PIN: %w", err)
	}
	return v, nil
}

// SetVaultPIN upserts the singleton row — "set" doubles as "change the
// PIN" since there's only ever one.
func SetVaultPIN(db *sql.DB, hash, salt string, digits int) error {
	_, err := db.Exec(
		`INSERT INTO vault (id, pin_hash, pin_salt, digits) VALUES (1, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET pin_hash = excluded.pin_hash, pin_salt = excluded.pin_salt, digits = excluded.digits`,
		hash, salt, digits,
	)
	if err != nil {
		return fmt.Errorf("setting vault PIN: %w", err)
	}
	return nil
}

// DeleteVault removes the singleton PIN row — the vault goes back to
// "not configured" (Show renders the Setup form again). Callers
// (VaultHandlers.Reset) are responsible for deleting the vaulted
// content itself first; this alone doesn't touch sessions/characters/
// lorebooks at all.
func DeleteVault(db *sql.DB) error {
	if _, err := db.Exec(`DELETE FROM vault WHERE id = 1`); err != nil {
		return fmt.Errorf("deleting vault PIN: %w", err)
	}
	return nil
}

func SetSessionVaulted(db *sql.DB, sessionID int64, vaulted bool) error {
	_, err := db.Exec(`UPDATE sessions SET is_vaulted = ? WHERE id = ?`, vaulted, sessionID)
	if err != nil {
		return fmt.Errorf("setting session %d vaulted=%v: %w", sessionID, vaulted, err)
	}
	return nil
}

func SetCharacterCardVaulted(db *sql.DB, id int64, vaulted bool) error {
	_, err := db.Exec(`UPDATE character_cards SET is_vaulted = ? WHERE id = ?`, vaulted, id)
	if err != nil {
		return fmt.Errorf("setting character card %d vaulted=%v: %w", id, vaulted, err)
	}
	return nil
}

func SetLorebookVaulted(db *sql.DB, id int64, vaulted bool) error {
	_, err := db.Exec(`UPDATE lorebooks SET is_vaulted = ? WHERE id = ?`, vaulted, id)
	if err != nil {
		return fmt.Errorf("setting lorebook %d vaulted=%v: %w", id, vaulted, err)
	}
	return nil
}

// ListVaultedSessions is the vault's own session list — the mirror of
// ListSessions' "WHERE is_vaulted = 0" filter.
func ListVaultedSessions(db *sql.DB) ([]Session, error) {
	rows, err := db.Query(`SELECT id, title, backend_profile_id, COALESCE(persona_id, 0), COALESCE(lorebook_id, 0), is_vaulted FROM sessions WHERE is_vaulted = 1 ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing vaulted sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.Title, &s.BackendProfileID, &s.PersonaID, &s.LorebookID, &s.IsVaulted); err != nil {
			return nil, fmt.Errorf("scanning vaulted session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}
