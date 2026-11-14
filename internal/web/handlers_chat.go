package web

import (
	"database/sql"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
	"github.com/Lord-ZuzurNC/AIvalon/internal/llmclient"
)

type ChatHandlers struct {
	DB *sql.DB
}

func (h ChatHandlers) List(w http.ResponseWriter, r *http.Request) {
	sessions, err := db.ListSessions(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	profiles, err := db.ListBackendProfiles(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, ChatListPage(sessions, profiles))
}

func (h ChatHandlers) Create(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	profileID, err := strconv.ParseInt(r.FormValue("backend_profile_id"), 10, 64)
	if err != nil {
		http.Error(w, "a backend profile is required", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = "Untitled chat"
	}
	id, err := db.CreateSession(h.DB, db.Session{Title: title, BackendProfileID: profileID})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/chat/%d", id), http.StatusSeeOther)
}

func (h ChatHandlers) Show(w http.ResponseWriter, r *http.Request) {
	session, messages, err := h.loadSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	needsReply := len(messages) > 0 && messages[len(messages)-1].Role == "user"
	render(w, r, ChatPage(session, messages, needsReply))
}

func (h ChatHandlers) PostMessage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	r.ParseForm()
	content := strings.TrimSpace(r.FormValue("content"))
	if content == "" {
		http.Redirect(w, r, fmt.Sprintf("/chat/%d", id), http.StatusSeeOther)
		return
	}
	if _, err := db.CreateMessage(h.DB, db.ChatMessage{SessionID: id, Role: "user", Content: content}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/chat/%d", id), http.StatusSeeOther)
}

// Stream is the SSE endpoint: it answers the most recent unanswered user
// message, streaming the reply token by token and persisting the full
// reply once the backend is done.
//
// Cancellation: r.Context() is cancelled by net/http the moment the client
// closes the connection (htmx's sse extension uses EventSource — see
// docs/DESIGN.md, "Stopping a generation in progress"), and that same
// context is the one passed to llmclient.StreamChat, so closing the tab
// actually stops the backend from generating, not just from being heard.
func (h ChatHandlers) Stream(w http.ResponseWriter, r *http.Request) {
	session, messages, err := h.loadSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	profile, err := db.GetBackendProfile(h.DB, session.BackendProfileID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	chatMessages := make([]llmclient.Message, 0, len(messages))
	for _, m := range messages {
		role := "assistant"
		if m.Role == "user" {
			role = "user"
		}
		chatMessages = append(chatMessages, llmclient.Message{Role: role, Content: m.Content})
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)

	var full strings.Builder
	err = llmclient.StreamChat(r.Context(), profile, chatMessages, func(token string) {
		full.WriteString(token)
		// Each token becomes one HTML-escaped SSE "message" event; htmx
		// appends it (hx-swap="beforeend") into the DOM as it arrives.
		// Escaping here isn't optional: this is LLM output rendered as
		// HTML, the same trust boundary as any other user-controlled text.
		escaped := strings.ReplaceAll(html.EscapeString(token), "\n", "<br>")
		fmt.Fprintf(w, "data: %s\n\n", escaped)
		if flusher != nil {
			flusher.Flush()
		}
	})
	if err != nil {
		// The connection may already be half-closed (client gone); best
		// effort only, nothing left to recover into for this request.
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", html.EscapeString(err.Error()))
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	if full.Len() > 0 {
		if _, err := db.CreateMessage(h.DB, db.ChatMessage{SessionID: session.ID, Role: "character", Content: full.String()}); err != nil {
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", html.EscapeString(err.Error()))
		}
	}
}

func (h ChatHandlers) loadSession(r *http.Request) (db.Session, []db.ChatMessage, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return db.Session{}, nil, fmt.Errorf("invalid session id")
	}
	session, err := db.GetSession(h.DB, id)
	if err != nil {
		return db.Session{}, nil, err
	}
	messages, err := db.ListMessages(h.DB, id)
	if err != nil {
		return db.Session{}, nil, err
	}
	return session, messages, nil
}
