package web

import (
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

func TestResolveLorebookContent(t *testing.T) {
	neverRoll := func(int) bool { return false }
	alwaysRoll := func(int) bool { return true }

	t.Run("constant entry is always active regardless of the message", func(t *testing.T) {
		entries := []db.LorebookEntry{
			{Content: "The sky is always violet here.", TriggerStrategy: "constant", Probability: 100, Enabled: true},
		}
		got := resolveLorebookContent(entries, "totally unrelated message", neverRoll)
		if got != "The sky is always violet here." {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("selective entry only triggers on a matching key, whole-word", func(t *testing.T) {
		entries := []db.LorebookEntry{
			{Keys: []string{"tower"}, Content: "The tower is warded.", TriggerStrategy: "selective", Probability: 100, Enabled: true},
		}
		if got := resolveLorebookContent(entries, "I walk toward the tower.", neverRoll); got != "The tower is warded." {
			t.Fatalf("got %q, want the entry triggered", got)
		}
		if got := resolveLorebookContent(entries, "I walk away.", neverRoll); got != "" {
			t.Fatalf("got %q, want nothing triggered", got)
		}
	})

	t.Run("a short key doesn't false-positive inside a longer word (Merlin/Merlinette trap)", func(t *testing.T) {
		entries := []db.LorebookEntry{
			{Keys: []string{"Merlin"}, Content: "Merlin's tower lore.", TriggerStrategy: "selective", Probability: 100, Enabled: true},
		}
		if got := resolveLorebookContent(entries, "Merlinette, are you there?", neverRoll); got != "" {
			t.Fatalf("got %q, want the Merlin key to NOT match inside Merlinette", got)
		}
	})

	t.Run("disabled entries never trigger, even if constant", func(t *testing.T) {
		entries := []db.LorebookEntry{
			{Content: "Should never appear.", TriggerStrategy: "constant", Probability: 100, Enabled: false},
		}
		if got := resolveLorebookContent(entries, "anything", alwaysRoll); got != "" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("probability 100 never consults roll; below 100 does", func(t *testing.T) {
		entries := []db.LorebookEntry{
			{Content: "Full.", TriggerStrategy: "constant", Probability: 100, Enabled: true},
			{Content: "Dampened.", TriggerStrategy: "constant", Probability: 50, Enabled: true, InsertionOrder: 1},
		}
		if got := resolveLorebookContent(entries, "", neverRoll); got != "Full." {
			t.Fatalf("got %q, want only the 100%% entry since roll always declines", got)
		}
		if got := resolveLorebookContent(entries, "", alwaysRoll); got != "Full.\n\nDampened." {
			t.Fatalf("got %q, want both since roll always accepts", got)
		}
	})

	t.Run("insertion order controls concatenation order", func(t *testing.T) {
		entries := []db.LorebookEntry{
			{Content: "Second.", TriggerStrategy: "constant", Probability: 100, Enabled: true, InsertionOrder: 2},
			{Content: "First.", TriggerStrategy: "constant", Probability: 100, Enabled: true, InsertionOrder: 1},
		}
		if got := resolveLorebookContent(entries, "", neverRoll); got != "First.\n\nSecond." {
			t.Fatalf("got %q", got)
		}
	})
}

func TestLastUserMessageContent(t *testing.T) {
	history := []db.ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "narrator", Content: "reply"},
		{Role: "user", Content: "second"},
	}
	if got := lastUserMessageContent(history); got != "second" {
		t.Fatalf("got %q", got)
	}
	if got := lastUserMessageContent(nil); got != "" {
		t.Fatalf("got %q", got)
	}
}
