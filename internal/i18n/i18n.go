// Package i18n resolves UI strings (templates, nav, About) and the
// system-prompt language instruction (see internal/web/handlers_chat.go's
// languageInstruction) into whichever language the user picked.
//
// Design, per Lord's own ask (task t_424b1a52): language files are plain
// JSON, one file per language, discovered at startup by scanning
// web/static/i18n/*.json — never a list hardcoded in Go. Dropping a new
// file in (e.g. web/static/i18n/de.json) and restarting is the entire
// "add a language" procedure; the switcher in the nav picks it up for
// free via Languages().
package i18n

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type ctxKey struct{}

// Language is one entry in the nav switcher's dropdown.
type Language struct {
	// Code is the filename stem (lowercased) — "en", "fr" — and what
	// gets stored in the "aivalon-lang" cookie and AppSettings.Language.
	Code string
	// Name is how the language names itself (the JSON's own "_name"
	// key, e.g. "Français") — not translated through any other
	// language's file, since a French speaker should see "Français"
	// in the switcher regardless of which language is currently active.
	Name string
}

// Registry is an immutable snapshot of every language LoadDir found.
// Safe for concurrent reads (every HTTP request reads it); nothing
// ever mutates one after LoadDir returns it.
type Registry struct {
	mu    sync.RWMutex
	langs map[string]map[string]string // code -> key -> string
	names map[string]string            // code -> display name
	order []string                     // discovery order (sorted codes)
}

// builtinFallback is the tiny safety net every T() call can lean on
// even when no Registry was ever loaded — direct unit tests across
// this repo call templ.Component.Render(context.Background(), …)
// straight, bypassing main()'s startup entirely (see e.g.
// vault_test.go's TestNavMarksExactlyTheRightSection), and a damaged
// or missing web/static/i18n directory shouldn't blank the whole nav.
// Deliberately NOT the full catalog — that stays in
// web/static/i18n/en.json, the one file an editor actually touches —
// just the handful of keys the shared nav/About chrome needs, so
// there's exactly one place that owns the real English wording in
// the common case (this map only wins when that file is unreadable).
var builtinFallback = map[string]string{
	"nav.chats":           "Chats",
	"nav.characters":      "Characters",
	"nav.worlds":          "Worlds",
	"nav.vault":           "Vault",
	"nav.settings":        "Settings",
	"nav.about":           "About",
	"nav.sidebar_toggle":  "Collapse sidebar",
	"lang.switcher_label": "Change language",
	"about.title":         "About",
	"about.heading":       "About AIvalon",
	"about.version_label": "Version",
	"about.source_label":  "Source",
	"about.license_label": "License",
	"about.authorship_label": "Authorship",
}

// defaultRegistry backs T/Languages/Has when no Registry was ever
// explicitly installed via SetDefault — i.e. in every test in this
// repo. Deliberately non-nil from the start so lookups against it
// never need a nil check.
var defaultRegistry = &Registry{}

// SetDefault installs r as what T/Languages/Has use from now on.
// Called exactly once, by cmd/aivalon/main.go at startup, right
// after LoadDir.
func SetDefault(r *Registry) {
	if r == nil {
		r = &Registry{}
	}
	defaultRegistry = r
}

// Default returns the currently-installed Registry — handlers
// validating a requested language code (the switcher's POST target)
// call this rather than keeping their own reference.
func Default() *Registry { return defaultRegistry }

// LoadDir scans dir for *.json files, one language per file. Each
// file is a flat {"key": "string", ...} map, plus an optional
// "_name" entry (that language's own name for itself) which is
// stripped out of the translation map before storage so it can never
// accidentally render as a UI string. Always returns a usable
// (possibly empty) *Registry, even on error, so callers can log and
// continue on English-only (via builtinFallback) rather than fail to
// start.
func LoadDir(dir string) (*Registry, error) {
	r := &Registry{langs: map[string]map[string]string{}, names: map[string]string{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return r, fmt.Errorf("i18n: reading %s: %w", dir, err)
	}
	var firstErr error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		code := strings.ToLower(strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		var raw map[string]string
		if err := json.Unmarshal(data, &raw); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("i18n: parsing %s: %w", e.Name(), err)
			}
			continue
		}
		name := raw["_name"]
		delete(raw, "_name")
		if name == "" {
			name = strings.ToUpper(code)
		}
		r.langs[code] = raw
		r.names[code] = name
		r.order = append(r.order, code)
	}
	sort.Strings(r.order) // deterministic switcher order, independent of directory listing order
	return r, firstErr
}

// Languages lists every language LoadDir discovered, "en" always
// first when present (Lord's explicit ask — "EN par défaut"), the
// rest alphabetically after it.
func (r *Registry) Languages() []Language {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Language, 0, len(r.order))
	hasEn := false
	for _, code := range r.order {
		if code == "en" {
			hasEn = true
			continue
		}
		out = append(out, Language{Code: code, Name: r.names[code]})
	}
	if hasEn {
		out = append([]Language{{Code: "en", Name: r.names["en"]}}, out...)
	}
	return out
}

// Has reports whether code is a known, selectable language. "en" is
// always considered known (builtinFallback backs it even if
// en.json itself failed to load), so the switcher/cookie/AppSettings
// validation never rejects the one language guaranteed to render
// something sensible.
func (r *Registry) Has(code string) bool {
	if code == "en" {
		return true
	}
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.langs[code]
	return ok
}

func (r *Registry) lookup(lang, key string) (string, bool) {
	if r == nil {
		return "", false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.langs[lang]
	if !ok {
		return "", false
	}
	v, ok := m[key]
	return v, ok
}

// WithLang stores the request's resolved UI language on its context
// — set once per request by the language-resolving middleware in
// cmd/aivalon/main.go, then read by every T() call down the render
// tree, since templ threads that same ctx through every nested
// component's Render call automatically (see internal/web's `render`
// helper and docs/DESIGN.md's i18n section).
func WithLang(ctx context.Context, code string) context.Context {
	return context.WithValue(ctx, ctxKey{}, code)
}

// FromContext returns the request's resolved language, "en" if none
// was ever set — true for every test in this repo that renders a
// templ.Component with a bare context.Background()/t.Context().
func FromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok && v != "" {
		return v
	}
	return "en"
}

// T resolves key in ctx's language, falling back to English, then to
// builtinFallback, then to the bracketed key itself — a missing
// translation is visibly obvious in the UI instead of rendering
// blank, but T never panics or 500s a page over it.
//
// params are alternating {{placeholder}}/value pairs, the exact same
// substitution convention as AppSettings.SystemPromptTemplate's
// {{persona_name}} etc. (see internal/db/app_settings.go) — one
// placeholder syntax for the whole app, not two.
func T(ctx context.Context, key string, params ...string) string {
	lang := FromContext(ctx)
	s, ok := defaultRegistry.lookup(lang, key)
	if !ok && lang != "en" {
		s, ok = defaultRegistry.lookup("en", key)
	}
	if !ok {
		if v, has := builtinFallback[key]; has {
			s, ok = v, true
		}
	}
	if !ok {
		s = "[" + key + "]"
	}
	for i := 0; i+1 < len(params); i += 2 {
		s = strings.ReplaceAll(s, "{{"+params[i]+"}}", params[i+1])
	}
	return s
}
