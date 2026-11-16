package db

import (
	"database/sql"
	"fmt"
)

// Session.PersonaID/LorebookID are 0 (unset) until the user picks one;
// CharacterCards are attached separately via session_characters (a
// many-to-many join, since a session can be a group chat) — see
// ListCharacterCardsForSession / AttachCharacterToSession in
// character_cards.go.
type Session struct {
	ID               int64
	Title            string
	BackendProfileID int64
	PersonaID        int64
	LorebookID       int64
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
	rows, err := db.Query(`SELECT id, title, backend_profile_id, COALESCE(persona_id, 0), COALESCE(lorebook_id, 0) FROM sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.Title, &s.BackendProfileID, &s.PersonaID, &s.LorebookID); err != nil {
			return nil, fmt.Errorf("scanning session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func GetSession(db *sql.DB, id int64) (Session, error) {
	var s Session
	err := db.QueryRow(`SELECT id, title, backend_profile_id, COALESCE(persona_id, 0), COALESCE(lorebook_id, 0) FROM sessions WHERE id = ?`, id).
		Scan(&s.ID, &s.Title, &s.BackendProfileID, &s.PersonaID, &s.LorebookID)
	if err != nil {
		return Session{}, fmt.Errorf("getting session %d: %w", id, err)
	}
	return s, nil
}

func CreateSession(db *sql.DB, s Session) (int64, error) {
	var personaID, lorebookID any
	if s.PersonaID != 0 {
		personaID = s.PersonaID
	}
	if s.LorebookID != 0 {
		lorebookID = s.LorebookID
	}
	res, err := db.Exec(
		`INSERT INTO sessions (title, backend_profile_id, persona_id, lorebook_id) VALUES (?, ?, ?, ?)`,
		s.Title, s.BackendProfileID, personaID, lorebookID,
	)
	if err != nil {
		return 0, fmt.Errorf("creating session: %w", err)
	}
	return res.LastInsertId()
}

// DeleteSession relies on the schema's ON DELETE CASCADE (messages,
// session_characters both reference sessions(id) that way — see
// 00001_init.sql) plus the foreign_keys pragma Open sets on every
// connection, so this one DELETE is enough; it does not need to clean up
// either table itself.
func DeleteSession(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting session %d: %w", id, err)
	}
	return nil
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

func GetMessage(db *sql.DB, id int64) (ChatMessage, error) {
	var m ChatMessage
	err := db.QueryRow(
		`SELECT id, session_id, role, COALESCE(character_card_id, 0), content FROM messages WHERE id = ?`, id,
	).Scan(&m.ID, &m.SessionID, &m.Role, &m.CharacterCardID, &m.Content)
	if err != nil {
		return ChatMessage{}, fmt.Errorf("getting message %d: %w", id, err)
	}
	return m, nil
}

// UpdateMessageContent is Edit's write — see DeleteMessagesFrom for the
// "and everything that followed is now stale" half of Edit.
func UpdateMessageContent(db *sql.DB, id int64, content string) error {
	_, err := db.Exec(`UPDATE messages SET content = ? WHERE id = ?`, content, id)
	if err != nil {
		return fmt.Errorf("updating message %d: %w", id, err)
	}
	return nil
}

// DeleteMessage removes exactly one message — Delete's write. Unlike
// Regenerate/Edit, this never cascades to later messages: the point is
// to drop one line, not rewind the conversation.
func DeleteMessage(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM messages WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting message %d: %w", id, err)
	}
	return nil
}

// DeleteMessagesFrom deletes fromMessageID and every message in the same
// session that came after it. IDs are assigned in strict insertion order
// (SQLite AUTOINCREMENT) and a session's own messages are always
// inserted in conversation order, so "id >= fromMessageID within this
// session" is exactly "this message onward" — no separate timestamp
// comparison needed.
//
// Two callers, two different "from": Regenerate deletes the AI reply
// itself to produce a fresh one in its place. Edit deletes everything
// AFTER the edited message (pass editedID+1) — the edited message's own
// new content is written separately via UpdateMessageContent, since
// editing it doesn't mean deleting it.
func DeleteMessagesFrom(db *sql.DB, sessionID, fromMessageID int64) error {
	_, err := db.Exec(`DELETE FROM messages WHERE session_id = ? AND id >= ?`, sessionID, fromMessageID)
	if err != nil {
		return fmt.Errorf("deleting messages from %d in session %d: %w", fromMessageID, sessionID, err)
	}
	return nil
}

// BranchSession is Branch's write: a new session, same backend
// profile/persona/lorebook/characters as the original, carrying a copy
// of every message up to and including uptoMessageID — never the
// original session's messages reused by reference, since further edits
// in either session must not leak into the other. Returns the new
// session's id so the handler can redirect straight into it.
func BranchSession(db *sql.DB, sessionID, uptoMessageID int64) (int64, error) {
	newSessionID, err := copySessionShell(db, sessionID, "branch")
	if err != nil {
		return 0, err
	}

	messages, err := ListMessages(db, sessionID)
	if err != nil {
		return 0, fmt.Errorf("listing messages to branch: %w", err)
	}
	for _, m := range messages {
		if _, err := CreateMessage(db, ChatMessage{
			SessionID:       newSessionID,
			Role:            m.Role,
			CharacterCardID: m.CharacterCardID,
			Content:         m.Content,
		}); err != nil {
			return 0, fmt.Errorf("copying message %d into branch: %w", m.ID, err)
		}
		if m.ID == uptoMessageID {
			break
		}
	}

	return newSessionID, nil
}

// DuplicateSession is Duplicate's write: same as BranchSession but
// copies every message in the session, not just up to a cutoff — a
// plain full clone to branch off from (or just keep as a backup before
// trying something risky in the original) rather than a specific-point
// fork.
func DuplicateSession(db *sql.DB, sessionID int64) (int64, error) {
	newSessionID, err := copySessionShell(db, sessionID, "copy")
	if err != nil {
		return 0, err
	}

	messages, err := ListMessages(db, sessionID)
	if err != nil {
		return 0, fmt.Errorf("listing messages to duplicate: %w", err)
	}
	for _, m := range messages {
		if _, err := CreateMessage(db, ChatMessage{
			SessionID:       newSessionID,
			Role:            m.Role,
			CharacterCardID: m.CharacterCardID,
			Content:         m.Content,
		}); err != nil {
			return 0, fmt.Errorf("copying message %d into duplicate: %w", m.ID, err)
		}
	}

	return newSessionID, nil
}

// copySessionShell is the part Branch and Duplicate share: a new
// session with the original's backend profile/persona/lorebook and
// attached characters, titled "<original> (suffix)". Neither caller's
// message-copying logic lives here, since that's the one thing they do
// differently (a cutoff vs. everything).
func copySessionShell(db *sql.DB, sessionID int64, suffix string) (int64, error) {
	original, err := GetSession(db, sessionID)
	if err != nil {
		return 0, err
	}

	newSessionID, err := CreateSession(db, Session{
		Title:            fmt.Sprintf("%s (%s)", original.Title, suffix),
		BackendProfileID: original.BackendProfileID,
		PersonaID:        original.PersonaID,
		LorebookID:       original.LorebookID,
	})
	if err != nil {
		return 0, fmt.Errorf("creating %s session: %w", suffix, err)
	}

	characters, err := ListCharacterCardsForSession(db, sessionID)
	if err != nil {
		return 0, fmt.Errorf("listing characters to copy: %w", err)
	}
	for i, c := range characters {
		if err := AttachCharacterToSession(db, newSessionID, c.ID, i); err != nil {
			return 0, fmt.Errorf("attaching copied character %d: %w", c.ID, err)
		}
	}

	return newSessionID, nil
}
