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
// "character", "World" for a narrator line) and the message already run
// through formatMessageContent into safe, styled HTML.
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

// Stream is the SSE endpoint. A session with 0 or 1 characters attached
// gets the simple treatment: that character (or nobody, for a naked
// session) answers directly, in their own voice. A session with 2+
// characters switches to "world" narration instead of picking one
// character to answer (see buildNarratorMessages) — because no fixed
// per-character turn allocation can express "ask Merlin, have him relay
// to Merlinette, and report back what she said" in one exchange; that
// needs something that can see and narrate the whole scene, not a single
// character's voice. This replaced an earlier addressee-classification
// scheme (deleted, not left lying around unused) that picked one
// character per round by LLM classification — it fixed identity
// crossover but structurally couldn't do multi-character relays, which is
// exactly what surfaced next.
//
// Live tokens are streamed as plain escaped text for immediate feedback;
// once the reply is complete and persisted, a final SSE event carries an
// out-of-band swap that replaces the live div with the same styled
// rendering (speaker name + quote/action formatting) a page reload would
// show — so the display ends up consistent with history instead of
// staying as flat streamed text.
//
// Cancellation: r.Context() is cancelled by net/http the moment the client
// closes the connection (htmx's sse extension uses EventSource — see
// docs/DESIGN.md, "Stopping a generation in progress"), and that same
// context is passed to llmclient.StreamChat, so closing the tab actually
// stops the backend from generating, not just from being heard.
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

	var reply db.ChatMessage
	var ok bool
	if len(characters) >= 2 {
		reply, ok = h.streamNarratorReply(r, w, session, profile, persona, characters, messages, emit, emitError)
	} else {
		speaking := db.CharacterCard{} // naked session: a generic, character-less reply
		if len(characters) == 1 {
			speaking = characters[0]
		}
		reply, ok = h.streamCharacterReply(r, w, session, profile, persona, characters, speaking, messages, emit, emitError)
	}
	if !ok {
		return
	}

	display, err := h.displayMessages(session, []db.ChatMessage{reply})
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

// streamCharacterReply handles the 0-or-1-character case: speaking answers
// directly in their own voice (or a generic reply, for a naked session).
func (h ChatHandlers) streamCharacterReply(r *http.Request, w http.ResponseWriter, session db.Session, profile db.BackendProfile, persona db.Persona, characters []db.CharacterCard, speaking db.CharacterCard, history []db.ChatMessage, emit func(string), emitError func(error)) (db.ChatMessage, bool) {
	speakerName := speaking.Name
	if speakerName == "" {
		speakerName = "AI"
	}
	emit(speakerName + ": ")

	chatMessages := buildChatMessages(persona, speaking, history)
	var full strings.Builder
	err := llmclient.StreamChat(r.Context(), profile, chatMessages, func(token string) {
		full.WriteString(token)
		emit(token)
	})
	if err != nil {
		emitError(err)
		return db.ChatMessage{}, false
	}
	if full.Len() == 0 {
		return db.ChatMessage{}, false
	}

	content := strings.TrimSpace(stripLeadingLabel(full.String(), []string{speakerName}))
	msg := db.ChatMessage{SessionID: session.ID, Role: "character", CharacterCardID: speaking.ID, Content: content}
	msgID, err := db.CreateMessage(h.DB, msg)
	if err != nil {
		emitError(err)
		return db.ChatMessage{}, false
	}
	msg.ID = msgID
	return msg, true
}

// streamNarratorReply handles the 2+-character case: one completion,
// voiced as the world rather than any single character, free to narrate
// whichever of the attached characters the moment calls for.
func (h ChatHandlers) streamNarratorReply(r *http.Request, w http.ResponseWriter, session db.Session, profile db.BackendProfile, persona db.Persona, characters []db.CharacterCard, history []db.ChatMessage, emit func(string), emitError func(error)) (db.ChatMessage, bool) {
	emit("World: ")

	chatMessages := buildNarratorMessages(persona, characters, history)
	var full strings.Builder
	err := llmclient.StreamChat(r.Context(), profile, chatMessages, func(token string) {
		full.WriteString(token)
		emit(token)
	})
	if err != nil {
		emitError(err)
		return db.ChatMessage{}, false
	}
	if full.Len() == 0 {
		return db.ChatMessage{}, false
	}

	content := strings.TrimSpace(stripLeadingLabel(full.String(), []string{"World", "Narrator"}))
	msg := db.ChatMessage{SessionID: session.ID, Role: "narrator", Content: content}
	msgID, err := db.CreateMessage(h.DB, msg)
	if err != nil {
		emitError(err)
		return db.ChatMessage{}, false
	}
	msg.ID = msgID
	return msg, true
}

