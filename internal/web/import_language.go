package web

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
	"github.com/Lord-ZuzurNC/AIvalon/internal/i18n"
	"github.com/Lord-ZuzurNC/AIvalon/internal/langdetect"
	"github.com/Lord-ZuzurNC/AIvalon/internal/llmclient"
)

// translateFieldTimeout bounds a single field/entry's translation
// call to the local LLM backend — generous for a local model under
// load, short enough that one stuck field doesn't hang the whole
// import request indefinitely. Each field/entry gets its own fresh
// timeout (not one shared budget for the whole card/lorebook), since
// a lorebook's entry count is unbounded and translating them is
// inherently sequential (see translateLorebookEntries) — one call at
// a time, no batching, documented trade-off in docs/DESIGN.md.
const translateFieldTimeout = 45 * time.Second

// languageDisplayName prefers the installed i18n registry's own
// self-name for code (Language.Name — the exact string the nav
// switcher already shows, e.g. "Français" not "French") when code
// matches one of the UI languages actually shipped with this
// install. fallback (whatlanggo's own English name, via
// langdetect.Result.Name) covers every other detected language this
// install has no UI translation file for at all (e.g. Chinese with
// no zh.json) — the point being AIvalon never maintains its own
// hardcoded code→name table either way, per Lord's own ask.
func languageDisplayName(code, fallback string) string {
	for _, l := range i18n.Default().Languages() {
		if l.Code == code {
			return l.Name
		}
	}
	if fallback != "" {
		return fallback
	}
	return strings.ToUpper(code)
}

// vfSuffix is the name tag the translated copy gets: "VF" for French
// (Lord's own literal ask), the uppercased language code otherwise
// (e.g. "DE") — never a hardcoded per-language word list beyond this
// one French-specific exception, which Lord's own wording called out
// by name.
func vfSuffix(targetCode string) string {
	if targetCode == "fr" {
		return "VF"
	}
	return strings.ToUpper(targetCode)
}

// cardLanguageText concatenates exactly the four CharacterCard fields
// Lord named for detection — description, personality, scenario,
// first_message. ExampleMessages and AuthorNote are deliberately
// excluded: Lord's ask listed these four by name, and author's notes
// in particular are often meta-commentary (recommended model, etc.)
// that can read as a different language than the card's own content
// without that being a real VO/VF case.
func cardLanguageText(c db.CharacterCard) string {
	return strings.Join([]string{c.Description, c.Personality, c.Scenario, c.FirstMessage}, "\n")
}

// lorebookLanguageText concatenates every entry's Content (never
// Keys — trigger keys are matched verbatim against the user's own
// messages, see db.LorebookEntry's doc comment, so they carry no
// useful signal about the book's prose language and must never be
// fed to translation either, per Lord's explicit call).
func lorebookLanguageText(entries []db.LorebookEntry) string {
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, e.Content)
	}
	return strings.Join(parts, "\n")
}

// languageMismatch is the one decision point both importers gate on:
// does text's own detected language (see internal/langdetect, which
// already refuses to guess on short/ambiguous input) differ from
// AppSettings.Language? ok=false means there is nothing to flag —
// either detection didn't clear its confidence bar, or it matched the
// target already — and mismatch handling must be skipped entirely.
func languageMismatch(sqlDB *sql.DB, text string) (detected langdetect.Result, targetCode, targetName string, mismatch bool, err error) {
	settings, err := db.GetAppSettings(sqlDB)
	if err != nil {
		return langdetect.Result{}, "", "", false, err
	}
	targetCode = settings.Language
	if targetCode == "" {
		targetCode = "en"
	}
	targetName = languageDisplayName(targetCode, "")
	res, ok := langdetect.Detect(text)
	if !ok || res.Code == targetCode {
		return res, targetCode, targetName, false, nil
	}
	return res, targetCode, targetName, true, nil
}

// pickTranslationBackend picks the backend profile the VF copy's
// translation calls go through: the first kind="llm" profile found
// (db.ListBackendProfiles already orders by name) — AppSettings has
// no separate "default backend" concept to prefer instead, so "the
// first configured chat-capable backend" is the simplest rule that
// needs no new setting of its own. hasProfile=false (not an error)
// means none exists yet — the caller's own fallback path, never a
// crash.
func pickTranslationBackend(sqlDB *sql.DB) (profile db.BackendProfile, hasProfile bool, err error) {
	profiles, err := db.ListBackendProfiles(sqlDB)
	if err != nil {
		return db.BackendProfile{}, false, err
	}
	for _, p := range profiles {
		if p.Kind == "llm" {
			return p, true, nil
		}
	}
	return db.BackendProfile{}, false, nil
}

