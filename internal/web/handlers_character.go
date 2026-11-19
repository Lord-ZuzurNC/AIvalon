package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

type CharacterHandlers struct {
	DB    *sql.DB
	Cache *ImportCache
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
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	c := characterCardFromForm(r, db.CharacterCard{})
	id, err := db.CreateCharacterCard(h.DB, c)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.saveImage(w, r, id); err != nil {
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
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	c := characterCardFromForm(r, db.CharacterCard{ID: id})
	if err := db.UpdateCharacterCard(h.DB, c); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.saveImage(w, r, id); err != nil {
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
func (h CharacterHandlers) saveImage(w http.ResponseWriter, r *http.Request, id int64) error {
	imageID, err := db.CreateCharacterCardImage(h.DB, id, "")
	if err != nil {
		return err
	}
	imagePath, err := saveUploadedImage(w, r, "image", "characters", imageID)
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

// saveImageFromBytes mirrors saveImage's gallery mechanics (first
// image auto-becomes primary) for an image that didn't arrive as a
// multipart form field — the PNG an import uploaded, reused as the
// new character's own portrait rather than leaving it blank just
// because the bytes came from a Character Card PNG, not a plain
// <input type=file name=image> post. Any failure here is logged and
// swallowed by the caller: the character record it's filling in a
// picture for already imported successfully, and that shouldn't be
// undone over a secondary nice-to-have.
func (h CharacterHandlers) saveImageFromBytes(raw []byte, id int64) error {
	imageID, err := db.CreateCharacterCardImage(h.DB, id, "")
	if err != nil {
		return err
	}
	imagePath, err := saveImageBytes(raw, "characters", imageID)
	if err != nil {
		_ = db.DeleteCharacterCardImage(h.DB, imageID)
		return err
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
// — a pointer swap, no files touched.
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
	// Without this, a crafted request naming a DIFFERENT character's
	// {id} in the path than the image actually belongs to would still
	// succeed — the two path params aren't otherwise linked.
	img, err := db.GetCharacterCardImage(h.DB, imageID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if img.CharacterCardID != id {
		http.Error(w, "image does not belong to this character", http.StatusBadRequest)
		return
	}
	if err := db.SetPrimaryCharacterCardImage(h.DB, id, imageID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/characters/%d/edit", id), http.StatusSeeOther)
}

// DeleteImage removes one gallery image and its on-disk file. If it
// was the primary, the schema clears that pointer automatically (ON
// DELETE SET NULL) — see db.DeleteCharacterCardImage.
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
	img, err := db.GetCharacterCardImage(h.DB, imageID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if img.CharacterCardID != id {
		http.Error(w, "image does not belong to this character", http.StatusBadRequest)
		return
	}
	if err := db.DeleteCharacterCardImage(h.DB, imageID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	removeUploadedFile(img.ImagePath)
	http.Redirect(w, r, fmt.Sprintf("/characters/%d/edit", id), http.StatusSeeOther)
}

func (h CharacterHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	// Gallery rows cascade-delete with the character (ON DELETE
	// CASCADE, see migration 00006), but that only removes the DB
	// rows — their files would rot on disk under /static/uploads
	// forever otherwise, so list them first and unlink after the
	// character itself is gone.
	images, err := db.ListCharacterCardImages(h.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := db.DeleteCharacterCard(h.DB, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, img := range images {
		removeUploadedFile(img.ImagePath)
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
