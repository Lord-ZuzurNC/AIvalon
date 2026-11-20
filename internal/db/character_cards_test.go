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
		AuthorNote:   "Works best with a model that follows third-person narration closely.",
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
	if got.AuthorNote != "Works best with a model that follows third-person narration closely." {
		t.Fatalf("AuthorNote round-trip failed: got %+v", got)
	}

	got.Personality = "Wise, cryptic, impatient."
	if err := UpdateCharacterCard(sqlDB, got); err != nil {
		t.Fatalf("Update: %v", err)
	}

	cards, err := ListCharacterCards(sqlDB, -1, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cards) != 1 || cards[0].Personality != "Wise, cryptic, impatient." {
		t.Fatalf("List after update returned %+v", cards)
	}

	if err := DeleteCharacterCard(sqlDB, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	cards, _ = ListCharacterCards(sqlDB, -1, 0)
	if len(cards) != 0 {
		t.Fatalf("expected no cards after delete, got %+v", cards)
	}
}

func TestCharacterCardImageGallery(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	id, err := CreateCharacterCard(sqlDB, CharacterCard{Name: "Merlin"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := GetCharacterCard(sqlDB, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ImagePath != "" {
		t.Fatalf("expected no image by default, got %q", got.ImagePath)
	}
	has, err := HasPrimaryImage(sqlDB, id)
	if err != nil {
		t.Fatalf("HasPrimaryImage: %v", err)
	}
	if has {
		t.Fatalf("expected no primary image yet")
	}

	// First upload: becomes the gallery's only entry.
	img1ID, err := CreateCharacterCardImage(sqlDB, id, "")
	if err != nil {
		t.Fatalf("CreateCharacterCardImage: %v", err)
	}
	if err := UpdateCharacterCardImagePath(sqlDB, img1ID, "uploads/characters/1.png"); err != nil {
		t.Fatalf("UpdateCharacterCardImagePath: %v", err)
	}
	// Simulates handlers_character.go's saveImage: first image on a
	// card auto-becomes primary.
	if err := SetPrimaryCharacterCardImage(sqlDB, id, img1ID); err != nil {
		t.Fatalf("SetPrimaryCharacterCardImage: %v", err)
	}

	got, err = GetCharacterCard(sqlDB, id)
	if err != nil {
		t.Fatalf("Get after first image: %v", err)
	}
	if got.ImagePath != "uploads/characters/1.png" {
		t.Fatalf("ImagePath did not resolve to the primary image: got %+v", got)
	}
	if got.Name != "Merlin" {
		t.Fatalf("image update touched unrelated fields: got %+v", got)
	}

	// Second upload: joins the gallery as secondary — ImagePath (the
	// primary) must not change just because another image exists.
	img2ID, err := CreateCharacterCardImage(sqlDB, id, "uploads/characters/2.png")
	if err != nil {
		t.Fatalf("CreateCharacterCardImage (second): %v", err)
	}
	got, _ = GetCharacterCard(sqlDB, id)
	if got.ImagePath != "uploads/characters/1.png" {
		t.Fatalf("adding a secondary image changed the primary: got %+v", got)
	}

	images, err := ListCharacterCardImages(sqlDB, id)
	if err != nil {
		t.Fatalf("ListCharacterCardImages: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("expected 2 gallery images, got %+v", images)
	}

	// Promote the second image to primary.
	if err := SetPrimaryCharacterCardImage(sqlDB, id, img2ID); err != nil {
		t.Fatalf("SetPrimaryCharacterCardImage (promote): %v", err)
	}
	got, _ = GetCharacterCard(sqlDB, id)
	if got.ImagePath != "uploads/characters/2.png" {
		t.Fatalf("promotion did not swap the primary: got %+v", got)
	}

	// Deleting the now-primary image clears the pointer (ON DELETE SET
	// NULL) rather than leaving a dangling reference.
	if err := DeleteCharacterCardImage(sqlDB, img2ID); err != nil {
		t.Fatalf("DeleteCharacterCardImage: %v", err)
	}
	got, _ = GetCharacterCard(sqlDB, id)
	if got.ImagePath != "" {
		t.Fatalf("expected primary pointer to clear after deleting it, got %+v", got)
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
