package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// TestRepeatedTailRun covers the pure detector behind the runaway-
// repetition guard (Lord: "l'App enchaîne les World: World: World:
// World: jusqu'à ce que je stoppe llama.cpp").
func TestRepeatedTailRun(t *testing.T) {
	t.Run("short phrase repeated enough times is caught", func(t *testing.T) {
		s := "Some real narration first. " + strings.Repeat("World: ", 10)
		pattern, repeats := repeatedTailRun(s, runawayPeriodMin, runawayPeriodMax, runawayMinRepeats)
		if pattern != "World: " || repeats != 10 {
			t.Fatalf("got pattern=%q repeats=%d", pattern, repeats)
		}
	})

	t.Run("normal prose never trips it", func(t *testing.T) {
		s := "The knight walked into the hall. The hall was quiet, the torches low, and nobody spoke a word as he passed."
		if pattern, repeats := repeatedTailRun(s, runawayPeriodMin, runawayPeriodMax, runawayMinRepeats); repeats != 0 {
			t.Fatalf("false positive: pattern=%q repeats=%d", pattern, repeats)
		}
	})

	t.Run("below the repeat threshold doesn't trip it", func(t *testing.T) {
		s := strings.Repeat("World: ", runawayMinRepeats-1)
		if _, repeats := repeatedTailRun(s, runawayPeriodMin, runawayPeriodMax, runawayMinRepeats); repeats != 0 {
			t.Fatalf("tripped below threshold: repeats=%d", repeats)
		}
	})

	t.Run("a single repeated character counts too", func(t *testing.T) {
		s := strings.Repeat("a", 50)
		if pattern, repeats := repeatedTailRun(s, runawayPeriodMin, runawayPeriodMax, runawayMinRepeats); pattern == "" || repeats == 0 {
			t.Fatalf("expected a hit, got pattern=%q repeats=%d", pattern, repeats)
		}
	})
}

func TestStripTrailingRepeats(t *testing.T) {
	got := stripTrailingRepeats("The tale begins. "+strings.Repeat("World: ", 9), "World: ")
	if got != "The tale begins. " {
		t.Fatalf("got %q", got)
	}
	// Nothing to strip: untouched.
	if got := stripTrailingRepeats("clean text", "World: "); got != "clean text" {
		t.Fatalf("got %q", got)
	}
}

// TestStreamWorldReplyAbortsOnRunawayRepetition is the end-to-end check:
// a fake backend that degenerates into repeating "World: " (the exact
// symptom Lord hit after a context overflow) must get its connection
// cancelled instead of streaming forever, and the saved message must
// not contain the repeated garbage.
func TestStreamWorldReplyAbortsOnRunawayRepetition(t *testing.T) {
	disconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"A real sentence. \"}}]}\n\n")
		flusher.Flush()
		for i := 0; i < 200; i++ {
			select {
			case <-r.Context().Done():
				close(disconnected)
				return
			default:
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"World: \"}}]}\n\n")
			flusher.Flush()
		}
	}))
	defer server.Close()

	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	profileID, err := db.CreateBackendProfile(sqlDB, db.BackendProfile{Name: "fake", Kind: "llm", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := db.CreateSession(sqlDB, db.Session{Title: "T", BackendProfileID: profileID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateMessage(sqlDB, db.ChatMessage{SessionID: sessionID, Role: "user", Content: "Go on."}); err != nil {
		t.Fatal(err)
	}
	session, err := db.GetSession(sqlDB, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := db.GetBackendProfile(sqlDB, profileID)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := db.ListMessages(sqlDB, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	h := ChatHandlers{DB: sqlDB}
	r := httptest.NewRequest("GET", "/x", nil)
	w := httptest.NewRecorder()
	var emitted strings.Builder
	msg, ok := h.streamWorldReply(r, w, session, profile, db.DefaultPersona, nil, messages,
		func(s string) { emitted.WriteString(s) },
		func(err error) { t.Fatalf("emitError called: %v", err) },
	)
	if !ok {
		t.Fatal("streamWorldReply reported failure for what should be a graceful abort")
	}

	select {
	case <-disconnected:
	default:
		t.Fatal("the fake backend never saw its context cancelled — the repetition guard didn't fire")
	}

	if strings.Contains(msg.Content, strings.Repeat("World: ", runawayMinRepeats)) {
		t.Fatalf("saved message still contains the repeated garbage: %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "A real sentence.") {
		t.Fatalf("the real content before the loop should survive: %q", msg.Content)
	}
	// RunawayRepetitionNotice is written straight to w via emitRaw (same
	// as the real Stream handler's OOB notices), not through the emit
	// callback — so it shows up in the response body, not `emitted`.
	if !strings.Contains(w.Body.String(), "stopped early") {
		t.Fatalf("expected a user-visible notice in the response, got: %q", w.Body.String())
	}
}