// translateText sends text to profile with an instruction to
// translate it into targetLangName, via the same llmclient.StreamChat
// the chat feature already uses — no third-party translation API,
// consistent with AIvalon's local-first principle. Empty input is
// returned as-is without a wasted round trip.
func translateText(ctx context.Context, profile db.BackendProfile, text, targetLangName string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	sys := fmt.Sprintf(
		"You are a translation engine embedded in a roleplay app. Translate the user's message into %s. "+
			"Preserve the original formatting exactly: *asterisks* for actions, \"quotes\" for spoken dialogue, "+
			"line breaks, and any {{placeholder}} tokens left untranslated. "+
			"Output ONLY the translated text — no preamble, no explanation, no quotation marks wrapped around it.",
		targetLangName,
	)
	var buf strings.Builder
	err := llmclient.StreamChat(ctx, profile, []llmclient.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: text},
	}, func(tok string) { buf.WriteString(tok) })
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(buf.String())
	if out == "" {
		return "", fmt.Errorf("translation returned empty output")
	}
	return out, nil
}

// translateCardFields attempts to translate exactly the fields
// cardLanguageText looked at. All-or-nothing: if any single field's
// call fails, the whole attempt is abandoned (ok=false) rather than
// landing a partially-translated VF copy — the caller then falls back
// to VO-only, same as "no backend configured" (see
// import.translation_unavailable_failed).
func translateCardFields(ctx context.Context, profile db.BackendProfile, c db.CharacterCard, targetLangName string) (db.CharacterCard, bool) {
	out := c
	for _, f := range []*string{&out.Description, &out.Personality, &out.Scenario, &out.FirstMessage} {
		if *f == "" {
			continue
		}
		fieldCtx, cancel := context.WithTimeout(ctx, translateFieldTimeout)
		translated, err := translateText(fieldCtx, profile, *f, targetLangName)
		cancel()
		if err != nil {
			return c, false
		}
		*f = translated
	}
	return out, true
}

// translateLorebookEntries mirrors translateCardFields for a
// lorebook's entries: only Content is translated, Keys are copied
// through byte-for-byte (see lorebookLanguageText's own doc comment
// for why). All-or-nothing across every entry, same reasoning as
// translateCardFields.
func translateLorebookEntries(ctx context.Context, profile db.BackendProfile, entries []db.LorebookEntry, targetLangName string) ([]db.LorebookEntry, bool) {
	out := make([]db.LorebookEntry, len(entries))
	copy(out, entries)
	for i := range out {
		if out[i].Content == "" {
			continue
		}
		entryCtx, cancel := context.WithTimeout(ctx, translateFieldTimeout)
		translated, err := translateText(entryCtx, profile, out[i].Content, targetLangName)
		cancel()
		if err != nil {
			return entries, false
		}
		out[i].Content = translated
	}
	return out, true
}

// ConfirmLanguageImport (Character) is ImportLanguageConfirmPage's
// Confirm button target — redeems the token importCharacterUpload
// stashed (same ImportCache as the cross-confirm flow) and performs
// the actual VO+VF double import.
func (h CharacterHandlers) ConfirmLanguageImport(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	raw, filename, vaulted, ok := h.Cache.Take(r.FormValue("token"))
	if !ok {
		http.Error(w, "this import link expired — upload the file again", http.StatusBadRequest)
		return
	}
	h.importCharacterDual(w, r, raw, filename, vaulted)
}

// importCharacterDual re-parses raw (the user already confirmed the
// language-mismatch warning) and creates two character cards — the
// untouched original suffixed " (VO)", and a translated copy suffixed
// " (VF)"/" (<CODE>)" (see vfSuffix) — plus, for each, their own copy
// of any embedded lorebook. Falls back to VO-only (never a crash, see
// docs/DESIGN.md) when no kind="llm" backend is configured or the
// translation attempt fails; ImportLanguageResultPage always reports
// which happened instead of a bare redirect, since the fallback case
// has no other way to tell the user the VF copy wasn't made.
func (h CharacterHandlers) importCharacterDual(w http.ResponseWriter, r *http.Request, raw []byte, filename string, vaulted bool) {
	voName, vfName, warning, voOnly, voID, err := h.importCharacterDualCore(r.Context(), raw, filename, vaulted)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, ImportLanguageResultPage("characters", voName, vfName, warning, voOnly, fmt.Sprintf("/characters/%d/edit", voID)))
}

