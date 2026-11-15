package web

import (
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

func TestNamePrefixed(t *testing.T) {
	merlin := db.CharacterCard{ID: 1, Name: "Merlin"}
	characters := []db.CharacterCard{merlin}

	t.Run("a character message gets its speaker's name prefixed back in", func(t *testing.T) {
		got := namePrefixed(db.ChatMessage{Role: "character", CharacterCardID: merlin.ID, Content: "Welcome."}, characters)
		if got != "Merlin: Welcome." {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("a narrator message passes through untouched — it's already a coherent passage", func(t *testing.T) {
		got := namePrefixed(db.ChatMessage{Role: "narrator", Content: "Merlin nods. Merlinette waves back."}, characters)
		if got != "Merlin nods. Merlinette waves back." {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("a user message is untouched regardless of characters", func(t *testing.T) {
		got := namePrefixed(db.ChatMessage{Role: "user", Content: "Hello?"}, characters)
		if got != "Hello?" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestHistoryRole(t *testing.T) {
	if got := historyRole(db.ChatMessage{Role: "user"}); got != "user" {
		t.Fatalf("got %q", got)
	}
	for _, role := range []string{"character", "narrator", "system"} {
		if got := historyRole(db.ChatMessage{Role: role}); got != "assistant" {
			t.Fatalf("role %q: got %q, want assistant", role, got)
		}
	}
}

func TestBuildNarratorPrompt(t *testing.T) {
	persona := db.Persona{Name: "Asteroid Destroyer", Description: "a curious engineer"}
	merlin := db.CharacterCard{Name: "Merlin", Description: "An ancient wizard."}
	merlinette := db.CharacterCard{Name: "Merlinette", Description: "His apprentice."}
	prompt := buildNarratorPrompt(persona, []db.CharacterCard{merlin, merlinette})

	// This is the whole point of the narrator redesign: the prompt must
	// explicitly allow narrating MULTIPLE characters and MUST forbid
	// speaking for the persona — unlike the old per-character prompt,
	// there's no "voice only X" restriction to assert here.
	for _, want := range []string{"not any single character", persona.Name, merlin.Name, merlinette.Name, "not limited to one"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("narrator prompt missing %q:\n%s", want, prompt)
		}
	}
}
