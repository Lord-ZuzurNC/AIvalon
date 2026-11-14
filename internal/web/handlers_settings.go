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
	profiles, err := db.ListBackendProfiles(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, SettingsPage(profiles, nil))
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
	profiles, err := db.ListBackendProfiles(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, SettingsPage(profiles, &profile))
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
	p := profileFromForm(r, db.BackendProfile{ID: id, Kind: "llm"})
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

// profileFromForm reads the plain POST form fields shared by Create and
// Update. ID/Kind come from the caller rather than the form: Kind isn't
// user-editable yet (v1 only ever creates "llm" profiles — see
// docs/DESIGN.md), and ID is never attacker-or-typo-controlled this way.
func profileFromForm(r *http.Request, base db.BackendProfile) db.BackendProfile {
	r.ParseForm()
	base.Name = r.FormValue("name")
	base.BaseURL = r.FormValue("base_url")
	base.APIKey = r.FormValue("api_key")
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
