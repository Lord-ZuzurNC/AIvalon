package web

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

type CharacterHandlers struct {
	DB *sql.DB
}

func (h CharacterHandlers) List(w http.ResponseWriter, r *http.Request) {
	cards, err := db.ListCharacterCards(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, CharacterPage(cards, nil))
}

func (h CharacterHandlers) EditForm(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	card, err := db.GetCharacterCard(h.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	cards, err := db.ListCharacterCards(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, CharacterPage(cards, &card))
}

func (h CharacterHandlers) Create(w http.ResponseWriter, r *http.Request) {
	c := characterCardFromForm(r, db.CharacterCard{})
	id, err := db.CreateCharacterCard(h.DB, c)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.saveImage(r, id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/characters", http.StatusSeeOther)
}

func (h CharacterHandlers) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	c := characterCardFromForm(r, db.CharacterCard{ID: id})
	if err := db.UpdateCharacterCard(h.DB, c); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.saveImage(r, id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/characters", http.StatusSeeOther)
}

// saveImage is a no-op (no error, no write) when the form's "image"
// field was left empty — see saveUploadedImage.
func (h CharacterHandlers) saveImage(r *http.Request, id int64) error {
	imagePath, err := saveUploadedImage(r, "image", "characters", id)
	if err != nil {
		return err
	}
	if imagePath == "" {
		return nil
	}
	return db.UpdateCharacterCardImagePath(h.DB, id, imagePath)
}

func (h CharacterHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := db.DeleteCharacterCard(h.DB, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/characters", http.StatusSeeOther)
}

func characterCardFromForm(r *http.Request, base db.CharacterCard) db.CharacterCard {
	// The create/edit form is multipart (it carries the optional image
	// file) — ParseForm alone skips multipart bodies entirely (see Go's
	// own doc comment on it), so the text fields would silently come back
	// empty without this.
	r.ParseMultipartForm(maxUploadSize)
	base.Name = r.FormValue("name")
	base.Description = r.FormValue("description")
	base.Personality = r.FormValue("personality")
	base.Scenario = r.FormValue("scenario")
	base.FirstMessage = r.FormValue("first_message")
	base.ExampleMessages = r.FormValue("example_messages")
	base.AuthorNote = r.FormValue("author_note")
	return base
}
