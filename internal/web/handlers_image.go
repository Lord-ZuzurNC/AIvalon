package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
	"github.com/Lord-ZuzurNC/AIvalon/internal/imageclient"
	"github.com/Lord-ZuzurNC/AIvalon/internal/llmclient"
)

// ImageGenForm is what the chat page's image UI needs: the kind="image"
// backend profiles for the "Generate image" dialog, and the saved
// presets for the session panel's picker (and the dialog's
// "using preset X" note).
type ImageGenForm struct {
	Profiles []db.BackendProfile
	Presets  []db.ImagePreset
}

// activePreset is the session's chosen preset, or the zero value when
// none is chosen (or it no longer exists — GenerateImage falls back to
// defaults in both cases).
func (f ImageGenForm) activePreset(presetID int64) db.ImagePreset {
	for _, p := range f.Presets {
		if p.ID == presetID {
			return p
		}
	}
	return db.ImagePreset{}
}

// defaultImageParams is what a session with no preset chosen generates
// with — A1111's own txt2img defaults.
var defaultImageParams = db.ImagePreset{Steps: 20, CFGScale: 7, Width: 512, Height: 512, SamplerName: "Euler a", Seed: -1}

func (h ChatHandlers) imageGenForm() (ImageGenForm, error) {
	var f ImageGenForm
	profiles, err := db.ListBackendProfiles(h.DB)
	if err != nil {
		return f, err
	}
	f.Profiles = imageProfiles(profiles)
	f.Presets, err = db.ListImagePresets(h.DB)
	return f, err
}

// GenerateImage runs one txt2img call and appends the result to the
// session as an "image" message, using the session's chosen preset
// (sessions.image_preset_id) or defaultImageParams — only the prompt
// and backend come from the dialog. The message row is inserted first
// because its ID names the file (writeImageToDisk overwrites per entity
// ID); a failed save rolls the row back. Being a normal message row, it
// sorts like any other — whatever's posted next lands below it.
//
// Errors come back as a 200 fragment (TestResult's ❌ line) so htmx
// swaps them into the dialog; success redirects to the session.
func (h ChatHandlers) GenerateImage(w http.ResponseWriter, r *http.Request) {
	sessionID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if blocked, err := h.vaultBlocks(r, sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	} else if blocked {
		http.Error(w, "locked", http.StatusForbidden)
		return
	}
	fail := func(err error) { render(w, r, TestResult(err, nil, "")) }

	r.ParseForm()
	prompt := strings.TrimSpace(r.FormValue("prompt"))
	if prompt == "" {
		fail(errors.New("a prompt is required"))
		return
	}
	session, err := db.GetSession(h.DB, sessionID)
	if err != nil {
		fail(err)
		return
	}
	preset := defaultImageParams
	if session.ImagePresetID != 0 {
		// A vanished preset (deleted mid-flight) degrades to defaults
		// rather than failing the whole generation.
		if p, err := db.GetImagePreset(h.DB, session.ImagePresetID); err == nil {
			preset = p
		}
	}

	// A preset tied to a backend always wins over the dialog's manual
	// picker — the whole point of linking them (Lord's report:
	// "Realistic" must always mean Juggernaut, never a style/model
	// mismatch picked by hand) is that this decision shouldn't be
	// re-made every single generation once a preset has settled it.
	profileID := preset.BackendProfileID
	if profileID == 0 {
		profileID, _ = strconv.ParseInt(r.FormValue("backend_profile_id"), 10, 64)
	}
	profile, err := db.GetBackendProfile(h.DB, profileID)
	if err != nil || profile.Kind != "image" {
		fail(errors.New("pick an image backend profile"))
		return
	}
	params := imageclient.GenerationParams{
		Prompt: prompt, NegativePrompt: preset.NegativePrompt, Steps: preset.Steps, CFGScale: preset.CFGScale,
		Width: preset.Width, Height: preset.Height, SamplerName: preset.SamplerName, Seed: preset.Seed,
	}
	if preset.StyleSuffix != "" {
		params.Prompt += ", " + preset.StyleSuffix
	}

	result, err := imageclient.GenerateImage(r.Context(), profile, params)
	if err != nil {
		fail(err)
		return
	}

	content := fmt.Sprintf("%s\n(seed %d)", params.Prompt, result.Seed)
	msgID, err := db.CreateMessage(h.DB, db.ChatMessage{SessionID: sessionID, Role: "image", Content: content})
	if err != nil {
		fail(err)
		return
	}
	path, err := saveImageBytes(result.Image, "generated", msgID)
	if err == nil {
		err = db.SetMessageImagePath(h.DB, msgID, path)
	}
	if err != nil {
		_ = db.DeleteMessage(h.DB, msgID)
		fail(fmt.Errorf("saving generated image: %w", err))
		return
	}

	dest := fmt.Sprintf("/chat/%d", sessionID)
	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, dest, http.StatusSeeOther)
		return
	}
	w.Header().Set("HX-Redirect", dest)
}

