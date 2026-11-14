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

// DisplayMessage is the view-model for one rendered chat line: who's
// speaking (persona name for "user", the resolved character name for
// "character") and the message already run through formatMessageContent
// into safe, styled HTML.
type DisplayMessage struct {
	Speaker     string
	ContentHTML string
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
	display, err := h.displayMessages(session, messages)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	needsReply := len(messages) > 0 && messages[len(messages)-1].Role == "user"
	render(w, r, ChatPage(session, display, needsReply))
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

// Stream is the SSE endpoint. One call answers one user message with one
// full *round*: every character attached to the session replies in
// turn_order (a naked session with none attached gets a single generic
// reply instead), each one seeing the others' replies from earlier in the
// same round so they can actually address each other, not just the user.
//
// Live tokens are streamed as plain escaped text (a "Name: " marker before
// each character's turn) for immediate feedback; once the whole round is
// persisted, a final SSE event carries an out-of-band swap that replaces
// the live div with the same styled rendering (name + quote/action
// formatting) a page reload would show — so the display ends up
// consistent with history instead of staying as flat streamed text.
//
// Cancellation: r.Context() is cancelled by net/http the moment the client
// closes the connection (htmx's sse extension uses EventSource — see
// docs/DESIGN.md, "Stopping a generation in progress"), and that same
// context is passed to llmclient.StreamChat, so closing the tab actually
// stops the backend from generating, not just from being heard. Closing
// mid-round leaves the round partially persisted, which is correct: what
// a character already said stays said.
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
	persona := h.resolvePersona(session)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)

	emit := func(text string) {
		escaped := strings.ReplaceAll(html.EscapeString(text), "\n", "<br>")
		fmt.Fprintf(w, "data: %s\n\n", escaped)
		if flusher != nil {
			flusher.Flush()
		}
	}
	emitError := func(err error) {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", html.EscapeString(err.Error()))
		if flusher != nil {
			flusher.Flush()
		}
	}

	turns := characters
	if len(turns) == 0 {
		turns = []db.CharacterCard{{}} // one generic, character-less reply
	}

	var newMessages []db.ChatMessage
	for i, speaking := range turns {
		speakerName := speaking.Name
		if speakerName == "" {
			speakerName = "AI"
		}
		if i == 0 {
			emit(speakerName + ": ")
		} else {
			emit("\n\n" + speakerName + ": ")
		}

		chatMessages := buildChatMessages(persona, characters, speaking, messages)
		var full strings.Builder
		err := llmclient.StreamChat(r.Context(), profile, chatMessages, func(token string) {
			full.WriteString(token)
			emit(token)
		})
		if err != nil {
			// The connection may already be half-closed (client gone); best
			// effort only. Whatever already streamed/persisted this round
			// stays — nothing to roll back into.
			emitError(err)
			return
		}
		if full.Len() == 0 {
			continue
		}

		content := strings.TrimSpace(stripSpeakerEcho(full.String(), characters))
		msg := db.ChatMessage{SessionID: session.ID, Role: "character", CharacterCardID: speaking.ID, Content: content}
		msgID, err := db.CreateMessage(h.DB, msg)
		if err != nil {
			emitError(err)
			return
		}
		msg.ID = msgID
		messages = append(messages, msg) // next character in the round sees this reply
		newMessages = append(newMessages, msg)
	}

	display, err := h.displayMessages(session, newMessages)
	if err != nil {
		emitError(err)
		return
	}
	var final strings.Builder
	final.WriteString(`<div id="live" hx-swap-oob="true">`)
	for _, d := range display {
		final.WriteString("<p><strong>")
		final.WriteString(html.EscapeString(d.Speaker))
		final.WriteString(":</strong> ")
		final.WriteString(d.ContentHTML)
		final.WriteString("</p>")
	}
	final.WriteString(`</div>`)
	fmt.Fprintf(w, "data: %s\n\n", final.String())
	if flusher != nil {
		flusher.Flush()
	}
}

// buildChatMessages turns the session's characters/persona into a system
// message (skipped entirely for a "naked" session with none attached) and
// maps the stored history onto the roles the backend API expects.
// speakingAs disambiguates the system prompt when characters has more than
// one entry — every character's sheet is still included for context, but
// the prompt is explicit about which one this particular completion voices.
func buildChatMessages(persona db.Persona, characters []db.CharacterCard, speakingAs db.CharacterCard, history []db.ChatMessage) []llmclient.Message {
	var messages []llmclient.Message
	if system := buildSystemPrompt(persona, characters, speakingAs); system != "" {
		messages = append(messages, llmclient.Message{Role: "system", Content: system})
	}
	for _, m := range history {
		role := "assistant"
		if m.Role == "user" {
			role = "user"
		}
		content := m.Content
		// In a group chat every reply is "assistant" to the backend
		// regardless of which character said it, so prefix the speaker's
		// name into the content itself — otherwise the model has no way
		// to tell Merlin's lines apart from another character's.
		if m.Role == "character" {
			if name := characterName(characters, m.CharacterCardID); name != "" {
				content = name + ": " + content
			}
		}
		messages = append(messages, llmclient.Message{Role: role, Content: content})
	}
	return messages
}

