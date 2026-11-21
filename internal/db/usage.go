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
//
// Returns the generated-image files (messages.image_path) that no
// longer have ANY message referencing them once those sessions are
// gone — NOT simply "every image_path the deleted sessions had":
// Branch/Duplicate copy image_path onto a new message pointing at the
// SAME file (see handlers_chat.go), so a file is only safe to delete
// once nothing, anywhere, still points at it. The check runs inside
// this same transaction, after the DELETE but before Commit, so it
// sees the post-delete state accurately. Callers (the web handlers)
// are responsible for actually removing these files from disk — this
// package has no business touching the filesystem.
func deleteWithSessions(db *sql.DB, idsSQL, entityTable string, id int64) ([]string, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("deleting %s %d: %w", entityTable, id, err)
	}
	defer tx.Rollback()

	candidates, err := queryStrings(tx, `SELECT DISTINCT image_path FROM messages WHERE session_id IN (`+idsSQL+`) AND image_path IS NOT NULL AND image_path != ''`, id)
	if err != nil {
		return nil, fmt.Errorf("listing images used by sessions using %s %d: %w", entityTable, id, err)
	}

	if _, err := tx.Exec(`DELETE FROM sessions WHERE id IN (`+idsSQL+`)`, id); err != nil {
		return nil, fmt.Errorf("deleting sessions using %s %d: %w", entityTable, id, err)
	}
	if _, err := tx.Exec(`DELETE FROM `+entityTable+` WHERE id = ?`, id); err != nil {
		return nil, fmt.Errorf("deleting %s %d: %w", entityTable, id, err)
	}

	var orphaned []string
	for _, p := range candidates {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM messages WHERE image_path = ?`, p).Scan(&n); err != nil {
			return nil, fmt.Errorf("checking remaining references to %q: %w", p, err)
		}
		if n == 0 {
			orphaned = append(orphaned, p)
		}
	}
	return orphaned, tx.Commit()
}

// queryStrings runs a single-column query and collects the results —
// shared by deleteWithSessions' and ImagePathsForSession's image-path
// lookups.
func queryStrings(tx dbQuerier, query string, args ...any) ([]string, error) {
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// dbQuerier is the one method queryStrings needs — satisfied by both
// *sql.DB (ImagePathsForSession, outside any transaction) and *sql.Tx
// (deleteWithSessions, inside one).
type dbQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// ImagePathsForSession returns the distinct generated-image file
// paths used by messages in session id — the candidates a direct
// session delete (ChatHandlers.Delete) must check with
// OrphanedImagePaths once the session itself is gone.
func ImagePathsForSession(db *sql.DB, sessionID int64) ([]string, error) {
	paths, err := queryStrings(db, `SELECT DISTINCT image_path FROM messages WHERE session_id = ? AND image_path IS NOT NULL AND image_path != ''`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("listing images used by session %d: %w", sessionID, err)
	}
	return paths, nil
}

// OrphanedImagePaths filters candidatePaths down to the ones no
// message anywhere still references — call AFTER the session(s) that
// used to hold them are already deleted (same Branch/Duplicate
// shared-file reasoning as deleteWithSessions).
func OrphanedImagePaths(db *sql.DB, candidatePaths []string) ([]string, error) {
	var orphaned []string
	for _, p := range candidatePaths {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE image_path = ?`, p).Scan(&n); err != nil {
			return nil, fmt.Errorf("checking remaining references to %q: %w", p, err)
		}
		if n == 0 {
			orphaned = append(orphaned, p)
		}
	}
	return orphaned, nil
}
