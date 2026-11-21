package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// CharacterCard follows the Character Card V2/V3 field shape (see
// docs/DESIGN.md) so cards from the wider ecosystem map cleanly. Cards
// created by hand in AIvalon (no import pipeline yet) get a RawJSON that
// just mirrors these fields; a real V2/V3 import later overwrites RawJSON
// with whatever the source card actually carried, including fields not
// listed here — that's what the column exists for.
//
// AuthorNote mirrors the spec's "creator_notes" — a note from whoever
// wrote the card (tips on running it, intended model, etc), not a
// per-session SillyTavern-style "Author's Note" injected at some depth
// in the chat (AIvalon doesn't have that feature).
type CharacterCard struct {
	ID              int64
	Name            string
	Description     string
	Personality     string
	Scenario        string
	FirstMessage    string
	ExampleMessages string
	AuthorNote      string
	// ImagePath is relative to web/static (e.g. "uploads/characters/3.png"),
	// served by the existing GET /static/ route — "" means no image was
	// uploaded, which the UI treats as a placeholder case, not an error.
	ImagePath string
	// ChatCount is populated by ListCharacterCards only: how many
	// sessions include this card (and would be deleted along with it).
	ChatCount int
}

// ListCharacterCards returns one page of cards, ordered by name. limit < 0
// means "no limit" — see ListLorebooks.
func ListCharacterCards(db *sql.DB, limit, offset int) ([]CharacterCard, error) {
	rows, err := db.Query(`
		SELECT cc.id, cc.name, cc.description, cc.personality, cc.scenario, cc.first_message, cc.example_messages, cc.author_note, COALESCE(cci.image_path, ''),
			(SELECT COUNT(*) FROM session_characters sc WHERE sc.character_card_id = cc.id)
		FROM character_cards cc
		LEFT JOIN character_card_images cci ON cci.id = cc.primary_image_id
		ORDER BY cc.name, cc.id
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing character cards: %w", err)
	}
	defer rows.Close()

	var cards []CharacterCard
	for rows.Next() {
		var c CharacterCard
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.Personality, &c.Scenario, &c.FirstMessage, &c.ExampleMessages, &c.AuthorNote, &c.ImagePath, &c.ChatCount); err != nil {
			return nil, fmt.Errorf("scanning character card: %w", err)
		}
		cards = append(cards, c)
	}
	return cards, rows.Err()
}

func CountCharacterCards(db *sql.DB) (int, error) {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM character_cards`).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting character cards: %w", err)
	}
	return n, nil
}

func GetCharacterCard(db *sql.DB, id int64) (CharacterCard, error) {
	var c CharacterCard
	err := db.QueryRow(`
		SELECT cc.id, cc.name, cc.description, cc.personality, cc.scenario, cc.first_message, cc.example_messages, cc.author_note, COALESCE(cci.image_path, '')
		FROM character_cards cc
		LEFT JOIN character_card_images cci ON cci.id = cc.primary_image_id
		WHERE cc.id = ?`, id,
	).Scan(&c.ID, &c.Name, &c.Description, &c.Personality, &c.Scenario, &c.FirstMessage, &c.ExampleMessages, &c.AuthorNote, &c.ImagePath)
	if err != nil {
		return CharacterCard{}, fmt.Errorf("getting character card %d: %w", id, err)
	}
	return c, nil
}

func CreateCharacterCard(db *sql.DB, c CharacterCard) (int64, error) {
	rawJSON, err := json.Marshal(c)
	if err != nil {
		return 0, fmt.Errorf("encoding card for raw_json: %w", err)
	}
	res, err := db.Exec(
		`INSERT INTO character_cards (name, description, personality, scenario, first_message, example_messages, author_note, raw_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.Description, c.Personality, c.Scenario, c.FirstMessage, c.ExampleMessages, c.AuthorNote, string(rawJSON),
	)
	if err != nil {
		return 0, fmt.Errorf("creating character card: %w", err)
	}
	return res.LastInsertId()
}

func UpdateCharacterCard(db *sql.DB, c CharacterCard) error {
	rawJSON, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encoding card for raw_json: %w", err)
	}
	_, err = db.Exec(
		`UPDATE character_cards SET name = ?, description = ?, personality = ?, scenario = ?, first_message = ?, example_messages = ?, author_note = ?, raw_json = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		c.Name, c.Description, c.Personality, c.Scenario, c.FirstMessage, c.ExampleMessages, c.AuthorNote, string(rawJSON), c.ID,
	)
	if err != nil {
		return fmt.Errorf("updating character card %d: %w", c.ID, err)
	}
	return nil
}

// DeleteCharacterCard also deletes every session including it — the
// WHOLE session, even a group chat with other characters still in it
// (Lord's explicit call). See deleteWithSessions; the web handler
// confirms with the user first. Returns generated-image files now
// orphaned by that cascade — the caller is responsible for removing
// them from disk.
func DeleteCharacterCard(db *sql.DB, id int64) ([]string, error) {
	return deleteWithSessions(db, characterSessionIDs, "character_cards", id)
}

// ListCharacterCardsForSession returns the characters attached to a
// session, in turn_order.
func ListCharacterCardsForSession(db *sql.DB, sessionID int64) ([]CharacterCard, error) {
	rows, err := db.Query(`
		SELECT cc.id, cc.name, cc.description, cc.personality, cc.scenario, cc.first_message, cc.example_messages, cc.author_note, COALESCE(cci.image_path, '')
		FROM character_cards cc
		LEFT JOIN character_card_images cci ON cci.id = cc.primary_image_id
		JOIN session_characters sc ON sc.character_card_id = cc.id
		WHERE sc.session_id = ?
		ORDER BY sc.turn_order`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("listing characters for session %d: %w", sessionID, err)
	}
	defer rows.Close()

	var cards []CharacterCard
	for rows.Next() {
		var c CharacterCard
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.Personality, &c.Scenario, &c.FirstMessage, &c.ExampleMessages, &c.AuthorNote, &c.ImagePath); err != nil {
			return nil, fmt.Errorf("scanning character card: %w", err)
		}
		cards = append(cards, c)
	}
	return cards, rows.Err()
}

func AttachCharacterToSession(db *sql.DB, sessionID, characterCardID int64, turnOrder int) error {
	_, err := db.Exec(
		`INSERT INTO session_characters (session_id, character_card_id, turn_order) VALUES (?, ?, ?)`,
		sessionID, characterCardID, turnOrder,
	)
	if err != nil {
		return fmt.Errorf("attaching character %d to session %d: %w", characterCardID, sessionID, err)
	}
	return nil
}
