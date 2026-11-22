package db

import (
	"path/filepath"
	"strings"
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

	wantWhitelist := []string{"character-tavern.com", "aicharactercards.com", "charavault.net", "www.characterhub.org", "taverncard.com", "github.com", "chub.ai"}
	if strings.Join(got.PNGImportWhitelist, ",") != strings.Join(wantWhitelist, ",") {
		t.Fatalf("seeded whitelist = %q", got.PNGImportWhitelist)
	}
	if got.ContextBudgetChars != 24000 {
		t.Fatalf("seeded context budget = %d", got.ContextBudgetChars)
	}

	got.SystemPromptTemplate = "Custom template {{persona_name}}"
	if err := UpdateAppSettings(sqlDB, got); err != nil {
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

func TestSplitHostList(t *testing.T) {
	got := SplitHostList("Example.com\r\n\n  b.org, c.net\nexample.com ")
	if strings.Join(got, "|") != "example.com|b.org|c.net" {
		t.Fatalf("got %q", got)
	}
}
