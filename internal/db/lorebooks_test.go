package db

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestLorebookCRUD(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	id, err := CreateLorebook(sqlDB, Lorebook{Name: "The Tower of Avalon"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := GetLorebook(sqlDB, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "The Tower of Avalon" {
		t.Fatalf("got %+v", got)
	}

	got.Name = "The Tower of Avalon, Renamed"
	if err := UpdateLorebook(sqlDB, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	books, err := ListLorebooks(sqlDB)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(books) != 1 || books[0].Name != "The Tower of Avalon, Renamed" {
		t.Fatalf("List after update returned %+v", books)
	}

	if err := DeleteLorebook(sqlDB, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	books, _ = ListLorebooks(sqlDB)
	if len(books) != 0 {
		t.Fatalf("expected no lorebooks after delete, got %+v", books)
	}
}

func TestLorebookEntryCRUD(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	bookID, err := CreateLorebook(sqlDB, Lorebook{Name: "World"})
	if err != nil {
		t.Fatalf("CreateLorebook: %v", err)
	}

	entry := LorebookEntry{
		LorebookID:      bookID,
		Keys:            []string{"tower", "arcane tomes"},
		Content:         "The tower has stood for a thousand years.",
		TriggerStrategy: "selective",
		InsertionOrder:  1,
		Probability:     100,
		Enabled:         true,
	}
	id, err := CreateLorebookEntry(sqlDB, entry)
	if err != nil {
		t.Fatalf("CreateLorebookEntry: %v", err)
	}

	got, err := GetLorebookEntry(sqlDB, id)
	if err != nil {
		t.Fatalf("GetLorebookEntry: %v", err)
	}
	if !reflect.DeepEqual(got.Keys, entry.Keys) {
		t.Fatalf("Keys round-trip: got %v, want %v", got.Keys, entry.Keys)
	}
	if got.Content != entry.Content || got.TriggerStrategy != "selective" || !got.Enabled {
		t.Fatalf("got %+v", got)
	}

	got.Enabled = false
	got.Probability = 50
	if err := UpdateLorebookEntry(sqlDB, got); err != nil {
		t.Fatalf("UpdateLorebookEntry: %v", err)
	}

	entries, err := ListLorebookEntries(sqlDB, bookID)
	if err != nil {
		t.Fatalf("ListLorebookEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Enabled || entries[0].Probability != 50 {
		t.Fatalf("List after update returned %+v", entries)
	}

	if err := DeleteLorebookEntry(sqlDB, id); err != nil {
		t.Fatalf("DeleteLorebookEntry: %v", err)
	}
	entries, _ = ListLorebookEntries(sqlDB, bookID)
	if len(entries) != 0 {
		t.Fatalf("expected no entries after delete, got %+v", entries)
	}
}
