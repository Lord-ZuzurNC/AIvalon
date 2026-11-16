package db

import (
	"path/filepath"
	"testing"
)

func TestSessionAndMessageCRUD(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	profileID, err := CreateBackendProfile(sqlDB, BackendProfile{Name: "p", Kind: "llm", BaseURL: "http://x"})
	if err != nil {
		t.Fatalf("CreateBackendProfile: %v", err)
	}

	sessionID, err := CreateSession(sqlDB, Session{Title: "test chat", BackendProfileID: profileID})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, err := CreateMessage(sqlDB, ChatMessage{SessionID: sessionID, Role: "user", Content: "hi"}); err != nil {
		t.Fatalf("CreateMessage (user): %v", err)
	}
	if _, err := CreateMessage(sqlDB, ChatMessage{SessionID: sessionID, Role: "character", Content: "hello there"}); err != nil {
		t.Fatalf("CreateMessage (character): %v", err)
	}

	messages, err := ListMessages(sqlDB, sessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 2 || messages[0].Role != "user" || messages[1].Content != "hello there" {
		t.Fatalf("ListMessages returned %+v", messages)
	}

	sessions, err := ListSessions(sqlDB)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Title != "test chat" {
		t.Fatalf("ListSessions returned %+v", sessions)
	}
}

func TestDeleteSessionCascadesMessages(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	profileID, _ := CreateBackendProfile(sqlDB, BackendProfile{Name: "p", Kind: "llm", BaseURL: "http://x"})
	sessionID, _ := CreateSession(sqlDB, Session{Title: "doomed", BackendProfileID: profileID})
	if _, err := CreateMessage(sqlDB, ChatMessage{SessionID: sessionID, Role: "user", Content: "hi"}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	if err := DeleteSession(sqlDB, sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	sessions, err := ListSessions(sqlDB)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected no sessions after delete, got %+v", sessions)
	}

	// The real point of this test: ON DELETE CASCADE (schema) + the
	// foreign_keys pragma (Open) must actually be wired up together, or
	// this row survives as an orphan with a dangling session_id.
	messages, err := ListMessages(sqlDB, sessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected messages to cascade-delete with their session, got %+v", messages)
	}
}
