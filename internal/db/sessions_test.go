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
