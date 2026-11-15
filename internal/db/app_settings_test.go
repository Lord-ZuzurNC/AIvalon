package db

import (
	"path/filepath"
	"testing"
)

func TestAppSettings(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	// The migration seeds row id=1 on a fresh DB — a fresh install should
	// already have a usable default without the user doing anything.
	got, err := GetAppSettings(sqlDB)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SystemPromptTemplate == "" {
		t.Fatal("expected a non-empty seeded default template")
	}
	if got.SystemPromptTemplate != DefaultSystemPromptTemplate {
		t.Fatal("seeded migration template and DefaultSystemPromptTemplate constant have drifted apart")
	}

	if err := UpdateAppSettings(sqlDB, AppSettings{SystemPromptTemplate: "Custom template {{persona_name}}"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = GetAppSettings(sqlDB)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.SystemPromptTemplate != "Custom template {{persona_name}}" {
		t.Fatalf("got %q", got.SystemPromptTemplate)
	}
}
