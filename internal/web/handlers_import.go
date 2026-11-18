package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// uniqueName appends " (n)" (n starting at 2) until name isn't already
// in taken — Lord's call: importing the same character/lorebook twice
// should never silently overwrite or error, just land as a numbered
// copy, the same way "Duplicate" already works for sessions.
func uniqueName(name string, taken map[string]bool) string {
	if !taken[name] {
		return name
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s (%d)", name, n)
		if !taken[candidate] {
			return candidate
		}
	}
}

// nameFromUpload is the fallback name when the JSON itself doesn't
// carry one (true of SillyTavern's standalone World Info export,
// which has no top-level "name" field at all) — the uploaded
// filename, minus its extension.
func nameFromUpload(filename string) string {
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}

// --- Character Card V2/V3 shapes (shared by the character importer
// and, for its embedded book, the lorebook importer) ---

type charaCardFile struct {
	Data charaCardData `json:"data"`
}

type charaCardData struct {
	Name          string             `json:"name"`
	Description   string             `json:"description"`
	Personality   string             `json:"personality"`
	Scenario      string             `json:"scenario"`
	FirstMes      string             `json:"first_mes"`
	MesExample    string             `json:"mes_example"`
	CreatorNotes  string             `json:"creator_notes"`
	CharacterBook *characterBookJSON `json:"character_book"`
}

// characterBookJSON is the Character Card V2/V3 spec's own embedded
// World Info format (an array of entries) — close enough to
// db.LorebookEntry's own fields to map almost 1:1. It can arrive
// nested inside a full character card, or on its own if someone
// exported just the book.
type characterBookJSON struct {
	Name    string                   `json:"name"`
	Entries []characterBookEntryJSON `json:"entries"`
}

type characterBookEntryJSON struct {
	Keys           []string `json:"keys"`
	Content        string   `json:"content"`
	Enabled        bool     `json:"enabled"`
	Constant       bool     `json:"constant"`
	InsertionOrder int      `json:"insertion_order"`
	Probability    int      `json:"probability"`
}

// silWorldInfoJSON is SillyTavern's own standalone "World Info"
// export — describes the same kind of data as characterBookJSON but
// in a completely incompatible shape: entries keyed by a string index
// instead of an array, and every field under a different name (key vs
// keys, comment vs name, disable vs !enabled, order vs
// insertion_order). Both of Lord's example lorebooks (Solo Leveling,
// World of Aedros) are this format.
type silWorldInfoJSON struct {
	Entries map[string]silWorldInfoEntryJSON `json:"entries"`
}

type silWorldInfoEntryJSON struct {
	Key         []string `json:"key"`
	Content     string   `json:"content"`
	Constant    bool     `json:"constant"`
	Disable     bool     `json:"disable"`
	Order       int      `json:"order"`
	Probability int      `json:"probability"`
}

func lorebookEntriesFromCharacterBook(book characterBookJSON) []db.LorebookEntry {
	entries := make([]db.LorebookEntry, 0, len(book.Entries))
	for _, e := range book.Entries {
		entries = append(entries, db.LorebookEntry{
			Keys:            e.Keys,
			Content:         e.Content,
			TriggerStrategy: triggerStrategy(e.Constant),
			InsertionOrder:  e.InsertionOrder,
			Probability:     orDefault(e.Probability, 100),
			Enabled:         e.Enabled,
		})
	}
	return entries
}

func lorebookEntriesFromSillyTavern(book silWorldInfoJSON) []db.LorebookEntry {
	// Map iteration order is random in Go; sort by the entry's own
	// numeric key so import at least preserves the source file's
	// original order as a tiebreaker when "order" doesn't distinguish
	// two entries.
	keys := make([]string, 0, len(book.Entries))
	for k := range book.Entries {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ni, _ := strconv.Atoi(keys[i])
		nj, _ := strconv.Atoi(keys[j])
		return ni < nj
	})
	entries := make([]db.LorebookEntry, 0, len(book.Entries))
	for _, k := range keys {
		e := book.Entries[k]
		entries = append(entries, db.LorebookEntry{
			Keys:            e.Key,
			Content:         e.Content,
			TriggerStrategy: triggerStrategy(e.Constant),
			InsertionOrder:  e.Order,
			Probability:     orDefault(e.Probability, 100),
			Enabled:         !e.Disable,
		})
	}
	return entries
}

func triggerStrategy(constant bool) string {
	if constant {
		return "constant"
	}
	return "selective"
}

func orDefault(n, fallback int) int {
	if n == 0 {
		return fallback
	}
	return n
}

// parseLorebookJSON tries each known lorebook shape in turn and
// returns the book's own name (empty if the format doesn't carry one
// — see nameFromUpload) plus its entries converted to db.LorebookEntry.
// ok is false if raw didn't match any recognized shape, so the caller
// can report a clear error instead of silently importing zero entries.
func parseLorebookJSON(raw []byte) (name string, entries []db.LorebookEntry, ok bool) {
	var chara charaCardFile
	if err := json.Unmarshal(raw, &chara); err == nil && chara.Data.CharacterBook != nil && len(chara.Data.CharacterBook.Entries) > 0 {
		return chara.Data.CharacterBook.Name, lorebookEntriesFromCharacterBook(*chara.Data.CharacterBook), true
	}
	var book characterBookJSON
	if err := json.Unmarshal(raw, &book); err == nil && len(book.Entries) > 0 {
		return book.Name, lorebookEntriesFromCharacterBook(book), true
	}
	var sil silWorldInfoJSON
	if err := json.Unmarshal(raw, &sil); err == nil && len(sil.Entries) > 0 {
		return "", lorebookEntriesFromSillyTavern(sil), true
	}
	return "", nil, false
}

