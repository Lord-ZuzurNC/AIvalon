package db

import (
	"database/sql"
	"fmt"
)

// Session is deliberately minimal for now: a backend profile and a message
// log. Persona/lorebook/character attachment (docs/DESIGN.md's full
// Session shape) lands once those CRUDs exist — a session with none of
// them attached is a valid, useful state on its own (plain chat against a
// backend), not a half-built feature.
type Session struct {
	ID               int64
	Title            string
	BackendProfileID int64
}

// ChatMessage.Role follows docs/DESIGN.md's vocabulary: "user" | "character" | "system".
// CharacterCardID is 0 (unset) until a session actually has a character
// attached; a "character" message with no card is a generic AI reply.
type ChatMessage struct {
	ID              int64
	SessionID       int64
	Role            string
	CharacterCardID int64
	Content         string
}

func ListSessions(db *sql.DB) ([]Session, error) {
	rows, err := db.Query(`SELECT id, title, backend_profile_id FROM sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.Title, &s.BackendProfileID); err != nil {
			return nil, fmt.Errorf("scanning session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func GetSession(db *sql.DB, id int64) (Session, error) {
	var s Session
	err := db.QueryRow(`SELECT id, title, backend_profile_id FROM sessions WHERE id = ?`, id).
		Scan(&s.ID, &s.Title, &s.BackendProfileID)
	if err != nil {
		return Session{}, fmt.Errorf("getting session %d: %w", id, err)
	}
	return s, nil
}

func CreateSession(db *sql.DB, s Session) (int64, error) {
	res, err := db.Exec(`INSERT INTO sessions (title, backend_profile_id) VALUES (?, ?)`, s.Title, s.BackendProfileID)
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
