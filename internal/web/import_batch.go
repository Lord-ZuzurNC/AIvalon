package web

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
	"github.com/Lord-ZuzurNC/AIvalon/internal/i18n"
)

// maxBatchFiles caps how many files ImportModal's multi-select file
// input can submit in one request — generous for a human picking
// cards/lorebooks by hand in a file dialog, small enough that the
// MaxBytesReader ceiling below (maxUploadSize * maxBatchFiles) can't
// be abused into an effectively unbounded request body.
//
// See docs/DESIGN.md's "Multi-file import" section for the UX
// decision this file implements: one combined confirmation screen
// for every file in the batch that hits a language mismatch, rather
// than N sequential single-file confirmations.
const maxBatchFiles = 25

// batchPendingItem is one file whose language-mismatch gate fired
// during a batch import — stashed under token (same ImportCache as
// the single-file flow, see import_cache.go) until the user confirms
// or cancels the whole batch via ImportBatchLanguageConfirmPage.
type batchPendingItem struct {
	Label        string // the parsed card/lorebook name, or the raw filename if that couldn't be determined
	DetectedName string
	TargetName   string
	Token        string
}

// batchFileError is one file that failed outright during a batch
// import (bad JSON, wrong kind, a DB error) — this never aborts the
// rest of the batch (see docs/DESIGN.md), it's just named in the
// final report so Lord knows which file(s) to fix and re-import.
type batchFileError struct {
	Filename string
	Reason   string
}

// parseMultipartFileHeaders returns every "file" field's
// *multipart.FileHeader from r without reading any of their bytes
// yet. isMultipart=false (not an error) means the request wasn't a
// multipart POST at all — i.e. ImportModal's "From URL" tab, which
// readUploadOrURL already handles on its own and stays single-file
// (fetching N arbitrary URLs server-side in one request has no
// upside Lord asked for, see docs/DESIGN.md).
func parseMultipartFileHeaders(w http.ResponseWriter, r *http.Request) (headers []*multipart.FileHeader, isMultipart bool, err error) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return nil, false, nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize*maxBatchFiles)
	if err := r.ParseMultipartForm(maxUploadSize * maxBatchFiles); err != nil {
		return nil, true, fmt.Errorf("file too large or not a valid upload")
	}
	if r.MultipartForm == nil {
		return nil, true, nil
	}
	return r.MultipartForm.File["file"], true, nil
}

// readFileHeader reads one multipart file field's bytes, capped at
// maxUploadSize same as every other upload in this app — a single
// oversized file in a batch fails only that file (see
// batchFileError), never the whole request.
func readFileHeader(h *multipart.FileHeader) ([]byte, error) {
	f, err := h.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxUploadSize+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxUploadSize {
		return nil, fmt.Errorf("file too large (over %d MiB)", maxUploadSize>>20)
	}
	return raw, nil
}