// buildChatMessages is the 0-or-1-character case: a plain system prompt
// ("you are X, talking to persona") plus history mapped onto the roles
// the backend API expects. Past "narrator" messages (from when this
// session, or an older build of AIvalon, used group narration) pass
// through as-is; past "character" messages get their speaker's name
// prefixed back in, same reasoning as buildNarratorMessages below.
func buildChatMessages(persona db.Persona, speaking db.CharacterCard, history []db.ChatMessage) []llmclient.Message {
	var messages []llmclient.Message
	if system := buildSoloPrompt(persona, speaking); system != "" {
		messages = append(messages, llmclient.Message{Role: "system", Content: system})
	}
	for _, m := range history {
		messages = append(messages, llmclient.Message{Role: historyRole(m), Content: namePrefixed(m, nil)})
	}
	return messages
}

func buildSoloPrompt(persona db.Persona, speaking db.CharacterCard) string {
	if speaking.Name == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are roleplaying as %s, talking to %s.\n", speaking.Name, persona.Name)
	fmt.Fprintf(&b, "Write only %s's reply itself. Do not prefix it with \"%s:\" or any other speaker label — the interface already shows who's speaking.\n", speaking.Name, speaking.Name)
	fmt.Fprintf(&b, "\n%s: %s", speaking.Name, speaking.Description)
	if speaking.Personality != "" {
		fmt.Fprintf(&b, "\nPersonality: %s", speaking.Personality)
	}
	if speaking.Scenario != "" {
		fmt.Fprintf(&b, "\nScenario: %s", speaking.Scenario)
	}
	if persona.Description != "" {
		fmt.Fprintf(&b, "\n\n%s is: %s", persona.Name, persona.Description)
	}
	return b.String()
}

// buildNarratorMessages is the 2+-character case: the system prompt casts
// the model as the world itself, with every attached character's sheet
// for context, explicitly free to narrate any of them — the thing a
// single fixed "you are only X" turn could never do.
func buildNarratorMessages(persona db.Persona, characters []db.CharacterCard, history []db.ChatMessage) []llmclient.Message {
	messages := []llmclient.Message{{Role: "system", Content: buildNarratorPrompt(persona, characters)}}
	for _, m := range history {
		messages = append(messages, llmclient.Message{Role: historyRole(m), Content: namePrefixed(m, characters)})
	}
	return messages
}

func buildNarratorPrompt(persona db.Persona, characters []db.CharacterCard) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the narrator of this world — not any single character in it. "+
		"%s is the player, speaking as themselves; never write %s's actions, words, or decisions, only react to what they actually said.\n",
		persona.Name, persona.Name)
	b.WriteString("These characters live in this world:\n")
	for _, c := range characters {
		fmt.Fprintf(&b, "\n%s: %s", c.Name, c.Description)
		if c.Personality != "" {
			fmt.Fprintf(&b, "\nPersonality: %s", c.Personality)
		}
		if c.Scenario != "" {
			fmt.Fprintf(&b, "\nScenario: %s", c.Scenario)
		}
	}
	b.WriteString("\n\nNarrate what happens next: describe the scene and write whichever of these " +
		"characters' actions or dialogue the moment calls for. You are not limited to one of " +
		"them — if the player asks one character to relay something to another and report " +
		"back, narrate the whole exchange, including the other character's actual reply, not " +
		"just the first character's side of it.")
	if persona.Description != "" {
		fmt.Fprintf(&b, "\n\n%s is: %s", persona.Name, persona.Description)
	}
	return b.String()
}

func historyRole(m db.ChatMessage) string {
	if m.Role == "user" {
		return "user"
	}
	return "assistant"
}

// namePrefixed prefixes a past "character" message with its speaker's
// name (characters may be nil when there's no group context to resolve
// against) so the model can tell whose line was whose; "narrator" and
// other roles pass through unchanged — a narrator message already reads
// as one coherent passage with names naturally in the prose.
func namePrefixed(m db.ChatMessage, characters []db.CharacterCard) string {
	if m.Role != "character" {
		return m.Content
	}
	if name := characterName(characters, m.CharacterCardID); name != "" {
		return name + ": " + m.Content
	}
	return m.Content
}

func characterName(characters []db.CharacterCard, id int64) string {
	for _, c := range characters {
		if c.ID == id {
			return c.Name
		}
	}
	return ""
}

// stripLeadingLabel defends against a known LLM habit: having seen
// "Name:" prefixes in the history we feed it, a model will sometimes echo
// one back at the start of its own reply even when told not to — the
// interface already shows the real speaker, so a leading "Label:"
// matching any of labels (after stray leading whitespace/asterisks, e.g.
// a "***" scene-break the model also likes to add) is stripped before
// persisting.
func stripLeadingLabel(content string, labels []string) string {
	trimmed := strings.TrimLeft(content, " \t\r\n*")
	for _, label := range labels {
		if label == "" {
			continue
		}
		prefix := label + ":"
		if len(trimmed) >= len(prefix) && strings.EqualFold(trimmed[:len(prefix)], prefix) {
			return strings.TrimLeft(trimmed[len(prefix):], " \t")
		}
	}
	return content
}

// displayMessages resolves each stored message into its view-model: the
// speaker's actual name (persona for "user", the matching character for
// "character", "World" for "narrator") and the message content formatted
// into safe styled HTML.
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
		case "narrator":
			speaker = "World"
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
