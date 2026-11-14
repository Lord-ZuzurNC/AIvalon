package db

import (
	"path/filepath"
	"testing"
)

func TestCharacterCardCRUD(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	id, err := CreateCharacterCard(sqlDB, CharacterCard{
		Name:         "Merlin",
		Description:  "An ancient wizard.",
		Personality:  "Wise, cryptic.",
		Scenario:     "A tower full of arcane tomes.",
		FirstMessage: "Ah, a visitor.",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := GetCharacterCard(sqlDB, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Merlin" || got.FirstMessage != "Ah, a visitor." {
		t.Fatalf("Get returned %+v", got)
	}

	got.Personality = "Wise, cryptic, impatient."
	if err := UpdateCharacterCard(sqlDB, got); err != nil {
		t.Fatalf("Update: %v", err)
	}

	cards, err := ListCharacterCards(sqlDB)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cards) != 1 || cards[0].Personality != "Wise, cryptic, impatient." {
		t.Fatalf("List after update returned %+v", cards)
	}

	if err := DeleteCharacterCard(sqlDB, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	cards, _ = ListCharacterCards(sqlDB)
	if len(cards) != 0 {
		t.Fatalf("expected no cards after delete, got %+v", cards)
	}
}

func TestAttachCharacterToSession(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	profileID, _ := CreateBackendProfile(sqlDB, BackendProfile{Name: "p", Kind: "llm", BaseURL: "http://x"})
	sessionID, _ := CreateSession(sqlDB, Session{Title: "t", BackendProfileID: profileID})
	cardID, _ := CreateCharacterCard(sqlDB, CharacterCard{Name: "Merlin"})

	if err := AttachCharacterToSession(sqlDB, sessionID, cardID, 0); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	cards, err := ListCharacterCardsForSession(sqlDB, sessionID)
	if err != nil {
		t.Fatalf("ListCharacterCardsForSession: %v", err)
	}
	if len(cards) != 1 || cards[0].Name != "Merlin" {
		t.Fatalf("got %+v", cards)
	}
}
