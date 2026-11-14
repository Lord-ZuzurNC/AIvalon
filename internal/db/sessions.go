package db

import (
	"database/sql"
	"fmt"
)

// Session.PersonaID/LorebookID are 0 (unset) until the user picks one;
// CharacterCards are attached separately via session_characters (a
// many-to-many join, since a session can be a group chat) — see
// ListCharacterCardsForSession / AttachCharacterToSession in
// character_cards.go. Lorebook attachment isn't wired up yet: no Lorebook
// CRUD exists, same reasoning as everything else here — build it when it
// exists, not a placeholder for it.
type Session struct {
	ID               int64
	Title            string
	BackendProfileID int64
	PersonaID        int64
}

// ChatMessage.Role follows docs/DESIGN.md's vocabulary: "user" | "character" | "system".
// CharacterCardID is 0 (unset) for "user"/"system" messages, and for a
// "character" reply before any character is attached to the session.
type ChatMessage struct {
	ID              int64
	SessionID       int64
	Role            string
	CharacterCardID int64
	Content         string
}

func ListSessions(db *sql.DB) ([]Session, error) {
	rows, err := db.Query(`SELECT id, title, backend_profile_id, COALESCE(persona_id, 0) FROM sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.Title, &s.BackendProfileID, &s.PersonaID); err != nil {
			return nil, fmt.Errorf("scanning session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func GetSession(db *sql.DB, id int64) (Session, error) {
	var s Session
	err := db.QueryRow(`SELECT id, title, backend_profile_id, COALESCE(persona_id, 0) FROM sessions WHERE id = ?`, id).
		Scan(&s.ID, &s.Title, &s.BackendProfileID, &s.PersonaID)
	if err != nil {
		return Session{}, fmt.Errorf("getting session %d: %w", id, err)
	}
	return s, nil
}

func CreateSession(db *sql.DB, s Session) (int64, error) {
	var personaID any
	if s.PersonaID != 0 {
		personaID = s.PersonaID
	}
	res, err := db.Exec(
		`INSERT INTO sessions (title, backend_profile_id, persona_id) VALUES (?, ?, ?)`,
		s.Title, s.BackendProfileID, personaID,
	)
	if err != nil {
		return 0, fmt.Errorf("creating session: %w", err)
	}
	return res.LastInsertId()
}

func ListMessages(db *sql.DB, sessionID int64) ([]ChatMessage, error) {
	rows, err := db.Query(
		`SELECT id, session_id, role, COALESCE(character_card_id, 0), content FROM messages WHERE session_id = ? ORDER BY created_at, id`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing messages for session %d: %w", sessionID, err)
	}
	defer rows.Close()

	var messages []ChatMessage
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.CharacterCardID, &m.Content); err != nil {
			return nil, fmt.Errorf("scanning message: %w", err)
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

func CreateMessage(db *sql.DB, m ChatMessage) (int64, error) {
	var characterCardID any
	if m.CharacterCardID != 0 {
		characterCardID = m.CharacterCardID
	}
	res, err := db.Exec(
		`INSERT INTO messages (session_id, role, character_card_id, content) VALUES (?, ?, ?, ?)`,
		m.SessionID, m.Role, characterCardID, m.Content,
	)
	if err != nil {
		return 0, fmt.Errorf("creating message: %w", err)
	}
	return res.LastInsertId()
}
