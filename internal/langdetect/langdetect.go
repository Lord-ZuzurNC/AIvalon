// Package langdetect wraps github.com/abadojack/whatlanggo (pure Go,
// MIT-licensed, zero cgo — see docs/DESIGN.md's "Import language
// detection" section for why this library and these thresholds) to
// answer one narrow question for handlers_import.go: "is this
// imported card/lorebook's text confidently in a language other than
// whatever we're told to compare it against?" Detect returns ok=false
// — not a low-confidence guess — whenever the text is too short or
// too ambiguous to trust, so callers never have to re-derive that
// judgment themselves.
package langdetect

import (
	"strings"

	"github.com/abadojack/whatlanggo"
)

// minTextLength and minConfidence were picked empirically (see
// docs/DESIGN.md), not pulled from whatlanggo's own
// ReliableConfidenceThreshold (0.8): that constant is tuned for
// paragraph-length prose, and a card's four fields concatenated often
// don't reach it even when genuinely unambiguous. A clean ~40-char
// English sentence measured during development still scored as low
// as 0.19 confidence under a WRONG guess (Afrikaans) — so both a
// length floor and a confidence floor are needed together; neither
// alone is enough to keep a false positive from triggering an
// unwanted double import.
const (
	minTextLength = 40
	minConfidence = 0.5
)

// Result is a language whatlanggo detected with enough confidence to
// act on.
type Result struct {
	// Code is the ISO 639-1 two-letter code (lowercase, e.g. "fr") —
	// the same convention AppSettings.Language and internal/i18n's
	// Language.Code use, so callers can compare directly. Detect
	// never returns a Result at all (ok=false instead) when
	// whatlanggo's match has no ISO 639-1 code (a handful of less
	// common languages only carry a 639-3 code) — there would be
	// nothing to compare against a 2-letter target anyway.
	Code string
	// Name is whatlanggo's own English name for the language (e.g.
	// "French", "Chinese") — a fallback display name only. Callers
	// that can resolve Code against internal/i18n's own registry
	// (i.e. it matches one of the UI languages actually shipped with
	// this install) should prefer that registry's Language.Name
	// instead, so a French speaker still sees "Français" and not
	// "French". Name exists for every other detected language this
	// install has no UI translation for at all.
	Name string
}

// Detect reports the dominant language of text, or ok=false if the
// text is too short or whatlanggo's own confidence is below
// minConfidence — in either case, callers must treat this exactly
// like "nothing to flag", never as "detected as the target language".
func Detect(text string) (result Result, ok bool) {
	text = strings.TrimSpace(text)
	if len([]rune(text)) < minTextLength {
		return Result{}, false
	}
	info := whatlanggo.Detect(text)
	if info.Confidence < minConfidence {
		return Result{}, false
	}
	code := info.Lang.Iso6391()
	if code == "" {
		return Result{}, false
	}
	return Result{Code: code, Name: whatlanggo.LangToString(info.Lang)}, true
}