// importCharacterDualCore is importCharacterDual's actual logic,
// pulled out so the batch importer (see import_batch.go) can run the
// exact same VO+VF pipeline per confirmed file without an
// http.ResponseWriter to render into — it returns everything the
// caller needs (names, warning, voOnly, the VO's id) and a plain
// error instead of writing one. The single-file path above is the
// only caller that still renders ImportLanguageResultPage directly;
// the batch path (ConfirmLanguageImportBatch) folds the same return
// values into one combined report instead.
func (h CharacterHandlers) importCharacterDualCore(ctx context.Context, raw []byte, filename string, vaulted bool) (voName, vfName, warning string, voOnly bool, voID int64, err error) {
	originalRaw := raw
	raw, err = extractJSONFromUpload(raw)
	if err != nil {
		return "", "", "", false, 0, err
	}
	card, book, ok := parseCharacterJSON(raw)
	if !ok {
		return "", "", "", false, 0, fmt.Errorf("unrecognized character JSON — expected a Character Card V2/V3 file")
	}
	if card.Name == "" {
		card.Name = nameFromUpload(filename)
	}

	settings, err := db.GetAppSettings(h.DB)
	if err != nil {
		return "", "", "", false, 0, err
	}
	targetCode := settings.Language
	if targetCode == "" {
		targetCode = "en"
	}
	targetName := languageDisplayName(targetCode, "")

	taken, err := existingCharacterNames(h.DB)
	if err != nil {
		return "", "", "", false, 0, err
	}
	isPNG := bytes.HasPrefix(originalRaw, pngSignature)
	voCard := card
	voCard.Name = uniqueName(card.Name+" (VO)", taken)
	taken[voCard.Name] = true
	voCard.IsVaulted = vaulted
	voID, err = db.CreateCharacterCard(h.DB, voCard)
	if err != nil {
		return "", "", "", false, 0, err
	}
	if isPNG {
		_ = h.saveImageFromBytes(originalRaw, voID)
	}
	if book != nil && len(book.Entries) > 0 {
		if err := h.createEmbeddedBookCopy(originalRaw, book, card.Name, "VO", vaulted, isPNG); err != nil {
			return "", "", "", false, 0, fmt.Errorf("imported %q (VO) but its embedded lorebook failed: %w", voCard.Name, err)
		}
	}

	// --- VF: best-effort translated copy ---
	profile, hasProfile, err := pickTranslationBackend(h.DB)
	if err != nil {
		return "", "", "", false, 0, err
	}
	if !hasProfile {
		return voCard.Name, "", i18n.T(ctx, "import.translation_unavailable_no_backend"), true, voID, nil
	}
	translated, tOK := translateCardFields(ctx, profile, card, targetName)
	if !tOK {
		return voCard.Name, "", i18n.T(ctx, "import.translation_unavailable_failed"), true, voID, nil
	}
	translated.Name = uniqueName(card.Name+" ("+vfSuffix(targetCode)+")", taken)
	taken[translated.Name] = true
	translated.IsVaulted = vaulted
	vfID, err := db.CreateCharacterCard(h.DB, translated)
	if err != nil {
		return "", "", "", false, 0, err
	}
	vfName = translated.Name
	if isPNG {
		_ = h.saveImageFromBytes(originalRaw, vfID)
	}
	if book != nil && len(book.Entries) > 0 {
		srcEntries := lorebookEntriesFromCharacterBook(*book)
		translatedEntries, eOK := translateLorebookEntries(ctx, profile, srcEntries, targetName)
		if !eOK {
			// The character itself translated fine; only its book
			// didn't — still a degraded-but-real VF character, worth
			// keeping rather than discarding.
			warning = i18n.T(ctx, "import.translation_unavailable_failed")
		} else if err := h.createTranslatedBookCopy(originalRaw, book.Name, card.Name, translatedEntries, targetCode, vaulted, isPNG); err != nil {
			return "", "", "", false, 0, fmt.Errorf("imported %q's translation but its lorebook failed: %w", translated.Name, err)
		}
	}

	return voCard.Name, vfName, warning, false, voID, nil
}

// createEmbeddedBookCopy creates one lorebook copy (VO or VF, per
// suffix) from book's own untranslated entries — the VO half of a
// character's embedded book; createTranslatedBookCopy is its VF
// counterpart since the entries there are a distinct, already-
// translated slice rather than a fresh re-derivation from book.
func (h CharacterHandlers) createEmbeddedBookCopy(originalRaw []byte, book *characterBookJSON, cardName, suffix string, vaulted, isPNG bool) error {
	bookName := book.Name
	if bookName == "" {
		bookName = cardName + " lorebook"
	}
	taken, err := existingLorebookNames(h.DB)
	if err != nil {
		return err
	}
	name := uniqueName(bookName+" ("+suffix+")", taken)
	id, err := createLorebookWithEntries(h.DB, name, lorebookEntriesFromCharacterBook(*book), vaulted)
	if err != nil {
		return err
	}
	if isPNG {
		if imagePath, err := saveImageBytes(originalRaw, "lorebooks", id); err == nil {
			_ = db.UpdateLorebookImagePath(h.DB, id, imagePath)
		}
	}
	return nil
}

