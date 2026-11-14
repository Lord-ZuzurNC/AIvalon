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
	personas, err := db.ListPersonas(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	characters, err := db.ListCharacterCards(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, ChatListPage(sessions, profiles, personas, characters))
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
	session := db.Session{Title: title, BackendProfileID: profileID}
	if personaID, err := strconv.ParseInt(r.FormValue("persona_id"), 10, 64); err == nil {
		session.PersonaID = personaID
	}

	id, err := db.CreateSession(h.DB, session)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Attach the selected characters and, if the first one has a
	// first_message, seed it as the session's opening line — the usual
	// roleplay convention of the character speaking first.
	var firstMessage string
	var firstCharacterID int64
	for i, idStr := range r.Form["character_ids"] {
		cardID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		if err := db.AttachCharacterToSession(h.DB, id, cardID, i); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if i == 0 {
			if card, err := db.GetCharacterCard(h.DB, cardID); err == nil {
				firstMessage = card.FirstMessage
				firstCharacterID = cardID
			}
		}
	}
	if firstMessage != "" {
		msg := db.ChatMessage{SessionID: id, Role: "character", CharacterCardID: firstCharacterID, Content: firstMessage}
		if _, err := db.CreateMessage(h.DB, msg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
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

	characters, err := db.ListCharacterCardsForSession(h.DB, session.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	persona := db.DefaultPersona
	if session.PersonaID != 0 {
		if p, err := db.GetPersona(h.DB, session.PersonaID); err == nil {
			persona = p
		}
	}

	chatMessages := buildChatMessages(persona, characters, messages)

	// ponytail: a group chat always replies as the first attached
	// character; turn-taking between multiple characters is a real design
	// question, not a bug, and it's deferred until it's actually needed.
	var replyAsCharacterID int64
	if len(characters) > 0 {
		replyAsCharacterID = characters[0].ID
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
		msg := db.ChatMessage{SessionID: session.ID, Role: "character", CharacterCardID: replyAsCharacterID, Content: full.String()}
		if _, err := db.CreateMessage(h.DB, msg); err != nil {
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", html.EscapeString(err.Error()))
		}
	}
}

// buildChatMessages turns the session's characters/persona into a system
// message (skipped entirely for a "naked" session with none attached) and
// maps the stored history onto the roles the backend API expects.
func buildChatMessages(persona db.Persona, characters []db.CharacterCard, history []db.ChatMessage) []llmclient.Message {
	var messages []llmclient.Message
	if system := buildSystemPrompt(persona, characters); system != "" {
		messages = append(messages, llmclient.Message{Role: "system", Content: system})
	}
	for _, m := range history {
		role := "assistant"
		if m.Role == "user" {
			role = "user"
		}
		messages = append(messages, llmclient.Message{Role: role, Content: m.Content})
	}
	return messages
}

func buildSystemPrompt(persona db.Persona, characters []db.CharacterCard) string {
	if len(characters) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are roleplaying as %s, talking to %s.\n", characters[0].Name, persona.Name)
	for _, c := range characters {
		fmt.Fprintf(&b, "\n%s: %s", c.Name, c.Description)
		if c.Personality != "" {
			fmt.Fprintf(&b, "\nPersonality: %s", c.Personality)
		}
		if c.Scenario != "" {
			fmt.Fprintf(&b, "\nScenario: %s", c.Scenario)
		}
	}
	if persona.Description != "" {
		fmt.Fprintf(&b, "\n\n%s is: %s", persona.Name, persona.Description)
	}
	return b.String()
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
