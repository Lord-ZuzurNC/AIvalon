package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// Image presets' Settings-side CRUD. The chat only picks one per
// session (ChatHandlers.SetImagePreset); everything else lives here.

func (h SettingsHandlers) ImagePresetEditForm(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	p, err := db.GetImagePreset(h.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	h.render(w, r, nil, nil, &p, "")
}

func (h SettingsHandlers) ImagePresetCreate(w http.ResponseWriter, r *http.Request) {
	h.saveImagePreset(w, r, 0)
}

func (h SettingsHandlers) ImagePresetUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if _, err := db.GetImagePreset(h.DB, id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	h.saveImagePreset(w, r, id)
}

// saveImagePreset creates (id 0) or updates. A validation error or a
// taken name re-renders Settings with the dialog open, the error above
// the form, and the submitted values intact (422 so it's not mistaken
// for success).
func (h SettingsHandlers) saveImagePreset(w http.ResponseWriter, r *http.Request, id int64) {
	r.ParseForm()
	p, err := imagePresetFromForm(r)
	p.ID = id
	if err == nil && p.BackendProfileID != 0 {
		if bp, e := db.GetBackendProfile(h.DB, p.BackendProfileID); e != nil || bp.Kind != "image" {
			err = errors.New("the selected backend is not a valid image backend")
		}
	}
	if err == nil {
		if id == 0 {
			_, err = db.CreateImagePreset(h.DB, p)
		} else {
			err = db.UpdateImagePreset(h.DB, p)
		}
	}
	if err != nil {
		if errors.Is(err, db.ErrImagePresetExists) {
			err = fmt.Errorf("a preset named %q already exists — pick another name", p.Name)
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		h.render(w, r, nil, nil, &p, err.Error())
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h SettingsHandlers) ImagePresetDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	// Sessions using it fall back to defaults (ON DELETE SET NULL) —
	// nothing irreplaceable is lost, so no confirm step.
	if err := db.DeleteImagePreset(h.DB, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// imagePresetFromForm reads and bounds-checks the preset form. The
// bounds are sanity limits, not model limits — A1111 itself rejects or
// clamps anything its loaded checkpoint can't do. The returned preset
// always carries what was submitted (even on error) so the form can be
// re-rendered with it.
func imagePresetFromForm(r *http.Request) (db.ImagePreset, error) {
	p := db.ImagePreset{
		Name:           strings.TrimSpace(r.FormValue("name")),
		StyleSuffix:    strings.TrimSpace(r.FormValue("style_suffix")),
		NegativePrompt: strings.TrimSpace(r.FormValue("negative_prompt")),
		SamplerName:    strings.TrimSpace(r.FormValue("sampler_name")),
	}
	p.BackendProfileID, _ = strconv.ParseInt(r.FormValue("backend_profile_id"), 10, 64)
	var errs []string
	intField := func(name string, lo, hi int) int {
		v, e := strconv.Atoi(r.FormValue(name))
		if e != nil || v < lo || v > hi {
			errs = append(errs, fmt.Sprintf("%s must be a whole number from %d to %d", name, lo, hi))
		}
		return v
	}
	if p.Name == "" {
		errs = append(errs, "a name is required")
	}
	p.Steps = intField("steps", 1, 150)
	p.Width = intField("width", 64, 2048)
	p.Height = intField("height", 64, 2048)
	var e error
	if p.CFGScale, e = strconv.ParseFloat(r.FormValue("cfg_scale"), 64); e != nil || p.CFGScale < 1 || p.CFGScale > 30 {
		errs = append(errs, "cfg_scale must be a number from 1 to 30")
	}
	if p.Seed, e = strconv.ParseInt(r.FormValue("seed"), 10, 64); e != nil || p.Seed < -1 {
		errs = append(errs, "seed must be -1 (random) or a non-negative whole number")
	}
	if p.SamplerName == "" {
		errs = append(errs, "a sampler name is required")
	}
	if len(errs) > 0 {
		return p, errors.New(strings.Join(errs, "; "))
	}
	return p, nil
}
