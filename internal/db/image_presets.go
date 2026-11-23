package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ImagePreset is a saved set of txt2img parameters — see
// 00012_image_presets.sql for StyleSuffix's role.
//
// BackendProfileID (00016_image_preset_backend.sql) optionally ties the
// preset to one specific "image" kind backend — 0 means unset, and the
// generation dialog falls back to its manual backend picker.
type ImagePreset struct {
	ID               int64
	Name             string
	StyleSuffix      string
	NegativePrompt   string
	Steps            int
	CFGScale         float64
	Width            int
	Height           int
	SamplerName      string
	Seed             int64
	BackendProfileID int64
}

// ErrImagePresetExists is Create/UpdateImagePreset's answer to a name
// already taken (case-insensitively) — shown to the user, never clobbered.
var ErrImagePresetExists = errors.New("an image preset with that name already exists")

const imagePresetColumns = `id, name, style_suffix, negative_prompt, steps, cfg_scale, width, height, sampler_name, seed, COALESCE(backend_profile_id, 0)`

func ListImagePresets(db *sql.DB) ([]ImagePreset, error) {
	rows, err := db.Query(`SELECT ` + imagePresetColumns + ` FROM image_presets ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing image presets: %w", err)
	}
	defer rows.Close()
	var out []ImagePreset
	for rows.Next() {
		var p ImagePreset
		if err := rows.Scan(&p.ID, &p.Name, &p.StyleSuffix, &p.NegativePrompt, &p.Steps, &p.CFGScale, &p.Width, &p.Height, &p.SamplerName, &p.Seed, &p.BackendProfileID); err != nil {
			return nil, fmt.Errorf("scanning image preset: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func GetImagePreset(db *sql.DB, id int64) (ImagePreset, error) {
	var p ImagePreset
	err := db.QueryRow(`SELECT `+imagePresetColumns+` FROM image_presets WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.StyleSuffix, &p.NegativePrompt, &p.Steps, &p.CFGScale, &p.Width, &p.Height, &p.SamplerName, &p.Seed, &p.BackendProfileID)
	if err != nil {
		return ImagePreset{}, fmt.Errorf("getting image preset %d: %w", id, err)
	}
	return p, nil
}

func CreateImagePreset(db *sql.DB, p ImagePreset) (int64, error) {
	res, err := db.Exec(
		`INSERT INTO image_presets (name, style_suffix, negative_prompt, steps, cfg_scale, width, height, sampler_name, seed, backend_profile_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.StyleSuffix, p.NegativePrompt, p.Steps, p.CFGScale, p.Width, p.Height, p.SamplerName, p.Seed, nullID(p.BackendProfileID),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, ErrImagePresetExists
		}
		return 0, fmt.Errorf("creating image preset: %w", err)
	}
	return res.LastInsertId()
}

// UpdateImagePreset rewrites preset p.ID in place (name included).
func UpdateImagePreset(db *sql.DB, p ImagePreset) error {
	_, err := db.Exec(
		`UPDATE image_presets SET name = ?, style_suffix = ?, negative_prompt = ?, steps = ?, cfg_scale = ?, width = ?, height = ?, sampler_name = ?, seed = ?, backend_profile_id = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		p.Name, p.StyleSuffix, p.NegativePrompt, p.Steps, p.CFGScale, p.Width, p.Height, p.SamplerName, p.Seed, nullID(p.BackendProfileID), p.ID,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrImagePresetExists
		}
		return fmt.Errorf("updating image preset %d: %w", p.ID, err)
	}
	return nil
}

func DeleteImagePreset(db *sql.DB, id int64) error {
	if _, err := db.Exec(`DELETE FROM image_presets WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting image preset %d: %w", id, err)
	}
	return nil
}
