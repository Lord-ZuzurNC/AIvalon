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
type CharacterCard struct {
	ID              int64
	Name            string
	Description     string
	Personality     string
	Scenario        string
	FirstMessage    string
	ExampleMessages string
}

func ListCharacterCards(db *sql.DB) ([]CharacterCard, error) {
	rows, err := db.Query(`SELECT id, name, description, personality, scenario, first_message, example_messages FROM character_cards ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing character cards: %w", err)
	}
	defer rows.Close()

	var cards []CharacterCard
	for rows.Next() {
		var c CharacterCard
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.Personality, &c.Scenario, &c.FirstMessage, &c.ExampleMessages); err != nil {
			return nil, fmt.Errorf("scanning character card: %w", err)
		}
		cards = append(cards, c)
	}
	return cards, rows.Err()
}

func GetCharacterCard(db *sql.DB, id int64) (CharacterCard, error) {
	var c CharacterCard
	err := db.QueryRow(
		`SELECT id, name, description, personality, scenario, first_message, example_messages FROM character_cards WHERE id = ?`, id,
	).Scan(&c.ID, &c.Name, &c.Description, &c.Personality, &c.Scenario, &c.FirstMessage, &c.ExampleMessages)
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
		`INSERT INTO character_cards (name, description, personality, scenario, first_message, example_messages, raw_json) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.Description, c.Personality, c.Scenario, c.FirstMessage, c.ExampleMessages, string(rawJSON),
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
		`UPDATE character_cards SET name = ?, description = ?, personality = ?, scenario = ?, first_message = ?, example_messages = ?, raw_json = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		c.Name, c.Description, c.Personality, c.Scenario, c.FirstMessage, c.ExampleMessages, string(rawJSON), c.ID,
	)
	if err != nil {
		return fmt.Errorf("updating character card %d: %w", c.ID, err)
	}
	return nil
}

func DeleteCharacterCard(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM character_cards WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting character card %d: %w", id, err)
	}
	return nil
}

// ListCharacterCardsForSession returns the characters attached to a
// session, in turn_order.
func ListCharacterCardsForSession(db *sql.DB, sessionID int64) ([]CharacterCard, error) {
	rows, err := db.Query(`
		SELECT cc.id, cc.name, cc.description, cc.personality, cc.scenario, cc.first_message, cc.example_messages
		FROM character_cards cc
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
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.Personality, &c.Scenario, &c.FirstMessage, &c.ExampleMessages); err != nil {
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
