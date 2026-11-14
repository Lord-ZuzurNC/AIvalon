// Package db also holds the data-access functions for AIvalon's tables.
// Kept as plain functions over *sql.DB rather than a repository interface:
// there's one implementation (SQLite) and no reason yet to abstract it.
package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// BackendProfile is a named connection to an OpenAI-compatible backend
// (Ollama, llama.cpp's server, a hosted API, ...). See docs/DESIGN.md.
type BackendProfile struct {
	ID          int64
	Name        string
	Kind        string // "llm" in v1; "image" from v2
	BaseURL     string
	APIKey      string             // may be empty; stored plain, see docs/DESIGN.md
	ModelParams map[string]float64 // e.g. {"temperature": 0.8, "top_p": 0.9, "top_k": 40, "max_tokens": 512}
}

func ListBackendProfiles(db *sql.DB) ([]BackendProfile, error) {
	rows, err := db.Query(`SELECT id, name, kind, base_url, api_key, model_params FROM backend_profiles ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing backend profiles: %w", err)
	}
	defer rows.Close()

	var profiles []BackendProfile
	for rows.Next() {
		p, err := scanBackendProfile(rows)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

func GetBackendProfile(db *sql.DB, id int64) (BackendProfile, error) {
	row := db.QueryRow(`SELECT id, name, kind, base_url, api_key, model_params FROM backend_profiles WHERE id = ?`, id)
	return scanBackendProfile(row)
}

func CreateBackendProfile(db *sql.DB, p BackendProfile) (int64, error) {
	params, err := json.Marshal(p.ModelParams)
	if err != nil {
		return 0, fmt.Errorf("encoding model params: %w", err)
	}
	res, err := db.Exec(
		`INSERT INTO backend_profiles (name, kind, base_url, api_key, model_params) VALUES (?, ?, ?, ?, ?)`,
		p.Name, p.Kind, p.BaseURL, p.APIKey, string(params),
	)
	if err != nil {
		return 0, fmt.Errorf("creating backend profile: %w", err)
	}
	return res.LastInsertId()
}

func UpdateBackendProfile(db *sql.DB, p BackendProfile) error {
	params, err := json.Marshal(p.ModelParams)
	if err != nil {
		return fmt.Errorf("encoding model params: %w", err)
	}
	_, err = db.Exec(
		`UPDATE backend_profiles SET name = ?, kind = ?, base_url = ?, api_key = ?, model_params = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		p.Name, p.Kind, p.BaseURL, p.APIKey, string(params), p.ID,
	)
	if err != nil {
		return fmt.Errorf("updating backend profile %d: %w", p.ID, err)
	}
	return nil
}

func DeleteBackendProfile(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM backend_profiles WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting backend profile %d: %w", id, err)
	}
	return nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanBackendProfile(row rowScanner) (BackendProfile, error) {
	var p BackendProfile
	var apiKey sql.NullString
	var paramsJSON string
	if err := row.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &apiKey, &paramsJSON); err != nil {
		return BackendProfile{}, fmt.Errorf("scanning backend profile: %w", err)
	}
	p.APIKey = apiKey.String
	if paramsJSON != "" {
		if err := json.Unmarshal([]byte(paramsJSON), &p.ModelParams); err != nil {
			return BackendProfile{}, fmt.Errorf("decoding model params: %w", err)
		}
	}
	return p, nil
}
