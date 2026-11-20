package web

import (
	"database/sql"
	"fmt"
	"html"
	"log"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
	"github.com/Lord-ZuzurNC/AIvalon/internal/llmclient"
)

type ChatHandlers struct {
	DB   *sql.DB
	Gate *VaultGate
}

// DisplayMessage is the view-model for one rendered chat line: who's
// speaking (persona name for "user", "World" for the narrator), the raw
// content (needed verbatim for the Edit textarea and the Copy button —
// ContentHTML is already escaped/styled, not a round-trippable source),
// and the message already run through formatMessageContent into safe,
// styled HTML.
type DisplayMessage struct {
	ID          int64
	Role        string
	Speaker     string
	Content     string
	ContentHTML string
}

func (h ChatHandlers) List(w http.ResponseWriter, r *http.Request) {
	total, err := db.CountSessions(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pager := newPager(r, "/chat", total, listPageSize)
	sessions, err := db.ListSessions(h.DB, pager.Size, pager.Offset())
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
	characters, err := db.ListCharacterCards(h.DB, -1, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	lorebooks, err := db.ListLorebooks(h.DB, -1, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, ChatListPage(sessions, pager, profiles, personas, characters, lorebooks))
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
	if lorebookID, err := strconv.ParseInt(r.FormValue("lorebook_id"), 10, 64); err == nil {
		session.LorebookID = lorebookID
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
	// A vaulted session's direct URL must refuse an unlocked-vault-less
	// browser the same way the vault's own list would — otherwise
	// hiding it from /chat's list is cosmetic, not real: anyone who
	// knows or bookmarks /chat/{id} could still open it straight away.
	if session.IsVaulted && !h.Gate.Unlocked(r) {
		salt := ""
		if stored, err := db.GetVaultPIN(h.DB); err == nil {
			salt = stored.PINSalt
		}
		render(w, r, VaultUnlockPage("", salt))
		return
	}
	display, err := h.displayMessages(session, messages)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	needsReply := len(messages) > 0 && messages[len(messages)-1].Role == "user"
	coverImage, err := h.resolveSessionCoverImage(session)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	bgOptions, err := h.backgroundOptions(session)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	info, err := h.sessionInfo(session)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, ChatPage(session, display, needsReply, coverImage, bgOptions, info))
}

// SessionInfo is the right-hand panel's "what's attached to this
// session" summary — one query per related table, each optional
// (PersonaID/LorebookID are 0 until set; Characters can be empty for a
// freeform chat). ActiveLorebookEntries is filtered to Enabled==true
// only: "active" here means "this entry's own on/off switch in World",
// not "triggered by the last message" — that's a per-turn, per-message
// computation (see formatMessageContent's lorebook injection) that
// doesn't have a single stable answer to show in a static panel.
type SessionInfo struct {
	BackendProfileName    string
	Persona               db.Persona
	LorebookName          string
	ActiveLorebookEntries []db.LorebookEntry
	Characters            []db.CharacterCard
}

func (h ChatHandlers) sessionInfo(session db.Session) (SessionInfo, error) {
	info := SessionInfo{Persona: db.DefaultPersona}
	if session.BackendProfileID != 0 {
		profile, err := db.GetBackendProfile(h.DB, session.BackendProfileID)
		if err != nil {
			return info, err
		}
		info.BackendProfileName = profile.Name
	}
	if session.PersonaID != 0 {
		persona, err := db.GetPersona(h.DB, session.PersonaID)
		if err != nil {
			return info, err
		}
		info.Persona = persona
	}
	if session.LorebookID != 0 {
		book, err := db.GetLorebook(h.DB, session.LorebookID)
		if err != nil {
			return info, err
		}
		info.LorebookName = book.Name
		entries, err := db.ListLorebookEntries(h.DB, session.LorebookID)
		if err != nil {
			return info, err
		}
		for _, e := range entries {
			if e.Enabled {
				info.ActiveLorebookEntries = append(info.ActiveLorebookEntries, e)
			}
		}
	}
	characters, err := db.ListCharacterCardsForSession(h.DB, session.ID)
	if err != nil {
		return info, err
	}
	info.Characters = characters
	return info, nil
}

// BackgroundOption is one entry in the "use this as wallpaper" picker —
// always sourced from an image the session already has access to
// (its Lorebook's cover, or an attached Character's portrait), never
// an arbitrary upload of its own; see SetBackground for why that
// matters.
type BackgroundOption struct {
	Label string
	Path  string
}

func (h ChatHandlers) backgroundOptions(session db.Session) ([]BackgroundOption, error) {
	var opts []BackgroundOption
	if session.LorebookID != 0 {
		book, err := db.GetLorebook(h.DB, session.LorebookID)
		if err != nil {
			return nil, err
		}
		if book.ImagePath != "" {
			opts = append(opts, BackgroundOption{Label: "World: " + book.Name, Path: book.ImagePath})
		}
	}
	characters, err := db.ListCharacterCardsForSession(h.DB, session.ID)
	if err != nil {
		return nil, err
	}
	for _, c := range characters {
		if c.ImagePath != "" {
			opts = append(opts, BackgroundOption{Label: "Character: " + c.Name, Path: c.ImagePath})
		}
	}
	return opts, nil
}

// SetBackground only ever accepts a path that's currently one of this
// session's own backgroundOptions (or "" to clear) — never whatever a
// POST body happens to contain. A free-text path here would land raw
// inside a CSS url(...) in ChatPage's style attribute; restricting it
// to known, already-uploaded, MIME-sniffed image paths closes that off
// at the source instead of trying to sanitize arbitrary CSS later.
func (h ChatHandlers) SetBackground(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	session, err := db.GetSession(h.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if session.IsVaulted && !h.Gate.Unlocked(r) {
		http.Error(w, "locked", http.StatusForbidden)
		return
	}
	r.ParseForm()
	path := r.FormValue("background_image_path")
	if path != "" {
		opts, err := h.backgroundOptions(session)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		valid := false
		for _, o := range opts {
			if o.Path == path {
				valid = true
				break
			}
		}
		if !valid {
			http.Error(w, "not one of this session's available images", http.StatusBadRequest)
			return
		}
	}
	if err := db.UpdateSessionBackground(h.DB, id, path); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/chat/%d", id), http.StatusSeeOther)
}

// resolveSessionCoverImage picks what to show next to the chat title: the
// session's Lorebook's own uploaded image when it has one, otherwise one
// of a small set of local placeholder SVGs — randomized per Lord's
// explicit "pick something random for now, we'll build a real default
// later" — picked fresh on every page load rather than stored, since it's
// a stand-in, not a setting.
func (h ChatHandlers) resolveSessionCoverImage(session db.Session) (string, error) {
	if session.LorebookID != 0 {
		book, err := db.GetLorebook(h.DB, session.LorebookID)
		if err != nil {
			return "", err
		}
		if book.ImagePath != "" {
			return "/static/" + book.ImagePath, nil
		}
	}
	return randomPlaceholderCoverImage(), nil
}

// placeholderCoverImages are plain authored SVGs (solid color + a simple
// drawn book shape), not a reused icon-font glyph or emoji — see
// web/static/placeholders/. Four is enough to make "random" visible
// without a real default-image system, which is the whole point: a
// stand-in, not a feature.
var placeholderCoverImages = []string{
	"/static/placeholders/lorebook-1.svg",
	"/static/placeholders/lorebook-2.svg",
	"/static/placeholders/lorebook-3.svg",
	"/static/placeholders/lorebook-4.svg",
}

func randomPlaceholderCoverImage() string {
	return placeholderCoverImages[rand.IntN(len(placeholderCoverImages))]
}

// vaultBlocks loads session id and reports whether it's vaulted and
// this browser hasn't unlocked the vault — every /chat/{id}/* route
// that can read or mutate a session (not just Show/Stream) needs this
// check, or hiding a session from the list is purely cosmetic: its
// direct URL, its Duplicate/Branch copies, and its message edits would
// all still go through untouched.
func (h ChatHandlers) vaultBlocks(r *http.Request, id int64) (bool, error) {
	session, err := db.GetSession(h.DB, id)
	if err != nil {
		return false, err
	}
	return session.IsVaulted && !h.Gate.Unlocked(r), nil
}

func (h ChatHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if blocked, err := h.vaultBlocks(r, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if blocked {
		http.Error(w, "locked", http.StatusForbidden)
		return
	}
	if err := db.DeleteSession(h.DB, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/chat", http.StatusSeeOther)
}

// Duplicate clones a whole session (see db.DuplicateSession) and
// redirects into the copy — unlike Branch, there's no cutoff message to
// pick: this is a full clone, reachable straight from the session list.
func (h ChatHandlers) Duplicate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if blocked, err := h.vaultBlocks(r, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if blocked {
		http.Error(w, "locked", http.StatusForbidden)
		return
	}
	newID, err := db.DuplicateSession(h.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/chat/%d", newID), http.StatusSeeOther)
}

func (h ChatHandlers) PostMessage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if blocked, err := h.vaultBlocks(r, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if blocked {
		http.Error(w, "locked", http.StatusForbidden)
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

// messageIDs pulls {id} and {messageID} out of the route, checks the
// message actually belongs to that session (without that check, a
// crafted request could edit/delete/regenerate/branch a message that
// lives in someone else's chat, since the two path params aren't
// otherwise linked), and refuses the whole thing if the session is
// vaulted and this browser hasn't unlocked it — Regenerate/Branch/Edit/
// DeleteMessage all share this one gate.
func (h ChatHandlers) messageIDs(r *http.Request) (sessionID, messageID int64, err error) {
	sessionID, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid session id")
	}
	messageID, err = strconv.ParseInt(r.PathValue("messageID"), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid message id")
	}
	if blocked, err := h.vaultBlocks(r, sessionID); err != nil {
		return 0, 0, err
	} else if blocked {
		return 0, 0, fmt.Errorf("session %d is locked", sessionID)
	}
	msg, err := db.GetMessage(h.DB, messageID)
	if err != nil {
		return 0, 0, err
	}
	if msg.SessionID != sessionID {
		return 0, 0, fmt.Errorf("message %d does not belong to session %d", messageID, sessionID)
	}
	return sessionID, messageID, nil
}

// RegenerateMessage drops the AI reply being regenerated (and anything
// after it, though there normally is nothing) and redirects back to the
// session. It writes no new reply itself: the redirect lands back on
// Show, which — since the message right before the deleted one is a
// user turn, by construction of this app's turn order — sets
// needsReply=true and the page's existing SSE auto-connect does the
// actual generation. Same mechanism Edit relies on below.
func (h ChatHandlers) RegenerateMessage(w http.ResponseWriter, r *http.Request) {
	sessionID, messageID, err := h.messageIDs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := db.DeleteMessagesFrom(h.DB, sessionID, messageID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/chat/%d", sessionID), http.StatusSeeOther)
}

// BranchMessage forks the conversation up to and including messageID
// into a new session and redirects straight into it — see
// db.BranchSession for what "fork" copies.
func (h ChatHandlers) BranchMessage(w http.ResponseWriter, r *http.Request) {
	sessionID, messageID, err := h.messageIDs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	newSessionID, err := db.BranchSession(h.DB, sessionID, messageID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/chat/%d", newSessionID), http.StatusSeeOther)
}

// EditMessage rewrites a user message's content and drops everything
// that followed it (the AI reply it provoked is no longer answering the
// right thing once the content changes) — then the same
// needsReply-on-redirect mechanism as RegenerateMessage produces a fresh
// reply to the edited line.
func (h ChatHandlers) EditMessage(w http.ResponseWriter, r *http.Request) {
	sessionID, messageID, err := h.messageIDs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.ParseForm()
	content := strings.TrimSpace(r.FormValue("content"))
	if content == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}
	if err := db.UpdateMessageContent(h.DB, messageID, content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := db.DeleteMessagesFrom(h.DB, sessionID, messageID+1); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/chat/%d", sessionID), http.StatusSeeOther)
}

// DeleteMessage removes a message and, per Lord's call, everything
// that came after it too — same cascade DeleteMessagesFrom already
// does for Regenerate/Edit, reused here rather than duplicated.
func (h ChatHandlers) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	sessionID, messageID, err := h.messageIDs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := db.DeleteMessagesFrom(h.DB, sessionID, messageID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/chat/%d", sessionID), http.StatusSeeOther)
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
	// http.CrossOriginProtection (wired in cmd/aivalon's newHandler)
	// explicitly treats GET as a "safe" method and never blocks it —
	// correct for a handler that only reads, wrong for this one: it
	// triggers a real LLM call (billed, on whatever backend the user
	// configured) and writes the reply to the DB, so a hostile page
	// doing nothing more than <img src="http://127.0.0.1:4140/chat/1/
	// stream"> would make this app spend the user's API credits and
	// pollute their session. Sec-Fetch-Site is sent by every browser
	// since 2023; a cross-site value means some other page initiated
	// this, not the app's own session page — reject that. Missing
	// entirely (old browser, or a non-browser client like curl) is let
	// through, same as CrossOriginProtection's own fallback, since
	// there's no signal to act on either way.
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	session, messages, err := h.loadSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if session.IsVaulted && !h.Gate.Unlocked(r) {
		http.Error(w, "locked", http.StatusForbidden)
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
		// Normalize any \r\n or bare \r to \n before the \n→<br>
		// replace below — SSE's spec treats a lone \r as its own line
		// terminator (same as \n), so an unnormalized \r surviving
		// into "data: ...\n\n" would make the browser's EventSource
		// parser split this one event into two, truncating what's
		// displayed. LLM output legitimately contains \r\n on some
		// backends/platforms.
		text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
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
		if err := MessageBlock(session.ID, d).Render(r.Context(), &final); err != nil {
			emitError(err)
			return
		}
	}
	final.WriteString(`</div>`)
	// SSE's framing is line-based: each physical line of an event's
	// payload needs its own "data:" prefix, or a bare newline inside it
	// is read as "end of this event" instead of literal content (per
	// the spec's multi-line data convention). MessageBlock's own markup
	// never contains one (templ drops insignificant whitespace between
	// tags), but its data-content attribute carries the message's raw
	// text verbatim — and a multi-paragraph narrator reply legitimately
	// contains '\n'. emitRaw splits on that so the final swap survives
	// unscathed instead of silently truncating at the first blank line.
	emitRaw(w, flusher, final.String())
}

// emitRaw writes payload as one SSE event, prefixing every line with
// "data: " per the spec's documented way to send multi-line data.
// Normalizes \r\n/\r to \n first for the same reason as emit above: a
// bare \r is its own SSE line terminator per spec, and MessageBlock's
// data-content attribute carries a message's raw text verbatim, which
// can legitimately contain \r\n.
func emitRaw(w http.ResponseWriter, flusher http.Flusher, payload string) {
	payload = strings.ReplaceAll(strings.ReplaceAll(payload, "\r\n", "\n"), "\r", "\n")
	for _, line := range strings.Split(payload, "\n") {
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprint(w, "\n")
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
	var lorebookContent string
	if session.LorebookID != 0 {
		entries, err := db.ListLorebookEntries(h.DB, session.LorebookID)
		if err != nil {
			emitError(err)
			return db.ChatMessage{}, false
		}
		lorebookContent = resolveLorebookContent(entries, lastUserMessageContent(history), defaultProbabilityRoll)
	}
	chatMessages, dropped := buildNarratorMessages(settings.SystemPromptTemplate, persona, characters, lorebookContent, history, settings.ContextBudgetChars)
	if dropped > 0 {
		// Never silent: logged server-side, and shown on the chat page
		// via an out-of-band swap into its #context-trim-notice live
		// region (same SSE+OOB mechanism as Stream's final swap, through
		// emitRaw so newlines can't break the event framing).
		log.Printf("session %d: left %d oldest message(s) out of the prompt to fit the %d-char context budget", session.ID, dropped, settings.ContextBudgetChars)
		var notice strings.Builder
		if err := ContextTrimNotice(dropped).Render(r.Context(), &notice); err == nil {
			flusher, _ := w.(http.Flusher)
			emitRaw(w, flusher, notice.String())
		}
	}
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
//
// budgetChars (AppSettings.ContextBudgetChars, 0 = unlimited) caps the
// total size — see truncateHistoryToBudget. The second return value is
// how many of the oldest history messages were left out to fit.
func buildNarratorMessages(template string, persona db.Persona, characters []db.CharacterCard, lorebookContent string, history []db.ChatMessage, budgetChars int) ([]llmclient.Message, int) {
	system := llmclient.Message{Role: "system", Content: buildNarratorPrompt(template, persona, characters, lorebookContent)}
	mapped := make([]llmclient.Message, 0, len(history))
	for _, m := range history {
		mapped = append(mapped, llmclient.Message{Role: historyRole(m), Content: namePrefixed(m, characters)})
	}
	kept, dropped := truncateHistoryToBudget(mapped, utf8.RuneCountInString(system.Content), budgetChars)
	return append([]llmclient.Message{system}, kept...), dropped
}

func trimNoticeText(dropped int) string {
	if dropped == 1 {
		return "1 earlier message was left out to fit the context budget —"
	}
	return fmt.Sprintf("%d earlier messages were left out to fit the context budget —", dropped)
}

// truncateHistoryToBudget drops the OLDEST history messages, one at a
// time, until system prompt + history fits in budgetChars characters
// (runes; ~4 per token — a deliberate approximation, real tokenizers
// differ per backend/model). It works on the already-mapped messages
// so the "Name: " prefixes namePrefixed adds are counted too.
//
//   - budgetChars <= 0: no limit, nothing dropped.
//   - the system prompt is never dropped or cut, even if it alone is
//     over budget — it's curated persona/lorebook/character content,
//     not disposable history (shrinking it is the user's call).
//   - the most recent message (the turn being answered) is always kept,
//     so the model is never sent a prompt with nothing to reply to.
func truncateHistoryToBudget(history []llmclient.Message, systemChars, budgetChars int) ([]llmclient.Message, int) {
	if budgetChars <= 0 {
		return history, 0
	}
	total := systemChars
	for _, m := range history {
		total += utf8.RuneCountInString(m.Content)
	}
	dropped := 0
	for total > budgetChars && len(history)-dropped > 1 {
		total -= utf8.RuneCountInString(history[dropped].Content)
		dropped++
	}
	return history[dropped:], dropped
}

// buildNarratorPrompt fills in the user-editable system prompt template
// (Settings → System prompt, stored as AppSettings.SystemPromptTemplate)
// with the live session state. Plain named placeholders, not Go's
// text/template — Lord edits this by hand and isn't a programmer (see
// USER.md), so there's no loop/conditional syntax to get wrong; an
// unrecognized or deleted placeholder just stays literal or disappears,
// it can never fail to render.
//
// {{lorebook}} resolves to whichever of the session's attached
// Lorebook's entries are active this turn (see resolveLorebookContent),
// or "" for a session with none attached.
func buildNarratorPrompt(template string, persona db.Persona, characters []db.CharacterCard, lorebookContent string) string {
	prompt := template
	prompt = strings.ReplaceAll(prompt, "{{persona_name}}", persona.Name)
	prompt = strings.ReplaceAll(prompt, "{{persona_description}}", persona.Description)
	prompt = strings.ReplaceAll(prompt, "{{characters}}", renderCharactersBlock(characters))
	prompt = strings.ReplaceAll(prompt, "{{lorebook}}", lorebookContent)
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
		if c.AuthorNote != "" {
			fmt.Fprintf(&b, "\nAuthor's note on how to play %s: %s", c.Name, c.AuthorNote)
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
			speaker = ""
		case "system":
			speaker = "System"
		}
		out = append(out, DisplayMessage{ID: m.ID, Role: m.Role, Speaker: speaker, Content: m.Content, ContentHTML: formatMessageContent(m.Content)})
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
