package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// importURLTimeout bounds the "From URL" tab's server-side fetch —
// generous enough for a slow host, short enough that a stalled or
// hanging remote doesn't tie up the request indefinitely.
const importURLTimeout = 20 * time.Second

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

// readUploadOrURL accepts either tab of ImportModal: a multipart
// "file" field (the "From your Computer" tab, capped at
// maxUploadSize like every other upload in this app), or a plain
// "url" field (the "From URL" tab) that gets fetched server-side —
// see fetchImportURL. Either way the caller gets the same (raw,
// filename) shape back and never needs to know which tab was used.
func readUploadOrURL(w http.ResponseWriter, r *http.Request, sqlDB *sql.DB) (raw []byte, filename string, err error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(maxUploadSize); err != nil {
			return nil, "", fmt.Errorf("file too large or not a valid upload")
		}
		if file, header, ferr := r.FormFile("file"); ferr == nil {
			defer file.Close()
			raw, err = io.ReadAll(file)
			if err != nil {
				return nil, "", err
			}
			return raw, header.Filename, nil
		}
		// A multipart POST without a "file" field falls through to
		// the "url" field below rather than failing outright — lets
		// both of ImportModal's tabs genuinely share one <form
		// enctype> if a future tweak ever merges them.
	} else if err := r.ParseForm(); err != nil {
		return nil, "", fmt.Errorf("invalid form")
	}
	rawURL := strings.TrimSpace(r.FormValue("url"))
	if rawURL == "" {
		return nil, "", fmt.Errorf("no file uploaded and no URL given")
	}
	settings, err := db.GetAppSettings(sqlDB)
	if err != nil {
		return nil, "", err
	}
	return fetchImportURL(rawURL, settings.PNGImportWhitelist)
}

// isLoopbackHost: the only hosts the URL import trusts unconditionally
// (any content type) — this machine itself, where a user is most
// likely serving their own card files from.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// checkImportHost is the SSRF gate for "From URL" imports, applied
// before any connection is made (and again to every redirect hop):
// loopback always passes; any other host must exactly match (case-
// insensitive, port ignored — u.Hostname() already strips both port
// and IPv6 brackets) an entry in Settings' PNG import whitelist, and
// then only for a Character Card PNG (requirePNG — checked on the
// downloaded bytes by checkImportBody, since nothing before the
// download can prove what a remote host will actually send).
//
// ponytail: matches the hostname only, not its resolved IP — a
// whitelisted name whose DNS points at a LAN address would still be
// fetched. Pin/validate the dialed IP (net.Dialer.Control) if the
// whitelist ever holds hosts that aren't trusted public sites.
func checkImportHost(u *url.URL, whitelist []string) (requirePNG bool, err error) {
	host := strings.ToLower(u.Hostname())
	if isLoopbackHost(host) {
		return false, nil
	}
	for _, allowed := range whitelist {
		if host == strings.ToLower(allowed) {
			return true, nil
		}
	}
	return false, fmt.Errorf("imports from %q aren't allowed — only 127.0.0.1 and the domains listed in Settings → Imports (Character Card PNGs only) are; add %q there if you trust it", host, host)
}

// checkImportBody enforces checkImportHost's requirePNG on what was
// actually downloaded — an untrusted host's non-PNG bytes are refused
// outright rather than falling through to JSON parsing.
func checkImportBody(raw []byte, host string, requirePNG bool) error {
	if requirePNG && !bytes.HasPrefix(raw, pngSignature) {
		return fmt.Errorf("%s is whitelisted for Character Card PNG imports only, got something else (JSON imports are only allowed from 127.0.0.1)", host)
	}
	return nil
}

// importHTTPClient is the "From URL" fetcher. CheckRedirect re-runs the
// host gate on every hop, so a whitelisted host can't bounce the
// request onward to some internal address. A package var (not built
// per call) only so tests can point its Transport at a local server.
var importHTTPClient = &http.Client{
	Timeout: importURLTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		whitelist, _ := req.Context().Value(importWhitelistKey{}).([]string)
		_, err := checkImportHost(req.URL, whitelist)
		return err
	},
}

// importWhitelistKey carries the whitelist into CheckRedirect, which
// only sees the request, not fetchImportURL's arguments.
type importWhitelistKey struct{}

