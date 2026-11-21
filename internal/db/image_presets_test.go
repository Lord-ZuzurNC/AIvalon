package db

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestImagePresetsSeededAndCRUD(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	presets, err := ListImagePresets(sqlDB)
	if err != nil {
		t.Fatalf("ListImagePresets: %v", err)
	}
	seeded := map[string]ImagePreset{}
	for _, p := range presets {
		seeded[p.Name] = p
	}
	for _, name := range []string{"Realistic", "Manga / Anime", "Cyberpunk"} {
		p, ok := seeded[name]
		if !ok {
			t.Fatalf("built-in preset %q missing after fresh migration; got %+v", name, presets)
		}
		if p.StyleSuffix == "" || p.Steps == 0 || p.Width == 0 || p.SamplerName == "" || p.Seed != -1 {
			t.Errorf("built-in preset %q looks incomplete: %+v", name, p)
		}
	}

	mine := ImagePreset{Name: "Mine", StyleSuffix: "oil painting", Steps: 22, CFGScale: 6.5, Width: 640, Height: 640, SamplerName: "Euler", Seed: 99}
	id, err := CreateImagePreset(sqlDB, mine)
	if err != nil {
		t.Fatalf("CreateImagePreset: %v", err)
	}

	// Same name, different case: refused, and the original is untouched.
	if _, err := CreateImagePreset(sqlDB, ImagePreset{Name: "mine", Steps: 1}); !errors.Is(err, ErrImagePresetExists) {
		t.Fatalf("duplicate create: err = %v, want ErrImagePresetExists", err)
	}
	if got := findPreset(t, sqlDB, id); got.Steps != 22 || got.CFGScale != 6.5 || got.StyleSuffix != "oil painting" {
		t.Fatalf("original clobbered by refused duplicate: %+v", got)
	}

	mine.ID, mine.Steps, mine.NegativePrompt = id, 40, "ugly"
	if err := UpdateImagePreset(sqlDB, mine); err != nil {
		t.Fatalf("UpdateImagePreset: %v", err)
	}
	if got := findPreset(t, sqlDB, id); got.Steps != 40 || got.NegativePrompt != "ugly" {
		t.Fatalf("update didn't stick: %+v", got)
	}
	// Renaming onto another preset's name is refused too.
	mine.Name = "cyberpunk"
	if err := UpdateImagePreset(sqlDB, mine); !errors.Is(err, ErrImagePresetExists) {
		t.Fatalf("rename onto existing: err = %v", err)
	}

	if err := DeleteImagePreset(sqlDB, id); err != nil {
		t.Fatalf("DeleteImagePreset: %v", err)
	}
	after, _ := ListImagePresets(sqlDB)
	if len(after) != len(presets) {
		t.Fatalf("after delete: %d presets, want %d", len(after), len(presets))
	}
}

func findPreset(t *testing.T, sqlDB *sql.DB, id int64) ImagePreset {
	t.Helper()
	presets, err := ListImagePresets(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range presets {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("preset %d not found", id)
	return ImagePreset{}
}
