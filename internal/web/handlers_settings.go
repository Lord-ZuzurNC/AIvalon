package web

import (
	"database/sql"
	"net/http"
	"strconv"

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
	h.render(w, r, nil, nil)
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
	h.render(w, r, &profile, nil)
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
	h.render(w, r, nil, &persona)
}

func (h SettingsHandlers) PersonaCreate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	p := db.Persona{Name: r.FormValue("name"), Description: r.FormValue("description")}
	if _, err := db.CreatePersona(h.DB, p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
	r.ParseForm()
	p := db.Persona{ID: id, Name: r.FormValue("name"), Description: r.FormValue("description")}
	if err := db.UpdatePersona(h.DB, p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h SettingsHandlers) PersonaDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := db.DeletePersona(h.DB, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h SettingsHandlers) SystemPromptUpdate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	err := db.UpdateAppSettings(h.DB, db.AppSettings{SystemPromptTemplate: r.FormValue("system_prompt_template")})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h SettingsHandlers) SystemPromptReset(w http.ResponseWriter, r *http.Request) {
	err := db.UpdateAppSettings(h.DB, db.AppSettings{SystemPromptTemplate: db.DefaultSystemPromptTemplate})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// render loads both lists (profiles are cheap, there's never many) and
// renders the one Settings page with whichever form (if any) is being
// edited — same page, two independent edit states.
func (h SettingsHandlers) render(w http.ResponseWriter, r *http.Request, editingProfile *db.BackendProfile, editingPersona *db.Persona) {
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
	appSettings, err := db.GetAppSettings(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, SettingsPage(profiles, editingProfile, personas, editingPersona, appSettings.SystemPromptTemplate))
}

// profileFromForm reads the plain POST form fields shared by Create and
// Update. ID/Kind come from the caller rather than the form: Kind isn't
// user-editable yet (v1 only ever creates "llm" profiles — see
// docs/DESIGN.md), and ID is never attacker-or-typo-controlled this way.
func profileFromForm(r *http.Request, base db.BackendProfile) db.BackendProfile {
	r.ParseForm()
	base.Name = r.FormValue("name")
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
