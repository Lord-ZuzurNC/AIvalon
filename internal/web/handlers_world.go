package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

type WorldHandlers struct {
	DB    *sql.DB
	Cache *ImportCache
}

func (h WorldHandlers) List(w http.ResponseWriter, r *http.Request) {
	books, err := db.ListLorebooks(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, WorldPage(books))
}

func (h WorldHandlers) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	r.ParseMultipartForm(maxUploadSize)
	id, err := db.CreateLorebook(h.DB, db.Lorebook{Name: r.FormValue("name")})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.saveImage(w, r, id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/world/%d", id), http.StatusSeeOther)
}

func (h WorldHandlers) Show(w http.ResponseWriter, r *http.Request) {
	book, entries, err := h.loadBook(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	render(w, r, LorebookPage(book, entries, nil))
}

func (h WorldHandlers) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	r.ParseMultipartForm(maxUploadSize)
	if err := db.UpdateLorebook(h.DB, db.Lorebook{ID: id, Name: r.FormValue("name")}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.saveImage(w, r, id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/world/%d", id), http.StatusSeeOther)
}

// saveImage is a no-op (no error, no write) when the form's "image"
// field was left empty — see saveUploadedImage.
func (h WorldHandlers) saveImage(w http.ResponseWriter, r *http.Request, id int64) error {
	imagePath, err := saveUploadedImage(w, r, "image", "lorebooks", id)
	if err != nil {
		return err
	}
	if imagePath == "" {
		return nil
	}
	return db.UpdateLorebookImagePath(h.DB, id, imagePath)
}

// saveImageFromBytes is saveImage's counterpart for an image that
// didn't arrive as a multipart form field — the PNG a lorebook import
// uploaded, reused as its own cover rather than leaving it blank just
// because the bytes came from a Character Card PNG with an embedded
// character_book, not a plain <input type=file name=image> post.
func (h WorldHandlers) saveImageFromBytes(raw []byte, id int64) error {
	imagePath, err := saveImageBytes(raw, "lorebooks", id)
	if err != nil {
		return err
	}
	return db.UpdateLorebookImagePath(h.DB, id, imagePath)
}

func (h WorldHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	book, err := db.GetLorebook(h.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := db.DeleteLorebook(h.DB, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	removeUploadedFile(book.ImagePath)
	http.Redirect(w, r, "/world", http.StatusSeeOther)
}

func (h WorldHandlers) EntryEditForm(w http.ResponseWriter, r *http.Request) {
	book, entries, err := h.loadBook(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	entryID, err := strconv.ParseInt(r.PathValue("entryID"), 10, 64)
	if err != nil {
		http.Error(w, "invalid entry id", http.StatusBadRequest)
		return
	}
	entry, err := db.GetLorebookEntry(h.DB, entryID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if entry.LorebookID != book.ID {
		http.Error(w, "entry does not belong to this lorebook", http.StatusBadRequest)
		return
	}
	render(w, r, LorebookPage(book, entries, &entry))
}

func (h WorldHandlers) EntryCreate(w http.ResponseWriter, r *http.Request) {
	bookID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	entry := entryFromForm(r, db.LorebookEntry{LorebookID: bookID})
	if _, err := db.CreateLorebookEntry(h.DB, entry); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/world/%d", bookID), http.StatusSeeOther)
}

func (h WorldHandlers) EntryUpdate(w http.ResponseWriter, r *http.Request) {
	bookID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	entryID, err := strconv.ParseInt(r.PathValue("entryID"), 10, 64)
	if err != nil {
		http.Error(w, "invalid entry id", http.StatusBadRequest)
		return
	}
	if existing, err := db.GetLorebookEntry(h.DB, entryID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	} else if existing.LorebookID != bookID {
		http.Error(w, "entry does not belong to this lorebook", http.StatusBadRequest)
		return
	}
	entry := entryFromForm(r, db.LorebookEntry{ID: entryID, LorebookID: bookID})
	if err := db.UpdateLorebookEntry(h.DB, entry); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/world/%d", bookID), http.StatusSeeOther)
}

// EntryToggleEnabled flips an entry's Enabled flag in place — the
// ✅/❌ cell in LorebookPage's table is itself the toggle now (Lord's
// ask), rather than needing the full edit modal just to switch a key
// on or off.
func (h WorldHandlers) EntryToggleEnabled(w http.ResponseWriter, r *http.Request) {
	bookID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	entryID, err := strconv.ParseInt(r.PathValue("entryID"), 10, 64)
	if err != nil {
		http.Error(w, "invalid entry id", http.StatusBadRequest)
		return
	}
	entry, err := db.GetLorebookEntry(h.DB, entryID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if entry.LorebookID != bookID {
		http.Error(w, "entry does not belong to this lorebook", http.StatusBadRequest)
		return
	}
	entry.Enabled = !entry.Enabled
	if err := db.UpdateLorebookEntry(h.DB, entry); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/world/%d", bookID), http.StatusSeeOther)
}

func (h WorldHandlers) EntryDelete(w http.ResponseWriter, r *http.Request) {
	bookID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	entryID, err := strconv.ParseInt(r.PathValue("entryID"), 10, 64)
	if err != nil {
		http.Error(w, "invalid entry id", http.StatusBadRequest)
		return
	}
	if existing, err := db.GetLorebookEntry(h.DB, entryID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	} else if existing.LorebookID != bookID {
		http.Error(w, "entry does not belong to this lorebook", http.StatusBadRequest)
		return
	}
	if err := db.DeleteLorebookEntry(h.DB, entryID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/world/%d", bookID), http.StatusSeeOther)
}

func (h WorldHandlers) loadBook(r *http.Request) (db.Lorebook, []db.LorebookEntry, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return db.Lorebook{}, nil, fmt.Errorf("invalid id")
	}
	book, err := db.GetLorebook(h.DB, id)
	if err != nil {
		return db.Lorebook{}, nil, err
	}
	entries, err := db.ListLorebookEntries(h.DB, id)
	if err != nil {
		return db.Lorebook{}, nil, err
	}
	return book, entries, nil
}

func entryFromForm(r *http.Request, base db.LorebookEntry) db.LorebookEntry {
	r.ParseForm()
	base.Keys = splitKeys(r.FormValue("keys"))
	base.Content = r.FormValue("content")
	base.TriggerStrategy = r.FormValue("trigger_strategy")
	if base.TriggerStrategy == "" {
		base.TriggerStrategy = "selective"
	}
	if v, err := strconv.Atoi(r.FormValue("insertion_order")); err == nil {
		base.InsertionOrder = v
	}
	base.Probability = 100
	if v, err := strconv.Atoi(r.FormValue("probability")); err == nil {
		base.Probability = v
	}
	base.Enabled = r.FormValue("enabled") != ""
	return base
}

// joinKeys/splitKeys convert between the []string the DB layer uses and
// the plain comma-separated text a non-programmer can type into one
// input field — see USER.md, Lord isn't a developer.
func joinKeys(keys []string) string {
	return strings.Join(keys, ", ")
}

func splitKeys(s string) []string {
	var keys []string
	for _, part := range strings.Split(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			keys = append(keys, trimmed)
		}
	}
	return keys
}
