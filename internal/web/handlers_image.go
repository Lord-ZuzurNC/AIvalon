package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
	"github.com/Lord-ZuzurNC/AIvalon/internal/imageclient"
)

// ImageGenForm is what the chat page's image UI needs: the kind="image"
// backend profiles for the "Generate image" dialog, and the saved
// presets for the session panel's picker (and the dialog's
// "using preset X" note).
type ImageGenForm struct {
	Profiles []db.BackendProfile
	Presets  []db.ImagePreset
}

// activePresetName is the session's chosen preset's name, or "" when
// none is chosen (or it no longer exists — GenerateImage falls back to
// defaults in both cases).
func (f ImageGenForm) activePresetName(presetID int64) string {
	for _, p := range f.Presets {
		if p.ID == presetID {
			return p.Name
		}
	}
	return ""
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
	for _, p := range profiles {
		if p.Kind == "image" {
			f.Profiles = append(f.Profiles, p)
		}
	}
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
	profileID, _ := strconv.ParseInt(r.FormValue("backend_profile_id"), 10, 64)
	profile, err := db.GetBackendProfile(h.DB, profileID)
	if err != nil || profile.Kind != "image" {
		fail(errors.New("pick an image backend profile"))
		return
	}
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
