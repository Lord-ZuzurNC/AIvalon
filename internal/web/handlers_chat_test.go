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
	prompt := buildNarratorPrompt(db.DefaultSystemPromptTemplate, persona, []db.CharacterCard{merlin, merlinette}, "")

	// This is the whole point of the narrator redesign: the prompt must
	// explicitly allow narrating MULTIPLE characters and MUST forbid
	// speaking for the persona — unlike the old per-character prompt,
	// there's no "voice only X" restriction to assert here.
	for _, want := range []string{"not any single character", persona.Name, merlin.Name, merlinette.Name, "Never write"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("narrator prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "{{") {
		t.Errorf("a placeholder was left unsubstituted:\n%s", prompt)
	}
}

func TestBuildNarratorPromptWithNoCharacters(t *testing.T) {
	// Lord's explicit ask: a naked session (no cards) still goes through
	// the world/narrator, acting as a free-form GM that invents whoever
	// the scene needs instead of being told about fixed cards.
	persona := db.Persona{Name: "Asteroid Destroyer"}
	prompt := buildNarratorPrompt(db.DefaultSystemPromptTemplate, persona, nil, "")
	for _, want := range []string{"game master", "inventing names", "consistent"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("narrator prompt (no characters) missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "These are the fixed characters") {
		t.Errorf("narrator prompt (no characters) shouldn't claim there are predefined characters:\n%s", prompt)
	}
}

func TestBuildNarratorPromptWithCustomTemplate(t *testing.T) {
	// The whole point of this feature: Lord can rewrite the template
	// freely in Settings. A trivial custom template must still get its
	// placeholders substituted exactly like the default one.
	custom := "Narrator mode. Player: {{persona_name}}. Cast: {{characters}}. Lore: {{lorebook}}."
	persona := db.Persona{Name: "Z"}
	merlin := db.CharacterCard{Name: "Merlin", Description: "A wizard."}
	got := buildNarratorPrompt(custom, persona, []db.CharacterCard{merlin}, "The tower is warded")
	want := "Narrator mode. Player: Z. Cast: These are the fixed characters who live in this world, as described by their character cards:\nMerlin: A wizard.. Lore: The tower is warded."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderCharactersBlockIncludesAuthorNote(t *testing.T) {
	// Lord's clarification: "Author's Note" in his classic prompt isn't
	// a separate per-session feature — it's a note some card authors put
	// IN the character card's own JSON (the V2/V3 spec's creator_notes).
	merlin := db.CharacterCard{Name: "Merlin", Description: "A wizard.", AuthorNote: "Best run with a verbose model."}
	got := renderCharactersBlock([]db.CharacterCard{merlin})
	if !strings.Contains(got, "Best run with a verbose model.") {
		t.Errorf("author note missing from characters block:\n%s", got)
	}

	t.Run("omitted entirely when a card has none, no empty label left behind", func(t *testing.T) {
		gorak := db.CharacterCard{Name: "Gorak", Description: "A guard."}
		got := renderCharactersBlock([]db.CharacterCard{gorak})
		if strings.Contains(got, "Author's note") {
			t.Errorf("shouldn't mention an author's note when the card has none:\n%s", got)
		}
	})
}
