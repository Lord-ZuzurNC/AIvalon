package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// SettingsHandlers groups the Settings-page HTTP handlers; they all need the
// *sql.DB, so a small receiver is less repetition than threading it through
// every function signature.
type SettingsHandlers struct {
	DB *sql.DB
}

func (h SettingsHandlers) List(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, nil, nil, nil, "")
}

func (h SettingsHandlers) EditForm(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	profile, err := db.GetBackendProfile(h.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	h.render(w, r, &profile, nil, nil, "")
}

func (h SettingsHandlers) Create(w http.ResponseWriter, r *http.Request) {
	p := profileFromForm(r, db.BackendProfile{Kind: "llm"})
	if _, err := db.CreateBackendProfile(h.DB, p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h SettingsHandlers) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	// Load the existing row first so an empty "api_key" field in the
	// submitted form means "keep the current key", not "clear it" —
	// the form never shows the real key back (see backendProfileForm),
	// so a blank field is the normal, expected state on every edit
	// that isn't specifically changing the key.
	existing, err := db.GetBackendProfile(h.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	p := profileFromForm(r, existing)
	if err := db.UpdateBackendProfile(h.DB, p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h SettingsHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := db.DeleteBackendProfile(h.DB, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// ProfilePing is the recurring healthcheck — a bare TCP dial to the
// profile's own host:port, re-polled every 5s by htmx
// (hx-trigger="load, every 5s" in settings.templ's list row) rather
// than a hand-rolled JS interval, since htmx is already the app's
// one SSE/polling dependency. Returns just the ●/● dot fragment, not
// a full page.
func (h SettingsHandlers) ProfilePing(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	profile, err := db.GetBackendProfile(h.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	render(w, r, PingDot(pingBackend(profile.BaseURL) == nil))
}

// ProfileTest is the on-demand "Test" button inside the add/edit
// form — it reads the form's OWN current fields (not the stored
// profile) so testing works before a new profile has ever been
// saved, and falls back to the stored API key when the field was
// left blank with an existing id, same "blank means keep it" rule
// Update already applies.
func (h SettingsHandlers) ProfileTest(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	profile := db.BackendProfile{Kind: r.FormValue("kind"), BaseURL: r.FormValue("base_url"), APIKey: r.FormValue("api_key")}
	if profile.APIKey == "" {
		if idStr := r.FormValue("id"); idStr != "" {
			if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
				if existing, err := db.GetBackendProfile(h.DB, id); err == nil {
					profile.APIKey = existing.APIKey
				}
			}
		}
	}
	models, err := testBackendConnection(profile)
	render(w, r, TestResult(err, models, r.FormValue("model")))
}

func (h SettingsHandlers) PersonaEditForm(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	persona, err := db.GetPersona(h.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	h.render(w, r, nil, &persona, nil, "")
}

func (h SettingsHandlers) PersonaCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	r.ParseMultipartForm(maxUploadSize)
	p := db.Persona{Name: r.FormValue("name"), Description: r.FormValue("description")}
	id, err := db.CreatePersona(h.DB, p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.savePersonaImage(w, r, id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h SettingsHandlers) PersonaUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	r.ParseMultipartForm(maxUploadSize)
	p := db.Persona{ID: id, Name: r.FormValue("name"), Description: r.FormValue("description")}
	if err := db.UpdatePersona(h.DB, p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.savePersonaImage(w, r, id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// savePersonaImage is a no-op (no error, no write) when the form's
// "image" field was left empty — see saveUploadedImage. One image per
// persona, like Lorebook's own single-cover pattern, not a gallery
// (Character's is the only multi-image case, since swapping between
// several portraits of the same AI-played character is the one place
// that's actually come up).
func (h SettingsHandlers) savePersonaImage(w http.ResponseWriter, r *http.Request, id int64) error {
	imagePath, err := saveUploadedImage(w, r, "image", "personas", id)
	if err != nil {
		return err
	}
	if imagePath == "" {
		return nil
	}
	return db.UpdatePersonaImagePath(h.DB, id, imagePath)
}

func (h SettingsHandlers) PersonaDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	persona, err := db.GetPersona(h.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	sessions, err := db.ListSessionsUsingPersona(h.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(sessions) > 0 && r.FormValue("confirm") != "1" {
		render(w, r, DeleteConfirmPage("Settings", persona.Name, fmt.Sprintf("/settings/personas/%d/delete", id), "/settings", sessions))
		return
	}
	orphanedImages, err := db.DeletePersona(h.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	removeUploadedFile(persona.ImagePath)
	for _, p := range orphanedImages {
		removeUploadedFile(p)
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h SettingsHandlers) SystemPromptUpdate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	h.updateAppSettings(w, r, func(s *db.AppSettings) error {
		s.SystemPromptTemplate = r.FormValue("system_prompt_template")
		return nil
	})
}

func (h SettingsHandlers) SystemPromptReset(w http.ResponseWriter, r *http.Request) {
	h.updateAppSettings(w, r, func(s *db.AppSettings) error {
		s.SystemPromptTemplate = db.DefaultSystemPromptTemplate
		return nil
	})
}

func (h SettingsHandlers) ImportWhitelistUpdate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	h.updateAppSettings(w, r, func(s *db.AppSettings) error {
		s.PNGImportWhitelist = db.SplitHostList(r.FormValue("png_import_whitelist"))
		return nil
	})
}

func (h SettingsHandlers) ContextBudgetUpdate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	h.updateAppSettings(w, r, func(s *db.AppSettings) error {
		n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("context_budget_chars")))
		if err != nil || n < 0 {
			return fmt.Errorf("context budget must be a whole number, 0 or more (0 = no limit)")
		}
		s.ContextBudgetChars = n
		return nil
	})
}

// updateAppSettings is the read-modify-write every AppSettings form
// goes through: UpdateAppSettings writes every column, so starting
// from the stored row is what keeps one form from wiping the others.
// A non-nil error from change is the user's input being wrong (400).
func (h SettingsHandlers) updateAppSettings(w http.ResponseWriter, r *http.Request, change func(*db.AppSettings) error) {
	s, err := db.GetAppSettings(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := change(&s); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := db.UpdateAppSettings(h.DB, s); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// imageProfiles filters to backend profiles an image preset (or the
// in-chat generation dialog) can actually target — mirrors the
// llm-only filter ChatHandlers.List applies for the opposite case.
func imageProfiles(all []db.BackendProfile) []db.BackendProfile {
	out := make([]db.BackendProfile, 0, len(all))
	for _, p := range all {
		if p.Kind == "image" {
			out = append(out, p)
		}
	}
	return out
}

// render loads every list (all small) and renders the one Settings page
// with whichever form (if any) is being edited — same page, independent
// edit states. imagePresetErr is shown above the image preset form
// (non-empty only when re-rendering a rejected submit).
func (h SettingsHandlers) render(w http.ResponseWriter, r *http.Request, editingProfile *db.BackendProfile, editingPersona *db.Persona, editingImagePreset *db.ImagePreset, imagePresetErr string) {
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
	imagePresets, err := db.ListImagePresets(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	appSettings, err := db.GetAppSettings(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, SettingsPage(profiles, editingProfile, personas, editingPersona, imagePresets, editingImagePreset, imagePresetErr, appSettings))
}

// profileFromForm reads the plain POST form fields shared by Create and
// Update. ID comes from the caller rather than the form (never attacker-
// or typo-controlled this way). Kind now comes from the form (the
// selector in backendProfileForm) — an empty/unrecognized value keeps
// whatever base.Kind already was (Create's "llm" default, or Update's
// existing persisted value) rather than silently resetting it.
func profileFromForm(r *http.Request, base db.BackendProfile) db.BackendProfile {
	r.ParseForm()
	base.Name = r.FormValue("name")
	if kind := r.FormValue("kind"); kind == "llm" || kind == "image" {
		base.Kind = kind
	}
	base.BaseURL = r.FormValue("base_url")
	base.Model = r.FormValue("model")
	// An empty field means "leave the existing key alone" (Create's
	// base has no key yet, so this is a no-op there) — see Update's
	// comment for why the form is never pre-filled with the real key.
	if apiKey := r.FormValue("api_key"); apiKey != "" {
		base.APIKey = apiKey
	}
	base.ModelParams = map[string]float64{}
	if v, err := strconv.ParseFloat(r.FormValue("temperature"), 64); err == nil {
		base.ModelParams["temperature"] = v
	}
	if v, err := strconv.ParseFloat(r.FormValue("top_p"), 64); err == nil {
		base.ModelParams["top_p"] = v
	}
	return base
}

// render is the one place that turns a templ.Component into an HTTP
// response, so every page handler does it the same way.
func render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
