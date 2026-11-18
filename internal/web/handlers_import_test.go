package web

import "testing"

func TestUniqueName(t *testing.T) {
	taken := map[string]bool{"Avalon": true, "Avalon (2)": true}
	got := uniqueName("Avalon", taken)
	if got != "Avalon (3)" {
		t.Fatalf("expected the first free suffix, got %q", got)
	}
	if got := uniqueName("Camelot", taken); got != "Camelot" {
		t.Fatalf("expected an untaken name to pass through unchanged, got %q", got)
	}
}

func TestParseLorebookJSON_SillyTavernWorldInfo(t *testing.T) {
	raw := []byte(`{
		"entries": {
			"0": {"key": ["tower"], "content": "A tall tower.", "constant": false, "disable": false, "order": 1, "probability": 100},
			"1": {"key": [], "content": "Always on.", "constant": true, "disable": true, "order": 0, "probability": 100}
		}
	}`)
	name, entries, ok := parseLorebookJSON(raw)
	if !ok {
		t.Fatal("expected the SillyTavern World Info shape to be recognized")
	}
	if name != "" {
		t.Fatalf("SillyTavern's export has no top-level name field, expected \"\", got %q", name)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Sorted by numeric index: entry "0" first, then "1".
	if entries[0].TriggerStrategy != "selective" || entries[0].Keys[0] != "tower" || !entries[0].Enabled {
		t.Fatalf("entry 0 mapped wrong: %+v", entries[0])
	}
	if entries[1].TriggerStrategy != "constant" || entries[1].Enabled {
		t.Fatalf("entry 1 mapped wrong (disable:true must become Enabled:false): %+v", entries[1])
	}
}

func TestParseLorebookJSON_CharacterBookStandalone(t *testing.T) {
	raw := []byte(`{
		"name": "My Book",
		"entries": [
			{"keys": ["sword"], "content": "A sharp sword.", "enabled": true, "constant": false, "insertion_order": 3, "probability": 50}
		]
	}`)
	name, entries, ok := parseLorebookJSON(raw)
	if !ok || name != "My Book" || len(entries) != 1 {
		t.Fatalf("expected name=My Book, 1 entry, ok=true; got name=%q entries=%d ok=%v", name, len(entries), ok)
	}
	e := entries[0]
	if e.Keys[0] != "sword" || e.InsertionOrder != 3 || e.Probability != 50 || !e.Enabled || e.TriggerStrategy != "selective" {
		t.Fatalf("entry mapped wrong: %+v", e)
	}
}

func TestParseLorebookJSON_EmbeddedInCharacterCard(t *testing.T) {
	raw := []byte(`{
		"spec": "chara_card_v2",
		"spec_version": "2.0",
		"data": {
			"name": "Some Character",
			"character_book": {
				"name": "Embedded Book",
				"entries": [
					{"keys": ["x"], "content": "y", "enabled": true, "constant": true, "insertion_order": 0, "probability": 100}
				]
			}
		}
	}`)
	name, entries, ok := parseLorebookJSON(raw)
	if !ok || name != "Embedded Book" || len(entries) != 1 {
		t.Fatalf("expected the embedded character_book to be pulled out: name=%q entries=%d ok=%v", name, len(entries), ok)
	}
}

func TestParseLorebookJSON_Unrecognized(t *testing.T) {
	if _, _, ok := parseLorebookJSON([]byte(`{"just": "some json"}`)); ok {
		t.Fatal("expected an unrecognized shape to report ok=false, not silently import zero entries")
	}
	if _, _, ok := parseLorebookJSON([]byte(`not even json`)); ok {
		t.Fatal("expected invalid JSON to report ok=false")
	}
}

func TestParseCharacterJSON(t *testing.T) {
	raw := []byte(`{
		"spec": "chara_card_v2",
		"data": {
			"name": "Merlin",
			"description": "A wise wizard.",
			"first_mes": "Greetings.",
			"character_book": {"name": "Merlin's Book", "entries": [{"keys": ["magic"], "content": "...", "enabled": true}]}
		}
	}`)
	card, book, ok := parseCharacterJSON(raw)
	if !ok {
		t.Fatal("expected a well-formed character card to parse")
	}
	if card.Name != "Merlin" || card.Description != "A wise wizard." || card.FirstMessage != "Greetings." {
		t.Fatalf("card fields mapped wrong: %+v", card)
	}
	if book == nil || book.Name != "Merlin's Book" || len(book.Entries) != 1 {
		t.Fatalf("embedded book not carried through: %+v", book)
	}
}

func TestParseCharacterJSON_RejectsBareLorebook(t *testing.T) {
	// A bare SillyTavern World Info export has no "data" wrapper at
	// all — dropping it into the Character importer by mistake must
	// not fabricate an empty, nameless character.
	raw := []byte(`{"entries": {"0": {"key": [], "content": "x"}}}`)
	if _, _, ok := parseCharacterJSON(raw); ok {
		t.Fatal("expected a bare lorebook JSON to be rejected by the character parser")
	}
}
