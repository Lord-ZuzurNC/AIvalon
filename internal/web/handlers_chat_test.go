package web

import (
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

func TestSelectRespondingCharacters(t *testing.T) {
	merlin := db.CharacterCard{ID: 1, Name: "Merlin"}
	merlinette := db.CharacterCard{ID: 2, Name: "Merlinette"}
	gorak := db.CharacterCard{ID: 3, Name: "Gorak"}
	characters := []db.CharacterCard{merlin, merlinette, gorak}

	t.Run("single or no character: unaffected", func(t *testing.T) {
		if got := selectRespondingCharacters([]db.CharacterCard{merlin}, nil); len(got) != 1 || got[0].ID != merlin.ID {
			t.Fatalf("got %+v", got)
		}
		if got := selectRespondingCharacters(nil, nil); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("addressing Merlinette does not also match Merlin (substring trap)", func(t *testing.T) {
		history := []db.ChatMessage{{Role: "user", Content: "Merlinette ! Are you there ?"}}
		got := selectRespondingCharacters(characters, history)
		if len(got) != 1 || got[0].ID != merlinette.ID {
			t.Fatalf("got %+v, want only Merlinette", got)
		}
	})

	t.Run("addressing by name picks that character even mid-conversation", func(t *testing.T) {
		history := []db.ChatMessage{
			{Role: "character", CharacterCardID: merlin.ID, Content: "Welcome, traveler."},
			{Role: "user", Content: "Gorak, stand down."},
		}
		got := selectRespondingCharacters(characters, history)
		if len(got) != 1 || got[0].ID != gorak.ID {
			t.Fatalf("got %+v, want only Gorak", got)
		}
	})

	t.Run("nobody addressed: continues with whoever spoke last", func(t *testing.T) {
		history := []db.ChatMessage{
			{Role: "user", Content: "Who goes there?"},
			{Role: "character", CharacterCardID: gorak.ID, Content: "State your business."},
			{Role: "user", Content: "Just passing through."},
		}
		got := selectRespondingCharacters(characters, history)
		if len(got) != 1 || got[0].ID != gorak.ID {
			t.Fatalf("got %+v, want only Gorak (last speaker)", got)
		}
	})

	t.Run("nobody addressed, nobody's spoken yet: falls back to the first attached", func(t *testing.T) {
		history := []db.ChatMessage{{Role: "user", Content: "Hello?"}}
		got := selectRespondingCharacters(characters, history)
		if len(got) != 1 || got[0].ID != merlin.ID {
			t.Fatalf("got %+v, want only Merlin (first attached)", got)
		}
	})
}