// importCharacterBatch is ImportCharacterJSON's multi-file branch
// (dispatched when the "From your Computer" tab submitted more than
// one file under the "file" field). Every file goes through the same
// per-file pipeline a single upload would — parse, language-mismatch
// gate, create — except a cross-kind upload (a Lorebook dropped into
// Character's importer) is reported as a per-file error instead of
// ImportCrossConfirmPage's own interactive detour, which has no clean
// way to scale to N files in one screen (documented trade-off, see
// docs/DESIGN.md).
func (h CharacterHandlers) importCharacterBatch(w http.ResponseWriter, r *http.Request, headers []*multipart.FileHeader, vaulted bool) {
	if len(headers) > maxBatchFiles {
		http.Error(w, fmt.Sprintf("too many files in one import (max %d)", maxBatchFiles), http.StatusBadRequest)
		return
	}
	settings, err := db.GetAppSettings(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	targetCode := settings.Language
	if targetCode == "" {
		targetCode = "en"
	}
	targetName := languageDisplayName(targetCode, "")

	var successes []string
	var errs []batchFileError
	var pending []batchPendingItem

	for _, hdr := range headers {
		raw, err := readFileHeader(hdr)
		if err != nil {
			errs = append(errs, batchFileError{hdr.Filename, err.Error()})
			continue
		}
		originalRaw := raw
		parsed, err := extractJSONFromUpload(raw)
		if err != nil {
			errs = append(errs, batchFileError{hdr.Filename, err.Error()})
			continue
		}
		card, book, ok := parseCharacterJSON(parsed)
		if !ok {
			if _, _, lbOK := parseLorebookJSON(parsed); lbOK {
				errs = append(errs, batchFileError{hdr.Filename, i18n.T(r.Context(), "import.batch_wrong_kind_lorebook")})
			} else {
				errs = append(errs, batchFileError{hdr.Filename, i18n.T(r.Context(), "import.batch_unrecognized_character")})
			}
			continue
		}
		if card.Name == "" {
			card.Name = nameFromUpload(hdr.Filename)
		}

		detected, _, _, mismatch, err := languageMismatch(h.DB, cardLanguageText(card))
		if err != nil {
			errs = append(errs, batchFileError{hdr.Filename, err.Error()})
			continue
		}
		if mismatch {
			token, err := h.Cache.Stash(originalRaw, hdr.Filename, vaulted)
			if err != nil {
				errs = append(errs, batchFileError{hdr.Filename, err.Error()})
				continue
			}
			detectedName := languageDisplayName(detected.Code, detected.Name)
			pending = append(pending, batchPendingItem{Label: card.Name, DetectedName: detectedName, TargetName: targetName, Token: token})
			continue
		}

		card.IsVaulted = vaulted
		name, _, err := h.createCharacterFromUpload(card, book, originalRaw, vaulted)
		if err != nil {
			errs = append(errs, batchFileError{hdr.Filename, err.Error()})
			continue
		}
		successes = append(successes, name)
	}

	if len(pending) > 0 {
		render(w, r, ImportBatchLanguageConfirmPage("characters", pending, successes, errs, "/characters/import/confirm-language-batch", vaultHome("characters", vaulted), vaulted))
		return
	}
	render(w, r, ImportBatchResultPage("characters", successes, errs, vaultHome("characters", vaulted)))
}

// createCharacterFromUpload is importCharacterUpload's "actually
// create it" tail, extracted so importCharacterBatch can reuse it
// per file without duplicating the dedupe/image/embedded-lorebook
// logic. Returns the final (deduped) name and new character id.
func (h CharacterHandlers) createCharacterFromUpload(card db.CharacterCard, book *characterBookJSON, originalRaw []byte, vaulted bool) (name string, id int64, err error) {
	taken, err := existingCharacterNames(h.DB)
	if err != nil {
		return "", 0, err
	}
	card.Name = uniqueName(card.Name, taken)
	charID, err := db.CreateCharacterCard(h.DB, card)
	if err != nil {
		return "", 0, err
	}
	isPNG := bytes.HasPrefix(originalRaw, pngSignature)
	if isPNG {
		_ = h.saveImageFromBytes(originalRaw, charID)
	}
	if book != nil && len(book.Entries) > 0 {
		bookName := book.Name
		if bookName == "" {
			bookName = card.Name + " lorebook"
		}
		lbTaken, err := existingLorebookNames(h.DB)
		if err != nil {
			return "", 0, err
		}
		lorebookID, err := createLorebookWithEntries(h.DB, uniqueName(bookName, lbTaken), lorebookEntriesFromCharacterBook(*book), vaulted)
		if err != nil {
			return "", 0, fmt.Errorf("imported character %q but its embedded lorebook failed: %w", card.Name, err)
		}
		if isPNG {
			if imagePath, err := saveImageBytes(originalRaw, "lorebooks", lorebookID); err == nil {
				_ = db.UpdateLorebookImagePath(h.DB, lorebookID, imagePath)
			}
		}
	}
	return card.Name, charID, nil
}

// ConfirmLanguageImportBatch (Character) redeems every token the
// batch confirm page submitted, running each through the same VO+VF
// pipeline a single confirmed import uses (importCharacterDualCore)
// — "Annuler tout" (action != "confirm") skips that loop entirely, so
// none of the pending files get created; the files that imported
// immediately in the first pass (already_done) still show up in the
// final report either way, since they were never blocked on this
// confirmation.
func (h CharacterHandlers) ConfirmLanguageImportBatch(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	successes := append([]string{}, r.Form["already_done"]...)
	var errs []batchFileError
	if r.FormValue("action") == "confirm" {
		for _, token := range r.Form["tokens"] {
			raw, filename, vaulted, ok := h.Cache.Take(token)
			if !ok {
				errs = append(errs, batchFileError{filename, i18n.T(r.Context(), "import.batch_token_expired")})
				continue
			}
			voName, vfName, _, _, _, err := h.importCharacterDualCore(r.Context(), raw, filename, vaulted)
			if err != nil {
				errs = append(errs, batchFileError{filename, err.Error()})
				continue
			}
			successes = append(successes, batchResultName(voName, vfName))
		}
	}
	vaulted := r.FormValue("vault") == "1"
	render(w, r, ImportBatchResultPage("characters", successes, errs, vaultHome("characters", vaulted)))
}

// importLorebookBatch mirrors importCharacterBatch for World's bare
// lorebook importer.
func (h WorldHandlers) importLorebookBatch(w http.ResponseWriter, r *http.Request, headers []*multipart.FileHeader, vaulted bool) {
	if len(headers) > maxBatchFiles {
		http.Error(w, fmt.Sprintf("too many files in one import (max %d)", maxBatchFiles), http.StatusBadRequest)
		return
	}
	settings, err := db.GetAppSettings(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	targetCode := settings.Language
	if targetCode == "" {
		targetCode = "en"
	}
	targetName := languageDisplayName(targetCode, "")

	var successes []string
	var errs []batchFileError
	var pending []batchPendingItem

	for _, hdr := range headers {
		raw, err := readFileHeader(hdr)
		if err != nil {
			errs = append(errs, batchFileError{hdr.Filename, err.Error()})
			continue
		}
		originalRaw := raw
		parsed, err := extractJSONFromUpload(raw)
		if err != nil {
			errs = append(errs, batchFileError{hdr.Filename, err.Error()})
			continue
		}
		name, entries, ok := parseLorebookJSON(parsed)
		if !ok {
			if _, _, charOK := parseCharacterJSON(parsed); charOK {
				errs = append(errs, batchFileError{hdr.Filename, i18n.T(r.Context(), "import.batch_wrong_kind_character")})
			} else {
				errs = append(errs, batchFileError{hdr.Filename, i18n.T(r.Context(), "import.batch_unrecognized_lorebook")})
			}
			continue
		}
		if name == "" {
			name = nameFromUpload(hdr.Filename)
		}

		detected, _, _, mismatch, err := languageMismatch(h.DB, lorebookLanguageText(entries))
		if err != nil {
			errs = append(errs, batchFileError{hdr.Filename, err.Error()})
			continue
		}
		if mismatch {
			token, err := h.Cache.Stash(originalRaw, hdr.Filename, vaulted)
			if err != nil {
				errs = append(errs, batchFileError{hdr.Filename, err.Error()})
				continue
			}
			detectedName := languageDisplayName(detected.Code, detected.Name)
			pending = append(pending, batchPendingItem{Label: name, DetectedName: detectedName, TargetName: targetName, Token: token})
			continue
		}

		finalName, _, err := h.createLorebookFromUpload(name, entries, originalRaw, vaulted)
		if err != nil {
			errs = append(errs, batchFileError{hdr.Filename, err.Error()})
			continue
		}
		successes = append(successes, finalName)
	}

	if len(pending) > 0 {
		render(w, r, ImportBatchLanguageConfirmPage("worlds", pending, successes, errs, "/world/import/confirm-language-batch", vaultHome("worlds", vaulted), vaulted))
		return
	}
	render(w, r, ImportBatchResultPage("worlds", successes, errs, vaultHome("worlds", vaulted)))
}

// createLorebookFromUpload is importLorebookUpload's "actually create
// it" tail, extracted for importLorebookBatch's reuse — same reason
// as createCharacterFromUpload above.
func (h WorldHandlers) createLorebookFromUpload(name string, entries []db.LorebookEntry, originalRaw []byte, vaulted bool) (finalName string, id int64, err error) {
	taken, err := existingLorebookNames(h.DB)
	if err != nil {
		return "", 0, err
	}
	finalName = uniqueName(name, taken)
	id, err = createLorebookWithEntries(h.DB, finalName, entries, vaulted)
	if err != nil {
		return "", 0, err
	}
	if bytes.HasPrefix(originalRaw, pngSignature) {
		_ = h.saveImageFromBytes(originalRaw, id)
	}
	return finalName, id, nil
}

// ConfirmLanguageImportBatch (World) mirrors CharacterHandlers' own.
func (h WorldHandlers) ConfirmLanguageImportBatch(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	successes := append([]string{}, r.Form["already_done"]...)
	var errs []batchFileError
	if r.FormValue("action") == "confirm" {
		for _, token := range r.Form["tokens"] {
			raw, filename, vaulted, ok := h.Cache.Take(token)
			if !ok {
				errs = append(errs, batchFileError{filename, i18n.T(r.Context(), "import.batch_token_expired")})
				continue
			}
			voName, vfName, _, _, _, err := h.importLorebookDualCore(r.Context(), raw, filename, vaulted)
			if err != nil {
				errs = append(errs, batchFileError{filename, err.Error()})
				continue
			}
			successes = append(successes, batchResultName(voName, vfName))
		}
	}
	vaulted := r.FormValue("vault") == "1"
	render(w, r, ImportBatchResultPage("worlds", successes, errs, vaultHome("worlds", vaulted)))
}

// batchResultName folds a confirmed batch item's VO (+ optional VF)
// names into the one line the final report shows for it — the batch
// report doesn't carry ImportLanguageResultPage's full voOnly/warning
// detail per item (documented simplification, see docs/DESIGN.md);
// it just names what landed.
func batchResultName(voName, vfName string) string {
	if vfName == "" {
		return voName
	}
	return voName + " / " + vfName
}