// parseCharacterJSON extracts the character-card fields from a
// chara_card_v2/v3 wrapper. ok is false if raw doesn't look like a
// character at all (most likely a bare lorebook JSON dropped into the
// wrong importer by mistake).
func parseCharacterJSON(raw []byte) (card db.CharacterCard, book *characterBookJSON, ok bool) {
	var chara charaCardFile
	if err := json.Unmarshal(raw, &chara); err != nil {
		return db.CharacterCard{}, nil, false
	}
	if chara.Data.Name == "" && chara.Data.Description == "" && chara.Data.FirstMes == "" {
		return db.CharacterCard{}, nil, false
	}
	card = db.CharacterCard{
		Name:            chara.Data.Name,
		Description:     chara.Data.Description,
		Personality:     chara.Data.Personality,
		Scenario:        chara.Data.Scenario,
		FirstMessage:    chara.Data.FirstMes,
		ExampleMessages: chara.Data.MesExample,
		AuthorNote:      chara.Data.CreatorNotes,
	}
	return card, chara.Data.CharacterBook, true
}

// readUploadedJSON pulls the "file" field out of a multipart POST,
// capped at maxUploadSize like every other upload in this app.
func readUploadedJSON(w http.ResponseWriter, r *http.Request) (raw []byte, filename string, err error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		return nil, "", fmt.Errorf("file too large or not a valid upload")
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, "", fmt.Errorf("no file uploaded")
	}
	defer file.Close()
	raw, err = io.ReadAll(file)
	if err != nil {
		return nil, "", err
	}
	return raw, header.Filename, nil
}

// createLorebookWithEntries is shared by both importers: makes the
// Lorebook row (name must already be de-duplicated by the caller —
// see uniqueName), then inserts every entry under it. On a mid-loop
// failure it stops rather than leaving a silent partial import; the
// lorebook itself still exists with whatever got in before the error,
// enough to retry (delete and re-import) rather than losing nothing
// at all.
func createLorebookWithEntries(sqlDB *sql.DB, name string, entries []db.LorebookEntry) (int64, error) {
	id, err := db.CreateLorebook(sqlDB, db.Lorebook{Name: name})
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		e.LorebookID = id
		if _, err := db.CreateLorebookEntry(sqlDB, e); err != nil {
			return id, fmt.Errorf("stopped partway through its entries: %w", err)
		}
	}
	return id, nil
}

func existingLorebookNames(sqlDB *sql.DB) (map[string]bool, error) {
	books, err := db.ListLorebooks(sqlDB)
	if err != nil {
		return nil, err
	}
	taken := make(map[string]bool, len(books))
	for _, b := range books {
		taken[b.Name] = true
	}
	return taken, nil
}

func existingCharacterNames(sqlDB *sql.DB) (map[string]bool, error) {
	cards, err := db.ListCharacterCards(sqlDB)
	if err != nil {
		return nil, err
	}
	taken := make(map[string]bool, len(cards))
	for _, c := range cards {
		taken[c.Name] = true
	}
	return taken, nil
}

// ImportLorebookJSON (World) accepts an uploaded lorebook JSON in any
// of the shapes parseLorebookJSON recognizes and creates a new
// Lorebook plus all its entries.
func (h WorldHandlers) ImportLorebookJSON(w http.ResponseWriter, r *http.Request) {
	raw, filename, err := readUploadedJSON(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name, entries, ok := parseLorebookJSON(raw)
	if !ok {
		http.Error(w, "unrecognized lorebook JSON — expected a SillyTavern World Info export, or a Character Card V2/V3 file with an embedded character_book", http.StatusBadRequest)
		return
	}
	if name == "" {
		name = nameFromUpload(filename)
	}
	taken, err := existingLorebookNames(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	id, err := createLorebookWithEntries(h.DB, uniqueName(name, taken), entries)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/world/%d", id), http.StatusSeeOther)
}

// ImportCharacterJSON (Character) accepts an uploaded Character Card
// V2/V3 JSON and creates the character; if it carries a non-empty
// embedded character_book, that's imported too as a separate
// Lorebook (AIvalon has no "character's own book" link, so this just
// makes both and leaves attaching the lorebook to a session as usual,
// same as any other independently-created book).
func (h CharacterHandlers) ImportCharacterJSON(w http.ResponseWriter, r *http.Request) {
	raw, filename, err := readUploadedJSON(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	card, book, ok := parseCharacterJSON(raw)
	if !ok {
		http.Error(w, "unrecognized character JSON — expected a Character Card V2/V3 file", http.StatusBadRequest)
		return
	}
	if card.Name == "" {
		card.Name = nameFromUpload(filename)
	}
	takenCards, err := existingCharacterNames(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	card.Name = uniqueName(card.Name, takenCards)
	charID, err := db.CreateCharacterCard(h.DB, card)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if book != nil && len(book.Entries) > 0 {
		bookName := book.Name
		if bookName == "" {
			bookName = card.Name + " lorebook"
		}
		taken, err := existingLorebookNames(h.DB)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := createLorebookWithEntries(h.DB, uniqueName(bookName, taken), lorebookEntriesFromCharacterBook(*book)); err != nil {
			http.Error(w, fmt.Sprintf("imported character %q but its embedded lorebook failed: %v", card.Name, err), http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/characters/%d/edit", charID), http.StatusSeeOther)
}
