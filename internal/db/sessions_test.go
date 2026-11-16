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

func TestMessageEditDeleteCascade(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	profileID, _ := CreateBackendProfile(sqlDB, BackendProfile{Name: "p", Kind: "llm", BaseURL: "http://x"})
	sessionID, _ := CreateSession(sqlDB, Session{Title: "chat", BackendProfileID: profileID})

	userID, err := CreateMessage(sqlDB, ChatMessage{SessionID: sessionID, Role: "user", Content: "hi"})
	if err != nil {
		t.Fatalf("CreateMessage (user): %v", err)
	}
	narratorID, err := CreateMessage(sqlDB, ChatMessage{SessionID: sessionID, Role: "narrator", Content: "hello there"})
	if err != nil {
		t.Fatalf("CreateMessage (narrator): %v", err)
	}

	// GetMessage round-trips a single row.
	got, err := GetMessage(sqlDB, userID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.Content != "hi" || got.Role != "user" {
		t.Fatalf("GetMessage returned %+v", got)
	}

	// UpdateMessageContent changes only the targeted row.
	if err := UpdateMessageContent(sqlDB, userID, "edited"); err != nil {
		t.Fatalf("UpdateMessageContent: %v", err)
	}
	got, _ = GetMessage(sqlDB, userID)
	if got.Content != "edited" {
		t.Fatalf("expected edited content, got %+v", got)
	}

	// DeleteMessagesFrom(narratorID) — Edit's "drop what followed" half
	// — removes the narrator reply but leaves the edited user message.
	if err := DeleteMessagesFrom(sqlDB, sessionID, narratorID); err != nil {
		t.Fatalf("DeleteMessagesFrom: %v", err)
	}
	messages, err := ListMessages(sqlDB, sessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 1 || messages[0].ID != userID {
		t.Fatalf("expected only the user message to survive, got %+v", messages)
	}

	// DeleteMessage removes exactly one row, no cascade.
	secondID, _ := CreateMessage(sqlDB, ChatMessage{SessionID: sessionID, Role: "narrator", Content: "more"})
	if err := DeleteMessage(sqlDB, secondID); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	messages, _ = ListMessages(sqlDB, sessionID)
	if len(messages) != 1 || messages[0].ID != userID {
		t.Fatalf("expected DeleteMessage to remove only its own row, got %+v", messages)
	}
}

func TestBranchSessionCopiesUpToMessage(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	profileID, _ := CreateBackendProfile(sqlDB, BackendProfile{Name: "p", Kind: "llm", BaseURL: "http://x"})
	originalID, _ := CreateSession(sqlDB, Session{Title: "original", BackendProfileID: profileID})

	_, _ = CreateMessage(sqlDB, ChatMessage{SessionID: originalID, Role: "user", Content: "msg1"})
	midID, _ := CreateMessage(sqlDB, ChatMessage{SessionID: originalID, Role: "narrator", Content: "msg2"})
	_, _ = CreateMessage(sqlDB, ChatMessage{SessionID: originalID, Role: "user", Content: "msg3 (should not be copied)"})

	branchID, err := BranchSession(sqlDB, originalID, midID)
	if err != nil {
		t.Fatalf("BranchSession: %v", err)
	}
	if branchID == originalID {
		t.Fatalf("BranchSession returned the original session id")
	}

	branch, err := GetSession(sqlDB, branchID)
	if err != nil {
		t.Fatalf("GetSession(branch): %v", err)
	}
	if branch.Title != "original (branch)" || branch.BackendProfileID != profileID {
		t.Fatalf("branch session carries wrong metadata: %+v", branch)
	}

	branchMessages, err := ListMessages(sqlDB, branchID)
	if err != nil {
		t.Fatalf("ListMessages(branch): %v", err)
	}
	if len(branchMessages) != 2 || branchMessages[0].Content != "msg1" || branchMessages[1].Content != "msg2" {
		t.Fatalf("expected exactly msg1+msg2 copied, got %+v", branchMessages)
	}

	// The original session must be untouched.
	originalMessages, err := ListMessages(sqlDB, originalID)
	if err != nil {
		t.Fatalf("ListMessages(original): %v", err)
	}
	if len(originalMessages) != 3 {
		t.Fatalf("branching mutated the original session: %+v", originalMessages)
	}
}
