package web

import (
	"database/sql"
	"fmt"
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
	render(w, r, CharacterPage(cards, nil, nil))
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
	images, err := db.ListCharacterCardImages(h.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, CharacterPage(cards, &card, images))
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

// saveImage adds the form's "image" field as a new gallery entry — a
// no-op (no error, no write) when the field was left empty, see
// saveUploadedImage. The very first image a character ever gets
// auto-becomes its primary (there's nothing to choose between yet);
// every later upload joins the gallery as secondary until explicitly
// promoted via PromoteImage.
func (h CharacterHandlers) saveImage(r *http.Request, id int64) error {
	imageID, err := db.CreateCharacterCardImage(h.DB, id, "")
	if err != nil {
		return err
	}
	imagePath, err := saveUploadedImage(r, "image", "characters", imageID)
	if err != nil {
		_ = db.DeleteCharacterCardImage(h.DB, imageID)
		return err
	}
	if imagePath == "" {
		// Nothing was actually uploaded — drop the placeholder row.
		return db.DeleteCharacterCardImage(h.DB, imageID)
	}
	if err := db.UpdateCharacterCardImagePath(h.DB, imageID, imagePath); err != nil {
		return err
	}
	hasPrimary, err := db.HasPrimaryImage(h.DB, id)
	if err != nil {
		return err
	}
	if !hasPrimary {
		return db.SetPrimaryCharacterCardImage(h.DB, id, imageID)
	}
	return nil
}

// PromoteImage makes an existing gallery image the character's primary
// — a pointer swap, no files touched. "Left as-is" isn't an option in
// the UI (the already-primary image doesn't get a Promote button), so
// no extra validation is needed here beyond the id itself being valid.
func (h CharacterHandlers) PromoteImage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	imageID, err := strconv.ParseInt(r.PathValue("imageID"), 10, 64)
	if err != nil {
		http.Error(w, "invalid image id", http.StatusBadRequest)
		return
	}
	if err := db.SetPrimaryCharacterCardImage(h.DB, id, imageID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/characters/%d/edit", id), http.StatusSeeOther)
}

// DeleteImage removes one gallery image. If it was the primary, the
// schema clears that pointer automatically (ON DELETE SET NULL) — see
// db.DeleteCharacterCardImage.
func (h CharacterHandlers) DeleteImage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	imageID, err := strconv.ParseInt(r.PathValue("imageID"), 10, 64)
	if err != nil {
		http.Error(w, "invalid image id", http.StatusBadRequest)
		return
	}
	if err := db.DeleteCharacterCardImage(h.DB, imageID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/characters/%d/edit", id), http.StatusSeeOther)
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
