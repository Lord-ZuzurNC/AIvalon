package db

import (
	"database/sql"
	"fmt"
)

// SessionRef is the lightweight "which chat uses this?" projection the
// delete-confirmation dialog lists. IsVaulted lets the UI mask a
// vaulted chat's title instead of leaking it outside the Vault.
type SessionRef struct {
	ID        int64
	Title     string
	IsVaulted bool
}

// Subqueries yielding the session ids that reference an entity — the
// single source of truth for both listing (confirmation dialog) and
// deleting (deleteWithSessions), so the two can never disagree.
const (
	personaSessionIDs   = `SELECT id FROM sessions WHERE persona_id = ?`
	lorebookSessionIDs  = `SELECT id FROM sessions WHERE lorebook_id = ?`
	characterSessionIDs = `SELECT session_id FROM session_characters WHERE character_card_id = ?`
)

func ListSessionsUsingPersona(db *sql.DB, id int64) ([]SessionRef, error) {
	return listSessionsIn(db, personaSessionIDs, id)
}

func ListSessionsUsingLorebook(db *sql.DB, id int64) ([]SessionRef, error) {
	return listSessionsIn(db, lorebookSessionIDs, id)
}

func ListSessionsUsingCharacterCard(db *sql.DB, id int64) ([]SessionRef, error) {
	return listSessionsIn(db, characterSessionIDs, id)
}

// listSessionsIn includes vaulted sessions on purpose: deleting the
// entity deletes them too, so they must count — hiding them here would
// let an "unused" delete silently wipe a vaulted chat.
func listSessionsIn(db *sql.DB, idsSQL string, id int64) ([]SessionRef, error) {
	rows, err := db.Query(`SELECT id, title, is_vaulted FROM sessions WHERE id IN (`+idsSQL+`) ORDER BY title COLLATE NOCASE, id`, id)
	if err != nil {
		return nil, fmt.Errorf("listing sessions using %d: %w", id, err)
	}
	defer rows.Close()
	var refs []SessionRef
	for rows.Next() {
		var s SessionRef
		if err := rows.Scan(&s.ID, &s.Title, &s.IsVaulted); err != nil {
			return nil, fmt.Errorf("scanning session ref: %w", err)
		}
		refs = append(refs, s)
	}
	return refs, rows.Err()
}

// deleteWithSessions deletes every session idsSQL selects (messages and
// session_characters follow via ON DELETE CASCADE), then the entity
// itself, in one transaction so a failure can't leave a half-delete.
// Sessions go first: messages.character_card_id has no ON DELETE
// action, so a card with messages can't be deleted while they exist.
func deleteWithSessions(db *sql.DB, idsSQL, entityTable string, id int64) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("deleting %s %d: %w", entityTable, id, err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM sessions WHERE id IN (`+idsSQL+`)`, id); err != nil {
		return fmt.Errorf("deleting sessions using %s %d: %w", entityTable, id, err)
	}
	if _, err := tx.Exec(`DELETE FROM `+entityTable+` WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting %s %d: %w", entityTable, id, err)
	}
	return tx.Commit()
}