func buildSystemPrompt(persona db.Persona, characters []db.CharacterCard, speakingAs db.CharacterCard) string {
	if len(characters) == 0 {
		return ""
	}
	var b strings.Builder
	if len(characters) == 1 {
		fmt.Fprintf(&b, "You are roleplaying as %s, talking to %s.\n", speakingAs.Name, persona.Name)
	} else {
		var others []string
		for _, c := range characters {
			if c.ID != speakingAs.ID {
				others = append(others, c.Name)
			}
		}
		fmt.Fprintf(&b, "This is a group roleplay with %s and the following characters. You are now voicing %s, and %s ONLY — you are not %s.\n", persona.Name, speakingAs.Name, speakingAs.Name, strings.Join(others, " and "))
		fmt.Fprintf(&b, "Even if %s is addressed by name instead of %s, stay in character as %s — %s will get an answer in their own turn, not from you.\n", strings.Join(others, " or "), speakingAs.Name, speakingAs.Name, strings.Join(others, " and "))
	}
	fmt.Fprintf(&b, "Write only %s's reply itself. Do not prefix it with \"%s:\" or any other speaker label — the interface already shows who's speaking.\n", speakingAs.Name, speakingAs.Name)
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

func characterName(characters []db.CharacterCard, id int64) string {
	for _, c := range characters {
		if c.ID == id {
			return c.Name
		}
	}
	return ""
}

// stripSpeakerEcho defends against a known LLM habit: having seen "Name:"
// prefixes in the history we feed it (see buildChatMessages), a model will
// sometimes echo a "Name:" label back at the start of its reply even when
// told not to — and in a group chat with similarly-named characters, it
// can pick the WRONG name (e.g. voicing "Merlinette" during "Merlin"'s
// slot because the user addressed Merlinette directly). Either way the
// interface already shows the actual speaker from the stored
// character_card_id, so any leading "Name:" matching ANY attached
// character (after stray leading whitespace/asterisks, e.g. a "***"
// scene-break the model also likes to add) is stripped before persisting.
//
// This only removes the stray label — it does not and cannot fix the
// model actually having written the wrong character's voice under this
// slot. That's a model/prompt-quality limitation (worse with near-
// identical names), not something a string trim can repair; see
// buildSystemPrompt's per-turn identity instructions for the mitigation
// that targets the root cause instead of the symptom.
func stripSpeakerEcho(content string, characters []db.CharacterCard) string {
	trimmed := strings.TrimLeft(content, " 	\r\n*")
	for _, c := range characters {
		if c.Name == "" {
			continue
		}
		prefix := c.Name + ":"
		if len(trimmed) >= len(prefix) && strings.EqualFold(trimmed[:len(prefix)], prefix) {
			return strings.TrimLeft(trimmed[len(prefix):], " 	")
		}
	}
	return content
}

// displayMessages resolves each stored message into its view-model: the
// speaker's actual name (persona for "user", the matching character for
// "character") and the message content formatted into safe styled HTML.
func (h ChatHandlers) displayMessages(session db.Session, messages []db.ChatMessage) ([]DisplayMessage, error) {
	characters, err := db.ListCharacterCardsForSession(h.DB, session.ID)
	if err != nil {
		return nil, err
	}
	persona := h.resolvePersona(session)

	out := make([]DisplayMessage, 0, len(messages))
	for _, m := range messages {
		speaker := "AI"
		switch m.Role {
		case "user":
			speaker = persona.Name
		case "character":
			if name := characterName(characters, m.CharacterCardID); name != "" {
				speaker = name
			}
		case "system":
			speaker = "System"
		}
		out = append(out, DisplayMessage{Speaker: speaker, ContentHTML: formatMessageContent(m.Content)})
	}
	return out, nil
}

func (h ChatHandlers) resolvePersona(session db.Session) db.Persona {
	if session.PersonaID != 0 {
		if p, err := db.GetPersona(h.DB, session.PersonaID); err == nil {
			return p
		}
	}
	return db.DefaultPersona
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
