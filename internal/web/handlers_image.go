package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
	"github.com/Lord-ZuzurNC/AIvalon/internal/imageclient"
)

// ImageGenForm is what the chat page's "Generate image" dialog needs:
// the kind="image" backend profiles to pick from, and the saved presets.
type ImageGenForm struct {
	Profiles []db.BackendProfile
	Presets  []db.ImagePreset
}

// defaultImageParams pre-fills the dialog before any preset is picked —
// A1111's own txt2img defaults.
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
// session as an "image" message. The message row is inserted first
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
	params, suffix, err := imageParamsFromForm(r)
	if err != nil {
		fail(err)
		return
	}
	if params.Prompt == "" {
		fail(errors.New("a prompt is required"))
		return
	}
	if suffix != "" {
		params.Prompt += ", " + suffix
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

// imageParamsFromForm reads and bounds-checks the dialog's fields. The
// bounds are sanity limits, not model limits — A1111 itself rejects or
// clamps anything its loaded checkpoint can't do.
func imageParamsFromForm(r *http.Request) (imageclient.GenerationParams, string, error) {
	p := imageclient.GenerationParams{
		Prompt:         strings.TrimSpace(r.FormValue("prompt")),
		NegativePrompt: strings.TrimSpace(r.FormValue("negative_prompt")),
		SamplerName:    strings.TrimSpace(r.FormValue("sampler_name")),
	}
	var err error
	intField := func(name string, lo, hi int) int {
		v, e := strconv.Atoi(r.FormValue(name))
		if err == nil && (e != nil || v < lo || v > hi) {
			err = fmt.Errorf("%s must be a whole number from %d to %d", name, lo, hi)
		}
		return v
	}
	p.Steps = intField("steps", 1, 150)
	p.Width = intField("width", 64, 2048)
	p.Height = intField("height", 64, 2048)
	if v, e := strconv.ParseFloat(r.FormValue("cfg_scale"), 64); e != nil || v < 1 || v > 30 {
		if err == nil {
			err = errors.New("cfg_scale must be a number from 1 to 30")
		}
	} else {
		p.CFGScale = v
	}
	if v, e := strconv.ParseInt(r.FormValue("seed"), 10, 64); e != nil || v < -1 {
		if err == nil {
			err = errors.New("seed must be -1 (random) or a non-negative whole number")
		}
	} else {
		p.Seed = v
	}
	if p.SamplerName == "" && err == nil {
		err = errors.New("a sampler name is required")
	}
	return p, strings.TrimSpace(r.FormValue("style_suffix")), err
}

// SaveImagePreset stores the dialog's CURRENT field values under
// preset_name. A taken name is refused unless the request carries
// overwrite=1 (the dialog's explicit "Overwrite" checkbox) — never a
// silent clobber.
func (h ChatHandlers) SaveImagePreset(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	name := strings.TrimSpace(r.FormValue("preset_name"))
	if name == "" {
		h.presetResult(w, r, "Give the preset a name first.", false, 0)
		return
	}
	params, suffix, err := imageParamsFromForm(r)
	if err != nil {
		h.presetResult(w, r, err.Error(), false, 0)
		return
	}
	preset := db.ImagePreset{
		Name: name, StyleSuffix: suffix, NegativePrompt: params.NegativePrompt, Steps: params.Steps,
		CFGScale: params.CFGScale, Width: params.Width, Height: params.Height, SamplerName: params.SamplerName, Seed: params.Seed,
	}

	id, err := db.CreateImagePreset(h.DB, preset)
	if errors.Is(err, db.ErrImagePresetExists) {
		if r.FormValue("overwrite") != "1" {
			h.presetResult(w, r, fmt.Sprintf("A preset named %q already exists — tick “Overwrite” to replace it, or pick another name.", name), false, 0)
			return
		}
		presets, lerr := db.ListImagePresets(h.DB)
		if lerr != nil {
			h.presetResult(w, r, lerr.Error(), false, 0)
			return
		}
		for _, p := range presets {
			if strings.EqualFold(p.Name, name) {
				preset.ID, preset.Name = p.ID, p.Name // keep the existing name's casing
			}
		}
		id, err = preset.ID, db.UpdateImagePreset(h.DB, preset)
	}
	if err != nil {
		h.presetResult(w, r, err.Error(), false, 0)
		return
	}
	h.presetResult(w, r, fmt.Sprintf("Saved preset %q.", preset.Name), true, id)
}

func (h ChatHandlers) DeleteImagePreset(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id, err := strconv.ParseInt(r.FormValue("preset_id"), 10, 64)
	if err != nil {
		h.presetResult(w, r, "Pick a preset to delete first.", false, 0)
		return
	}
	if err := db.DeleteImagePreset(h.DB, id); err != nil {
		h.presetResult(w, r, err.Error(), false, 0)
		return
	}
	h.presetResult(w, r, "Preset deleted.", true, 0)
}

// presetResult renders the dialog's preset notice and, on success, an
// out-of-band refresh of the preset <select> (selecting selectedID).
func (h ChatHandlers) presetResult(w http.ResponseWriter, r *http.Request, msg string, ok bool, selectedID int64) {
	var presets []db.ImagePreset
	if ok {
		var err error
		if presets, err = db.ListImagePresets(h.DB); err != nil {
			msg, ok = err.Error(), false
		}
	}
	render(w, r, ImagePresetResult(msg, ok, presets, selectedID))
}

// presetJSON is one <option>'s data-preset blob, keyed by the dialog's
// own field names so applyImagePreset can copy it over generically.
func presetJSON(p db.ImagePreset) string {
	b, _ := json.Marshal(map[string]any{
		"preset_name": p.Name, "style_suffix": p.StyleSuffix, "negative_prompt": p.NegativePrompt,
		"steps": p.Steps, "cfg_scale": p.CFGScale, "width": p.Width, "height": p.Height,
		"sampler_name": p.SamplerName, "seed": p.Seed,
	})
	return string(b)
}
