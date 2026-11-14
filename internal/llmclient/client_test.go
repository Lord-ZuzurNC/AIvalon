package llmclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

func TestStreamChatParsesDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, tok := range []string{"Hel", "lo", " world"} {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", tok)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	profile := db.BackendProfile{BaseURL: server.URL, Model: "test-model"}
	var got strings.Builder
	err := StreamChat(context.Background(), profile, []Message{{Role: "user", Content: "hi"}}, func(s string) {
		got.WriteString(s)
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if got.String() != "Hello world" {
		t.Fatalf("got %q, want %q", got.String(), "Hello world")
	}
}

func TestStreamChatCancellationAbortsRequest(t *testing.T) {
	serverSawDisconnect := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done(): // the server's own ctx is cancelled when the client disconnects
			close(serverSawDisconnect)
		case <-time.After(5 * time.Second):
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- StreamChat(ctx, db.BackendProfile{BaseURL: server.URL}, nil, func(string) {
			cancel() // simulate the browser closing the SSE connection after the first token
		})
	}()

	select {
	case <-serverSawDisconnect:
	case <-time.After(5 * time.Second):
		t.Fatal("server never saw the client disconnect after ctx cancellation")
	}
	<-done // StreamChat must return (ctx.Err-wrapped), not hang
}
