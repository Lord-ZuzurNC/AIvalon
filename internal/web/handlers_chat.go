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
// speaking (persona name for "user", "World" for the narrator) and the
// message already run through formatMessageContent into safe, styled
// HTML.
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
	// roleplay convention of the character speaking first. This is a
	// specific line the user wrote on the card, so it keeps its own
	// character_card_id even though every reply from here on is narrated
	// by the world instead.
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

// Stream is the SSE endpoint. Every session is answered by the world, not
// by one fixed character's voice — Lord's call, after hitting the natural
// limit of the previous "one character replies" designs: no fixed
// per-character slot can express "ask Merlin, have him relay to
// Merlinette, and report back what she said" in one exchange, and that
// turned out to be a common, not rare, request. The world sees every
// attached character card and is free to narrate whichever of them (or,
// with none attached, characters it invents on the spot, GM-style) the
// moment calls for — see buildNarratorPrompt. The world is explicitly
// forbidden from speaking or acting for the player.
//
// This replaced two earlier designs in the same build: forced
// round-robin (every character replies every round — caused identity
// crossover) and addressee classification (one LLM call decides who's
// being spoken to, then only they reply — fixed crossover but couldn't
// do relays). Both, and their tests, were deleted rather than left lying
// around unused.
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

	reply, ok := h.streamWorldReply(r, w, session, profile, persona, characters, messages, emit, emitError)
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

// streamWorldReply is the one and only reply path: a single completion,
// voiced as the world rather than any character, free to narrate whoever
// the moment calls for.
func (h ChatHandlers) streamWorldReply(r *http.Request, w http.ResponseWriter, session db.Session, profile db.BackendProfile, persona db.Persona, characters []db.CharacterCard, history []db.ChatMessage, emit func(string), emitError func(error)) (db.ChatMessage, bool) {
	emit("World: ")

	settings, err := db.GetAppSettings(h.DB)
	if err != nil {
		emitError(err)
		return db.ChatMessage{}, false
	}
	chatMessages := buildNarratorMessages(settings.SystemPromptTemplate, persona, characters, history)
	var full strings.Builder
	err = llmclient.StreamChat(r.Context(), profile, chatMessages, func(token string) {
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

// buildNarratorMessages maps the session into the backend API shape: a
// system prompt built from the user-editable template (Settings → System
// prompt; see buildNarratorPrompt) plus history with past "character"
// lines re-prefixed by their speaker's name (characters may be nil for a
// session with none attached — no prefix is added then, there's nothing
// to resolve against) and past "narrator" lines passed through as-is,
// since each already reads as one coherent passage with names naturally
// in the prose.
func buildNarratorMessages(template string, persona db.Persona, characters []db.CharacterCard, history []db.ChatMessage) []llmclient.Message {
	messages := []llmclient.Message{{Role: "system", Content: buildNarratorPrompt(template, persona, characters)}}
	for _, m := range history {
		messages = append(messages, llmclient.Message{Role: historyRole(m), Content: namePrefixed(m, characters)})
	}
	return messages
}

// buildNarratorPrompt fills in the user-editable system prompt template
// (Settings → System prompt, stored as AppSettings.SystemPromptTemplate)
// with the live session state. Plain named placeholders, not Go's
// text/template — Lord edits this by hand and isn't a programmer (see
// USER.md), so there's no loop/conditional syntax to get wrong; an
// unrecognized or deleted placeholder just stays literal or disappears,
// it can never fail to render.
//
// {{lorebook}} always resolves to "" today: there's no Lorebook CRUD yet
// (see docs/DESIGN.md's "World" section — still an empty shell), so
// there's nothing to inject. The placeholder is wired up now so that
// landing Lorebook later is a pure content change here, not a prompt
// rewrite.
func buildNarratorPrompt(template string, persona db.Persona, characters []db.CharacterCard) string {
	prompt := template
	prompt = strings.ReplaceAll(prompt, "{{persona_name}}", persona.Name)
	prompt = strings.ReplaceAll(prompt, "{{persona_description}}", persona.Description)
	prompt = strings.ReplaceAll(prompt, "{{characters}}", renderCharactersBlock(characters))
	prompt = strings.ReplaceAll(prompt, "{{lorebook}}", "")
	return prompt
}

// renderCharactersBlock is the only part of the prompt still built in Go
// rather than the editable template — the per-character loop needs real
// code, not a placeholder, and the empty case (act as GM, invent
// characters) only makes sense as a substitute for an actual character
// list, not something to leave as a template edit.
func renderCharactersBlock(characters []db.CharacterCard) string {
	if len(characters) == 0 {
		return "No characters are predefined for this session. Act as the game master: " +
			"introduce and voice whichever characters the scene naturally calls for, inventing " +
			"names and personalities as you go, and keep each one consistent once introduced " +
			"rather than reinventing them from one reply to the next."
	}
	var b strings.Builder
	b.WriteString("These are the fixed characters who live in this world, as described by their character cards:")
	for _, c := range characters {
		fmt.Fprintf(&b, "\n%s: %s", c.Name, c.Description)
		if c.Personality != "" {
			fmt.Fprintf(&b, "\nPersonality: %s", c.Personality)
		}
		if c.Scenario != "" {
			fmt.Fprintf(&b, "\nScenario: %s", c.Scenario)
		}
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
// other roles pass through unchanged.
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
// an old "character"-role row kept for history, "World" for "narrator")
// and the message content formatted into safe styled HTML.
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