// fetchImportURL downloads rawURL server-side for the "From URL" tab
// — it must point directly at a .json or a Character Card .png
// (same formats readUploadOrURL's other branch accepts), not a page
// that merely links to one; nothing here follows links or scrapes
// HTML.
func fetchImportURL(rawURL string, whitelist []string) (raw []byte, filename string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, "", fmt.Errorf("invalid URL — must be http(s) and point directly to a .json or .png file")
	}
	requirePNG, err := checkImportHost(u, whitelist)
	if err != nil {
		return nil, "", err
	}
	ctx := context.WithValue(context.Background(), importWhitelistKey{}, whitelist)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("invalid URL: %w", err)
	}
	resp, err := importHTTPClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetching URL: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("URL returned HTTP %d", resp.StatusCode)
	}
	raw, err = io.ReadAll(io.LimitReader(resp.Body, maxUploadSize+1))
	if err != nil {
		return nil, "", fmt.Errorf("reading URL response: %w", err)
	}
	if len(raw) > maxUploadSize {
		return nil, "", fmt.Errorf("file at URL is too large (over %d MiB)", maxUploadSize>>20)
	}
	// A redirect may have landed somewhere else (already host-gated by
	// CheckRedirect) — if either end is non-loopback, it's PNG-only.
	finalHost := resp.Request.URL.Hostname()
	if !isLoopbackHost(finalHost) {
		requirePNG = true
	}
	if err := checkImportBody(raw, finalHost, requirePNG); err != nil {
		return nil, "", err
	}
	filename = path.Base(u.Path)
	if filename == "" || filename == "/" || filename == "." {
		filename = "import"
	}
	return raw, filename, nil
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
	books, err := db.ListLorebooks(sqlDB, -1, 0)
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
	cards, err := db.ListCharacterCards(sqlDB, -1, 0)
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
// Lorebook plus all its entries. If the upload actually parses as a
// Character Card instead, it offers to finish the import in
// Character rather than just failing (see ImportCrossConfirmPage).
func (h WorldHandlers) ImportLorebookJSON(w http.ResponseWriter, r *http.Request) {
	raw, filename, err := readUploadOrURL(w, r, h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.importLorebookUpload(w, r, raw, filename)
}

// ResumeCharacterImport finishes, as a Character, an upload that was
// first dropped into World's lorebook importer by mistake.
func (h CharacterHandlers) ResumeCharacterImport(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	raw, filename, ok := h.Cache.Take(r.FormValue("token"))
	if !ok {
		http.Error(w, "this import link expired — upload the file again", http.StatusBadRequest)
		return
	}
	h.importCharacterUpload(w, r, raw, filename)
}

func (h WorldHandlers) importLorebookUpload(w http.ResponseWriter, r *http.Request, raw []byte, filename string) {
	originalRaw := raw // kept in case it's a PNG — reused as the lorebook's own cover, see below
	raw, err := extractJSONFromUpload(raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name, entries, ok := parseLorebookJSON(raw)
	if !ok {
		if card, _, charOK := parseCharacterJSON(raw); charOK {
			token, err := h.Cache.Stash(originalRaw, filename)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			who := card.Name
			if who == "" {
				who = nameFromUpload(filename)
			}
			render(w, r, ImportCrossConfirmPage(
				"Worlds",
				fmt.Sprintf("This is a Character Card JSON (%q), not a Lorebook — import it in Character instead?", who),
				"/characters/import/resume", "/world", token,
			))
			return
		}
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
	if bytes.HasPrefix(originalRaw, pngSignature) {
		// The source PNG is itself the card's own art — use it as the
		// lorebook's cover too. A failure here doesn't undo the import
		// that already succeeded; the lorebook just keeps no image.
		_ = h.saveImageFromBytes(originalRaw, id)
	}
	http.Redirect(w, r, fmt.Sprintf("/world/%d", id), http.StatusSeeOther)
}

// ImportCharacterJSON (Character) accepts an uploaded Character Card
// V2/V3 JSON and creates the character; if it carries a non-empty
// embedded character_book, that's imported too as a separate
// Lorebook (AIvalon has no "character's own book" link, so this just
// makes both and leaves attaching the lorebook to a session as usual,
// same as any other independently-created book). If the upload
// actually parses as a bare lorebook instead, it offers to finish the
// import in World (see ImportCrossConfirmPage).
func (h CharacterHandlers) ImportCharacterJSON(w http.ResponseWriter, r *http.Request) {
	raw, filename, err := readUploadOrURL(w, r, h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.importCharacterUpload(w, r, raw, filename)
}

// ResumeLorebookImport finishes, as a Lorebook, an upload that was
// first dropped into Character's importer by mistake.
func (h WorldHandlers) ResumeLorebookImport(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	raw, filename, ok := h.Cache.Take(r.FormValue("token"))
	if !ok {
		http.Error(w, "this import link expired — upload the file again", http.StatusBadRequest)
		return
	}
	h.importLorebookUpload(w, r, raw, filename)
}

func (h CharacterHandlers) importCharacterUpload(w http.ResponseWriter, r *http.Request, raw []byte, filename string) {
	originalRaw := raw // kept in case it's a PNG — reused as the character's (and its embedded lorebook's) own image, see below
	raw, err := extractJSONFromUpload(raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	card, book, ok := parseCharacterJSON(raw)
	if !ok {
		if _, entries, lbOK := parseLorebookJSON(raw); lbOK {
			token, err := h.Cache.Stash(originalRaw, filename)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			who := nameFromUpload(filename)
			render(w, r, ImportCrossConfirmPage(
				"Characters",
				fmt.Sprintf("This is a Lorebook JSON (%q, %d entries), not a Character Card — import it in World instead?", who, len(entries)),
				"/world/import/resume", "/characters", token,
			))
			return
		}
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
	isPNG := bytes.HasPrefix(originalRaw, pngSignature)
	if isPNG {
		// The source PNG is itself the card's own art — use it as the
		// character's portrait too. A failure here doesn't undo the
		// import that already succeeded; the character just keeps no
		// picture.
		_ = h.saveImageFromBytes(originalRaw, charID)
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
		lorebookID, err := createLorebookWithEntries(h.DB, uniqueName(bookName, taken), lorebookEntriesFromCharacterBook(*book))
		if err != nil {
			http.Error(w, fmt.Sprintf("imported character %q but its embedded lorebook failed: %v", card.Name, err), http.StatusInternalServerError)
			return
		}
		if isPNG {
			// Same source PNG, same art — the embedded lorebook gets
			// a cover too rather than only the character it came
			// bundled with.
			if imagePath, err := saveImageBytes(originalRaw, "lorebooks", lorebookID); err == nil {
				_ = db.UpdateLorebookImagePath(h.DB, lorebookID, imagePath)
			}
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/characters/%d/edit", charID), http.StatusSeeOther)
}