// createTranslatedBookCopy is createEmbeddedBookCopy's VF half: same
// shape, but entries already carry translated Content (Keys
// untouched) rather than being re-derived from the raw JSON.
func (h CharacterHandlers) createTranslatedBookCopy(originalRaw []byte, bookName, cardName string, entries []db.LorebookEntry, targetCode string, vaulted, isPNG bool) error {
	if bookName == "" {
		bookName = cardName + " lorebook"
	}
	taken, err := existingLorebookNames(h.DB)
	if err != nil {
		return err
	}
	name := uniqueName(bookName+" ("+vfSuffix(targetCode)+")", taken)
	id, err := createLorebookWithEntries(h.DB, name, entries, vaulted)
	if err != nil {
		return err
	}
	if isPNG {
		if imagePath, err := saveImageBytes(originalRaw, "lorebooks", id); err == nil {
			_ = db.UpdateLorebookImagePath(h.DB, id, imagePath)
		}
	}
	return nil
}

// ConfirmLanguageImport (World) mirrors CharacterHandlers' own —
// ImportLanguageConfirmPage's Confirm button target for a bare
// lorebook import.
func (h WorldHandlers) ConfirmLanguageImport(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	raw, filename, vaulted, ok := h.Cache.Take(r.FormValue("token"))
	if !ok {
		http.Error(w, "this import link expired — upload the file again", http.StatusBadRequest)
		return
	}
	h.importLorebookDual(w, r, raw, filename, vaulted)
}

// importLorebookDual mirrors importCharacterDual for a bare lorebook
// import: VO (untouched, " (VO)") + best-effort VF (translated
// Content only, Keys untouched, " (VF)"/" (<CODE>)"), same fallback
// contract on no backend/failed translation.
func (h WorldHandlers) importLorebookDual(w http.ResponseWriter, r *http.Request, raw []byte, filename string, vaulted bool) {
	voName, vfName, warning, voOnly, voID, err := h.importLorebookDualCore(r.Context(), raw, filename, vaulted)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, ImportLanguageResultPage("worlds", voName, vfName, warning, voOnly, fmt.Sprintf("/world/%d", voID)))
}

// importLorebookDualCore is importLorebookDual's logic without an
// http.ResponseWriter — see importCharacterDualCore's doc comment for
// why (the batch importer, import_batch.go, is the other caller).
func (h WorldHandlers) importLorebookDualCore(ctx context.Context, raw []byte, filename string, vaulted bool) (voName, vfName, warning string, voOnly bool, voID int64, err error) {
	originalRaw := raw
	raw, err = extractJSONFromUpload(raw)
	if err != nil {
		return "", "", "", false, 0, err
	}
	name, entries, ok := parseLorebookJSON(raw)
	if !ok {
		return "", "", "", false, 0, fmt.Errorf("unrecognized lorebook JSON — expected a SillyTavern World Info export, or a Character Card V2/V3 file with an embedded character_book")
	}
	if name == "" {
		name = nameFromUpload(filename)
	}

	settings, err := db.GetAppSettings(h.DB)
	if err != nil {
		return "", "", "", false, 0, err
	}
	targetCode := settings.Language
	if targetCode == "" {
		targetCode = "en"
	}
	targetName := languageDisplayName(targetCode, "")

	taken, err := existingLorebookNames(h.DB)
	if err != nil {
		return "", "", "", false, 0, err
	}
	isPNG := bytes.HasPrefix(originalRaw, pngSignature)

	voName = uniqueName(name+" (VO)", taken)
	taken[voName] = true
	voID, err = createLorebookWithEntries(h.DB, voName, entries, vaulted)
	if err != nil {
		return "", "", "", false, 0, err
	}
	if isPNG {
		_ = h.saveImageFromBytes(originalRaw, voID)
	}

	profile, hasProfile, err := pickTranslationBackend(h.DB)
	if err != nil {
		return "", "", "", false, 0, err
	}
	if !hasProfile {
		return voName, "", i18n.T(ctx, "import.translation_unavailable_no_backend"), true, voID, nil
	}
	translatedEntries, eOK := translateLorebookEntries(ctx, profile, entries, targetName)
	if !eOK {
		return voName, "", i18n.T(ctx, "import.translation_unavailable_failed"), true, voID, nil
	}
	vfName = uniqueName(name+" ("+vfSuffix(targetCode)+")", taken)
	vfID, err := createLorebookWithEntries(h.DB, vfName, translatedEntries, vaulted)
	if err != nil {
		return "", "", "", false, 0, err
	}
	if isPNG {
		_ = h.saveImageFromBytes(originalRaw, vfID)
	}
	return voName, vfName, "", false, voID, nil
}
