package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
	"github.com/Lord-ZuzurNC/AIvalon/internal/llmclient"
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

func TestTruncateHistoryToBudget(t *testing.T) {
	msg := func(s string) llmclient.Message { return llmclient.Message{Role: "user", Content: s} }
	history := []llmclient.Message{msg("aaaaaaaaaa"), msg("bbbbbbbbbb"), msg("cccccccccc"), msg("dddddddddd")} // 4 × 10 chars

	// Budget 0 = no limit, even when far over.
	if kept, dropped := truncateHistoryToBudget(history, 1000, 0); len(kept) != 4 || dropped != 0 {
		t.Fatalf("budget 0 must not truncate, kept %d dropped %d", len(kept), dropped)
	}

	// system 15 + 40 history = 55 > 40: drop oldest-first until it fits
	// (15+30=45 still over, 15+20=35 fits) → a and b gone.
	kept, dropped := truncateHistoryToBudget(history, 15, 40)
	if dropped != 2 || len(kept) != 2 || kept[0].Content != "cccccccccc" || kept[1].Content != "dddddddddd" {
		t.Fatalf("expected the two oldest dropped, got dropped=%d kept=%+v", dropped, kept)
	}

	// Already fits: untouched.
	if kept, dropped := truncateHistoryToBudget(history, 0, 40); len(kept) != 4 || dropped != 0 {
		t.Fatalf("fitting history must be untouched, kept %d dropped %d", len(kept), dropped)
	}

	// System prompt alone over budget: history shrinks to just the
	// latest message, the system prompt itself is never what's cut.
	kept, dropped = truncateHistoryToBudget(history, 500, 100)
	if dropped != 3 || len(kept) != 1 || kept[0].Content != "dddddddddd" {
		t.Fatalf("expected only the latest message kept, got dropped=%d kept=%+v", dropped, kept)
	}

	// Counts runes, not bytes: 10 × "é" is 10 chars (20 bytes).
	if _, dropped := truncateHistoryToBudget([]llmclient.Message{msg("éééééééééé"), msg("x")}, 0, 11); dropped != 0 {
		t.Fatalf("multi-byte text over-counted, dropped %d", dropped)
	}
}

func TestBuildNarratorMessagesKeepsSystemPromptOverBudget(t *testing.T) {
	history := []db.ChatMessage{{Role: "user", Content: "old"}, {Role: "narrator", Content: "reply"}, {Role: "user", Content: "latest"}}
	msgs, dropped := buildNarratorMessages("a very long system prompt that is over budget by itself", db.DefaultPersona, nil, "", history, 10)
	if msgs[0].Role != "system" || !strings.Contains(msgs[0].Content, "very long system prompt") {
		t.Fatalf("system message must be first and untouched, got %+v", msgs[0])
	}
	if dropped != 2 || len(msgs) != 2 || msgs[1].Content != "latest" {
		t.Fatalf("expected system + latest only, got dropped=%d msgs=%+v", dropped, msgs)
	}
}

// TestChatCreate_RejectsImageKindBackend is the regression test for
// Lord's real report: nothing stopped a session from being created on
// an "image" kind backend (A1111/Forge), which then has no chat API to
// talk to at all. Create must reject it server-side even if a stale
// client somehow still offers one (the picker itself is now filtered
// too — see ChatHandlers.List's llmProfiles filter).
func TestChatCreate_RejectsImageKindBackend(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	imageProfile, err := db.CreateBackendProfile(sqlDB, db.BackendProfile{Name: "Forge", Kind: "image", BaseURL: "http://127.0.0.1:7860"})
	if err != nil {
		t.Fatal(err)
	}
	llmProfile, err := db.CreateBackendProfile(sqlDB, db.BackendProfile{Name: "Ollama", Kind: "llm", BaseURL: "http://127.0.0.1:11434"})
	if err != nil {
		t.Fatal(err)
	}

	h := ChatHandlers{DB: sqlDB}
	create := func(profileID int64) *httptest.ResponseRecorder {
		form := url.Values{"backend_profile_id": {strconv.FormatInt(profileID, 10)}, "title": {"Test"}}
		req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.Create(w, req)
		return w
	}

	if w := create(imageProfile); w.Code != http.StatusBadRequest {
		t.Fatalf("creating a session on an image-kind backend: status = %d, want 400", w.Code)
	}
	if n, err := db.CountSessions(sqlDB); err != nil || n != 0 {
		t.Fatalf("an image-kind backend must not create a session, got n=%d err=%v", n, err)
	}

	if w := create(llmProfile); w.Code != http.StatusSeeOther {
		t.Fatalf("creating a session on an llm-kind backend: status = %d, want 303", w.Code)
	}
	if n, err := db.CountSessions(sqlDB); err != nil || n != 1 {
		t.Fatalf("expected exactly one session after the valid create, got n=%d err=%v", n, err)
	}
}