// SetImagePreset is the session panel's preset picker — same plain
// POST-then-redirect shape as SetBackground. image_preset_id 0 (or
// empty) clears it back to defaultImageParams.
func (h ChatHandlers) SetImagePreset(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if blocked, err := h.vaultBlocks(r, id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	} else if blocked {
		http.Error(w, "locked", http.StatusForbidden)
		return
	}
	r.ParseForm()
	presetID, _ := strconv.ParseInt(r.FormValue("image_preset_id"), 10, 64)
	if presetID != 0 {
		if _, err := db.GetImagePreset(h.DB, presetID); err != nil {
			http.Error(w, "no such image preset", http.StatusBadRequest)
			return
		}
	}
	if err := db.SetSessionImagePreset(h.DB, id, presetID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/chat/%d", id), http.StatusSeeOther)
}

// suggestPromptTimeout bounds the one-shot LLM call: short relative to
// generateTimeout, since this is a few sentences of tags, not a full
// reply — a hung backend shouldn't leave the dialog spinning forever.
const suggestPromptTimeout = 60 * time.Second

// SuggestImagePrompt is the "one click, get an image" half of Lord's
// request: instead of staring at an empty textarea, the session's own
// LLM writes a candidate Stable Diffusion prompt from the current
// scene. The result comes back for review/editing (Lord's call — not
// a silent auto-send straight to the image backend) via the dialog's
// textarea fragment; nothing here is stored as a chat message, it's a
// throwaway side request.
func (h ChatHandlers) SuggestImagePrompt(w http.ResponseWriter, r *http.Request) {
	sessionID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if blocked, err := h.vaultBlocks(r, sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	} else if blocked {
		http.Error(w, "locked", http.StatusForbidden)
		return
	}
	fail := func(msg string) { render(w, r, imagePromptSuggestion("", msg)) }

	session, messages, err := h.loadSession(r)
	if err != nil {
		fail(err.Error())
		return
	}
	profile, err := db.GetBackendProfile(h.DB, session.BackendProfileID)
	if err != nil || profile.Kind != "llm" {
		fail("no LLM backend configured for this session")
		return
	}
	characters, err := db.ListCharacterCardsForSession(h.DB, sessionID)
	if err != nil {
		fail(err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), suggestPromptTimeout)
	defer cancel()
	var out strings.Builder
	instruction := buildImagePromptInstruction(h.resolvePersona(session), characters, messages)
	if err := llmclient.StreamChat(ctx, profile, instruction, func(tok string) { out.WriteString(tok) }); err != nil {
		fail(fmt.Sprintf("asking the model for a prompt: %v", err))
		return
	}
	suggestion := strings.TrimSpace(out.String())
	if suggestion == "" {
		fail("the model returned nothing — try again, or write the prompt by hand")
		return
	}
	render(w, r, imagePromptSuggestion(suggestion, ""))
}

// buildImagePromptInstruction is a one-shot, out-of-band request to
// the session's own LLM backend — not a chat turn, nothing here gets
// stored as a message. It asks for a plain comma-separated Stable
// Diffusion prompt, grounded in the characters' own cards (macros
// already resolved by renderCharactersBlock, same as the narrator's
// real context — a {{user}}/{{char}} leak here would be just as wrong
// as in the chat itself) plus a short tail of the recent conversation
// so the image matches the scene as it stands right now.
func buildImagePromptInstruction(persona db.Persona, characters []db.CharacterCard, messages []db.ChatMessage) []llmclient.Message {
	const system = "You write prompts for a Stable Diffusion image generator. " +
		"Reply with ONLY a comma-separated list of visual tags/phrases describing what the image should show — " +
		"appearance, pose, expression, setting, lighting. No narration, no quotes, no explanation, under 60 words."
	recent := messages
	if n := len(messages); n > 6 {
		recent = messages[n-6:]
	}
	var scene strings.Builder
	scene.WriteString(renderCharactersBlock(characters, persona.Name))
	var hasRecent bool
	for _, m := range recent {
		if m.Role == "image" {
			continue // a picture already generated, not scene description
		}
		if !hasRecent {
			scene.WriteString("\n\nRecent conversation:")
			hasRecent = true
		}
		fmt.Fprintf(&scene, "\n%s", namePrefixed(m, characters))
	}
	scene.WriteString("\n\nWrite the image prompt now.")
	return []llmclient.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: scene.String()},
	}
}
