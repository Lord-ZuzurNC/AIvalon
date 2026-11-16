package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// Lorebook is a session's optional "world info" book — at most one per
// session (sessions.lorebook_id), holding many LorebookEntry rows. See
// LorebookEntry for how an entry decides whether it's active on a given
// turn.
type Lorebook struct {
	ID   int64
	Name string
	// ImagePath mirrors CharacterCard.ImagePath — relative to
	// web/static, "" means no image uploaded.
	ImagePath string
}

// LorebookEntry.TriggerStrategy is "constant" (always included whenever
// its lorebook is attached) or "selective" (only included when one of
// Keys is matched in the recent conversation — see
// internal/web/handlers_chat.go's resolveLorebookContent for the matching
// logic). Probability (0-100) is rolled only for an entry that already
// triggered by the rules above — it's a further dampener ("include this
// 30% of the time it would otherwise apply"), not a separate trigger
// path; 100 (the default) always passes. InsertionOrder controls
// concatenation order among entries that are all active at once.
type LorebookEntry struct {
	ID              int64
	LorebookID      int64
	Keys            []string
	Content         string
	TriggerStrategy string
	InsertionOrder  int
	Probability     int
	Enabled         bool
}

func ListLorebooks(db *sql.DB) ([]Lorebook, error) {
	rows, err := db.Query(`SELECT id, name, image_path FROM lorebooks ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing lorebooks: %w", err)
	}
	defer rows.Close()

	var books []Lorebook
	for rows.Next() {
		var l Lorebook
		if err := rows.Scan(&l.ID, &l.Name, &l.ImagePath); err != nil {
			return nil, fmt.Errorf("scanning lorebook: %w", err)
		}
		books = append(books, l)
	}
	return books, rows.Err()
}

func GetLorebook(db *sql.DB, id int64) (Lorebook, error) {
	var l Lorebook
	err := db.QueryRow(`SELECT id, name, image_path FROM lorebooks WHERE id = ?`, id).Scan(&l.ID, &l.Name, &l.ImagePath)
	if err != nil {
		return Lorebook{}, fmt.Errorf("getting lorebook %d: %w", id, err)
	}
	return l, nil
}

func CreateLorebook(db *sql.DB, l Lorebook) (int64, error) {
	res, err := db.Exec(`INSERT INTO lorebooks (name) VALUES (?)`, l.Name)
	if err != nil {
		return 0, fmt.Errorf("creating lorebook: %w", err)
	}
	return res.LastInsertId()
}

func UpdateLorebook(db *sql.DB, l Lorebook) error {
	_, err := db.Exec(
		`UPDATE lorebooks SET name = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		l.Name, l.ID,
	)
	if err != nil {
		return fmt.Errorf("updating lorebook %d: %w", l.ID, err)
	}
	return nil
}

func DeleteLorebook(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM lorebooks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting lorebook %d: %w", id, err)
	}
	return nil
}

// UpdateLorebookImagePath mirrors UpdateCharacterCardImagePath — its own
// multipart upload request, separate from the plain-text rename form.
func UpdateLorebookImagePath(db *sql.DB, id int64, imagePath string) error {
	_, err := db.Exec(`UPDATE lorebooks SET image_path = ? WHERE id = ?`, imagePath, id)
	if err != nil {
		return fmt.Errorf("updating lorebook %d image: %w", id, err)
	}
	return nil
}

func ListLorebookEntries(db *sql.DB, lorebookID int64) ([]LorebookEntry, error) {
	rows, err := db.Query(
		`SELECT id, lorebook_id, keys, content, trigger_strategy, insertion_order, probability, enabled
		 FROM lorebook_entries WHERE lorebook_id = ? ORDER BY insertion_order, id`,
		lorebookID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing entries for lorebook %d: %w", lorebookID, err)
	}
	defer rows.Close()

	var entries []LorebookEntry
	for rows.Next() {
		var e LorebookEntry
		var keysJSON string
		var enabled int
		if err := rows.Scan(&e.ID, &e.LorebookID, &keysJSON, &e.Content, &e.TriggerStrategy, &e.InsertionOrder, &e.Probability, &enabled); err != nil {
			return nil, fmt.Errorf("scanning lorebook entry: %w", err)
		}
		if err := json.Unmarshal([]byte(keysJSON), &e.Keys); err != nil {
			return nil, fmt.Errorf("decoding keys for lorebook entry %d: %w", e.ID, err)
		}
		e.Enabled = enabled != 0
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func GetLorebookEntry(db *sql.DB, id int64) (LorebookEntry, error) {
	var e LorebookEntry
	var keysJSON string
	var enabled int
	err := db.QueryRow(
		`SELECT id, lorebook_id, keys, content, trigger_strategy, insertion_order, probability, enabled FROM lorebook_entries WHERE id = ?`, id,
	).Scan(&e.ID, &e.LorebookID, &keysJSON, &e.Content, &e.TriggerStrategy, &e.InsertionOrder, &e.Probability, &enabled)
	if err != nil {
		return LorebookEntry{}, fmt.Errorf("getting lorebook entry %d: %w", id, err)
	}
	if err := json.Unmarshal([]byte(keysJSON), &e.Keys); err != nil {
		return LorebookEntry{}, fmt.Errorf("decoding keys for lorebook entry %d: %w", e.ID, err)
	}
	e.Enabled = enabled != 0
	return e, nil
}

func CreateLorebookEntry(db *sql.DB, e LorebookEntry) (int64, error) {
	keysJSON, err := json.Marshal(e.Keys)
	if err != nil {
		return 0, fmt.Errorf("encoding keys: %w", err)
	}
	res, err := db.Exec(
		`INSERT INTO lorebook_entries (lorebook_id, keys, content, trigger_strategy, insertion_order, probability, enabled) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.LorebookID, string(keysJSON), e.Content, e.TriggerStrategy, e.InsertionOrder, e.Probability, boolToInt(e.Enabled),
	)
	if err != nil {
		return 0, fmt.Errorf("creating lorebook entry: %w", err)
	}
	return res.LastInsertId()
}

func UpdateLorebookEntry(db *sql.DB, e LorebookEntry) error {
	keysJSON, err := json.Marshal(e.Keys)
	if err != nil {
		return fmt.Errorf("encoding keys: %w", err)
	}
	_, err = db.Exec(
		`UPDATE lorebook_entries SET keys = ?, content = ?, trigger_strategy = ?, insertion_order = ?, probability = ?, enabled = ? WHERE id = ?`,
		string(keysJSON), e.Content, e.TriggerStrategy, e.InsertionOrder, e.Probability, boolToInt(e.Enabled), e.ID,
	)
	if err != nil {
		return fmt.Errorf("updating lorebook entry %d: %w", e.ID, err)
	}
	return nil
}

func DeleteLorebookEntry(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM lorebook_entries WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting lorebook entry %d: %w", id, err)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
