package db

import (
	"database/sql"
	"fmt"
)

// Persona is the human player's own self-profile — see docs/DESIGN.md's
// vocabulary note (Character = AI-played, Persona = the user). A session
// with no Persona falls back to DefaultPersona below rather than requiring
// one to exist.
type Persona struct {
	ID          int64
	Name        string
	Description string
	ImagePath   string
	// ChatCount is populated by ListPersonas only: how many sessions
	// use this persona (and would be deleted along with it).
	ChatCount int
}

// DefaultPersona is used when a session has no persona_id set.
var DefaultPersona = Persona{Name: "User", Description: ""}

func ListPersonas(db *sql.DB) ([]Persona, error) {
	rows, err := db.Query(`SELECT p.id, p.name, p.description, p.image_path, (SELECT COUNT(*) FROM sessions s WHERE s.persona_id = p.id) FROM personas p ORDER BY p.name`)
	if err != nil {
		return nil, fmt.Errorf("listing personas: %w", err)
	}
	defer rows.Close()

	var personas []Persona
	for rows.Next() {
		var p Persona
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.ImagePath, &p.ChatCount); err != nil {
			return nil, fmt.Errorf("scanning persona: %w", err)
		}
		personas = append(personas, p)
	}
	return personas, rows.Err()
}

func GetPersona(db *sql.DB, id int64) (Persona, error) {
	var p Persona
	err := db.QueryRow(`SELECT id, name, description, image_path FROM personas WHERE id = ?`, id).Scan(&p.ID, &p.Name, &p.Description, &p.ImagePath)
	if err != nil {
		return Persona{}, fmt.Errorf("getting persona %d: %w", id, err)
	}
	return p, nil
}

func CreatePersona(db *sql.DB, p Persona) (int64, error) {
	res, err := db.Exec(`INSERT INTO personas (name, description) VALUES (?, ?)`, p.Name, p.Description)
	if err != nil {
		return 0, fmt.Errorf("creating persona: %w", err)
	}
	return res.LastInsertId()
}

func UpdatePersona(db *sql.DB, p Persona) error {
	_, err := db.Exec(
		`UPDATE personas SET name = ?, description = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		p.Name, p.Description, p.ID,
	)
	if err != nil {
		return fmt.Errorf("updating persona %d: %w", p.ID, err)
	}
	return nil
}

// UpdatePersonaImagePath is separate from UpdatePersona for the same
// reason as the character/lorebook equivalents: an image upload is
// handled by its own code path (see internal/web/image_upload.go),
// not folded into the plain-fields form update.
func UpdatePersonaImagePath(db *sql.DB, id int64, imagePath string) error {
	_, err := db.Exec(`UPDATE personas SET image_path = ? WHERE id = ?`, imagePath, id)
	if err != nil {
		return fmt.Errorf("updating persona %d image: %w", id, err)
	}
	return nil
}

// DeletePersona also deletes every session using it — see
// deleteWithSessions. The web handler confirms with the user first.
// Returns generated-image files now orphaned by that cascade — the
// caller is responsible for removing them from disk.
func DeletePersona(db *sql.DB, id int64) ([]string, error) {
	return deleteWithSessions(db, personaSessionIDs, "personas", id)
}
