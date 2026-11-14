package db

import (
	"path/filepath"
	"testing"
)

func TestBackendProfileCRUD(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	id, err := CreateBackendProfile(sqlDB, BackendProfile{
		Name:        "local ollama",
		Kind:        "llm",
		BaseURL:     "http://127.0.0.1:11434/v1",
		Model:       "llama3.1",
		ModelParams: map[string]float64{"temperature": 0.8, "top_p": 0.9},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := GetBackendProfile(sqlDB, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "local ollama" || got.Model != "llama3.1" || got.ModelParams["temperature"] != 0.8 {
		t.Fatalf("Get returned %+v", got)
	}

	got.Name = "renamed"
	got.ModelParams["top_p"] = 0.95
	if err := UpdateBackendProfile(sqlDB, got); err != nil {
		t.Fatalf("Update: %v", err)
	}

	list, err := ListBackendProfiles(sqlDB)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Name != "renamed" || list[0].ModelParams["top_p"] != 0.95 {
		t.Fatalf("List after update returned %+v", list)
	}

	if err := DeleteBackendProfile(sqlDB, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list, err = ListBackendProfiles(sqlDB)
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected no profiles after delete, got %+v", list)
	}
}
