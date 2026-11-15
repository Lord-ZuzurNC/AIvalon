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

	t.Run("single or no character: unaffected, classify never even consulted", func(t *testing.T) {
		panicky := func(string, []db.CharacterCard) (db.CharacterCard, bool) {
			t.Fatal("classify should not be called")
			return db.CharacterCard{}, false
		}
		if got := selectRespondingCharacters([]db.CharacterCard{merlin}, nil, panicky); len(got) != 1 || got[0].ID != merlin.ID {
			t.Fatalf("got %+v", got)
		}
		if got := selectRespondingCharacters(nil, nil, panicky); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("classify overrides a misleading name match: mentioned but not addressed", func(t *testing.T) {
		// Exactly Lord's real case: naming one character while actually
		// talking to another. A plain name search can't tell "addressed
		// to" from "mentioned in passing" — this is precisely why
		// classify gets first say.
		history := []db.ChatMessage{{Role: "user", Content: "Tell Merlin that Merlinette should calm down."}}
		classify := func(message string, candidates []db.CharacterCard) (db.CharacterCard, bool) { return merlin, true }
		got := selectRespondingCharacters(characters, history, classify)
		if len(got) != 1 || got[0].ID != merlin.ID {
			t.Fatalf("got %+v, want only Merlin (per classify, despite Merlinette also being named)", got)
		}
	})

	t.Run("classify declines (NONE): falls back to plain name match", func(t *testing.T) {
		history := []db.ChatMessage{{Role: "user", Content: "Merlinette ! Are you there ?"}}
		declines := func(string, []db.CharacterCard) (db.CharacterCard, bool) { return db.CharacterCard{}, false }
		got := selectRespondingCharacters(characters, history, declines)
		if len(got) != 1 || got[0].ID != merlinette.ID {
			t.Fatalf("got %+v, want only Merlinette", got)
		}
	})

	t.Run("no classifier at all (e.g. backend unavailable): same fallback chain still works", func(t *testing.T) {
		history := []db.ChatMessage{{Role: "user", Content: "Merlinette ! Are you there ?"}}
		got := selectRespondingCharacters(characters, history, nil)
		if len(got) != 1 || got[0].ID != merlinette.ID {
			t.Fatalf("got %+v, want only Merlinette (substring trap still avoided)", got)
		}
	})

	t.Run("addressing by name picks that character even mid-conversation", func(t *testing.T) {
		history := []db.ChatMessage{
			{Role: "character", CharacterCardID: merlin.ID, Content: "Welcome, traveler."},
			{Role: "user", Content: "Gorak, stand down."},
		}
		got := selectRespondingCharacters(characters, history, nil)
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
		got := selectRespondingCharacters(characters, history, nil)
		if len(got) != 1 || got[0].ID != gorak.ID {
			t.Fatalf("got %+v, want only Gorak (last speaker)", got)
		}
	})

	t.Run("nobody addressed, nobody's spoken yet: falls back to the first attached", func(t *testing.T) {
		history := []db.ChatMessage{{Role: "user", Content: "Hello?"}}
		got := selectRespondingCharacters(characters, history, nil)
		if len(got) != 1 || got[0].ID != merlin.ID {
			t.Fatalf("got %+v, want only Merlin (first attached)", got)
		}
	})
}

func TestParseAddresseeReply(t *testing.T) {
	merlin := db.CharacterCard{ID: 1, Name: "Merlin"}
	merlinette := db.CharacterCard{ID: 2, Name: "Merlinette"}
	candidates := []db.CharacterCard{merlin, merlinette}

	cases := []struct {
		name, reply string
		wantID      int64
		wantOK      bool
	}{
		{"exact name", "Merlin", merlin.ID, true},
		{"trailing punctuation the model adds despite instructions", "Merlin.", merlin.ID, true},
		{"surrounding whitespace", "  Merlinette  \n", merlinette.ID, true},
		{"case-insensitive", "merlin", merlin.ID, true},
		{"explicit NONE", "NONE", 0, false},
		{"garbage answer", "I'm not sure who", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseAddresseeReply(c.reply, candidates)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if ok && got.ID != c.wantID {
				t.Fatalf("got ID %d, want %d", got.ID, c.wantID)
			}
		})
	}
}
