# AIvalon — Design

AIvalon is a local, single-user web app for AI roleplay.
It replaces OOC: instead of a rate-limited hosted service, it talks to a locally-running LLM and keeps everything — characters, world lore, chat history — on the user's own machine.
No account system, no multi-tenancy, no network exposure by default.

## Scope

**v1** ships text-only roleplay: chat, character cards, lorebooks, and the settings needed to point AIvalon at a local model.
**v2** adds on-demand image generation inside a chat session. The code is in (backend profile, presets, generation endpoint) but unvalidated — no real pass against a live A1111 instance yet — so treat it as work in progress, not a shipped feature.
Nothing in v1's data model should need to change shape to support v2 — see **Backend profiles** below.

## Tech stack

- **Language: Go** (latest stable).
  Chosen for single-binary distribution (no runtime to install, no venv to explain to a GitHub user who just wants to try it) and for a language simple enough to iterate on quickly, which this project will need: it is still settling its own design.
  AIvalon never links against an inference engine; it only ever speaks HTTP to one, so none of Rust's FFI/memory-safety strengths would have paid for themselves here.
- **Web layer: standard library `net/http`**, using the Go 1.22+ routing enhancements (method + wildcard patterns).
  No external router until a real need for one shows up.
- **Interactivity: htmx**, including its SSE extension for streaming chat tokens.
  No separate JavaScript build toolchain, no Node/npm dependency: `go build` alone produces the whole app.
- **Storage: SQLite via `modernc.org/sqlite`** (pure Go, no cgo).
  This is the detail that actually protects the single-binary promise: the more common driver, `mattn/go-sqlite3`, uses cgo and would quietly break static cross-compilation.
  Postgres, MariaDB, and MongoDB were considered and set aside: all three mean a second process to install, configure, and keep running, which contradicts the single-binary, zero-ops pitch for a single-user local app with a small, mostly-relational schema.
  If AIvalon ever grows a real shared/multi-user mode, that is the point to revisit this, with Postgres as the natural upgrade — not before.
  **Migrations: `goose`**, plain `.sql` files.
- **Templates: `templ`**, generated `_templ.go` files committed to the repo.
  Keeping them out of `.gitignore` is deliberate: it is what lets `go build` alone work from a bare clone, without requiring a user to install the `templ` CLI first.
  Compiled, type-checked HTML components — catches a broken template at build time instead of at render time, in the same spirit as the language choice.
- **Streaming transport: Server-Sent Events.**
  One-directional, token-by-token, matches how Ollama/llama.cpp/OpenAI-compatible APIs already stream completions — AIvalon just relays the stream instead of re-framing it.
- **Default bind: `127.0.0.1` only.**
  Nothing about this app needs to be reachable from the network by default; a user who wants that sets `AIVALON_ADDR=0.0.0.0:PORT`.
  Doing so also relaxes `requireLocalHost` (DNS-rebinding guard, `cmd/aivalon/main.go`) to accept a Host header that's a bare IP literal in a private LAN range (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`) on the same port — a domain name never matches there, so rebinding a public name to a private address is still rejected. Lord's own call, for reaching the app from his phone on the same home WiFi; see the "Open questions" TLS entry below for what this does and doesn't cover.

## Backend profiles

AIvalon never embeds inference.
It is a client to whatever exposes an OpenAI-compatible `chat/completions` endpoint: Ollama, llama.cpp's server, the OpenAI API itself, or any other engine that speaks the same shape.

A **backend profile** holds: a name, a kind (`llm` in v1; `image` added in v2), a base URL, an optional API key, and the model parameters exposed to the user (temperature, top_p, top_k, max tokens, and so on).
Multiple profiles can exist side by side — a user can point one profile at Ollama and another at a hosted API without AIvalon caring which is which beyond the `kind`.
Designing the `kind` field in now, even though only `llm` is used in v1, is what lets v2's image generation slot in as a new backend profile instead of a schema migration.
The v2 `image` kind targets the **AUTOMATIC1111 (A1111) Stable Diffusion WebUI API** (`/sdapi/v1/txt2img` and friends) — picked for being the simplest of the common options. The client code is written but unvalidated against a real A1111 instance; see **Scope** above.

## Stopping a generation in progress

The browser closes the stream (`EventSource.close()`, since htmx's SSE extension uses `EventSource`, which has no `AbortController` support — `AbortController` pairs with `fetch()`, not with `EventSource`).
Closing the connection is only half the job: the Go handler must notice via `r.Context().Done()` and cancel, through that same context, its own outbound request to the LLM backend.
Without that second step the browser stops receiving tokens but Ollama/llama.cpp keeps generating them for nobody — wasted GPU time on the one GPU this app has to share.

## Data model

Vocabulary follows the Character Card V2/V3 spec exactly, on purpose: **Character** is the AI-played card, **Persona** is the human player's own self-profile.
An earlier draft of this document used the reversed, project-local terms `PersonaCard`/`UserPersona` — that naming is gone, so a card or a doc from the wider ecosystem now reads the same way here.

- **CharacterCard** — name, description, personality, scenario, first message, example messages, avatar, tags, spec version (`v2`/`v3`).
  Also keeps the full original imported JSON (see schema draft below) so fields AIvalon doesn't surface in the UI yet still round-trip on export.
- **Persona** — the human player's own self-profile: name, description, avatar.
  Used wherever a prompt needs to refer to the user.
  Optional: a session with no `Persona` selected falls back to a built-in default (name "User", empty description) rather than failing or refusing to load.
- **Lorebook** (World Info) — name, entries (keys, content, trigger strategy, insertion order, probability).
  A separate object from CharacterCard, attachable to a session — this is the split that was missing from the first pass at the idea, where "a card" was expected to carry an entire world by itself.
- **Session** — a `Persona`, one or more `CharacterCard`s (group chats are multiple AI characters in one session), at most one `Lorebook`, the message log, and a snapshot of which backend profile and model parameters were used.
- **Message** — role, content, timestamp, and for role `image` an `image_path` to a generated picture.
  Image messages are their own role (not `system`) so they're trivially kept out of the LLM prompt history and get their own rendering; they sort like any other message, so the next turn always lands below the image.
- **ImagePreset** — a named txt2img parameter set (steps, cfg_scale, sampler, size, seed, negative prompt) plus a style suffix appended to the prompt (`image_presets`, migration 00012; three built-ins seeded: Realistic, Manga / Anime, Cyberpunk).
  Created, edited and deleted only in Settings → Image presets. Each session picks at most one (`sessions.image_preset_id`, migration 00013, nullable, `ON DELETE SET NULL`) from its session panel's Gallery tab; the "Generate image" dialog asks only for the prompt (and backend, if there are several) and resolves every other parameter server-side from that preset, or from A1111's defaults when none is chosen or the preset is gone.
- **BackendProfile** — see above.

## Character card compatibility

v1 must import and export both serializations in circulation for Character Card V2/V3:

- plain JSON,
- PNG with the JSON embedded in a `tEXt` chunk (`chara` keyword, base64; v3 adds a `ccv3` chunk).

This is what lets a card found online, or one built by hand elsewhere, actually open in AIvalon.

## Database schema (draft)

SQLite, managed by `goose`.
Table and column names are a draft — fine to adjust once real code is being written — but the shape (what references what, what's nullable, what's JSON) is meant to hold.

```sql
-- +goose Up

CREATE TABLE personas (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    avatar_path TEXT,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- No row is required: a session with persona_id NULL uses the built-in default persona in code.

CREATE TABLE character_cards (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    name              TEXT NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    personality       TEXT NOT NULL DEFAULT '',
    scenario          TEXT NOT NULL DEFAULT '',
    first_message     TEXT NOT NULL DEFAULT '',
    example_messages  TEXT NOT NULL DEFAULT '',
    avatar_path       TEXT,
    tags              TEXT NOT NULL DEFAULT '[]',  -- JSON array of strings
    spec_version      TEXT NOT NULL DEFAULT 'v2',  -- 'v2' | 'v3'
    raw_json          TEXT NOT NULL,               -- full imported card, for lossless export
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE lorebooks (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE lorebook_entries (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    lorebook_id       INTEGER NOT NULL REFERENCES lorebooks(id) ON DELETE CASCADE,
    keys              TEXT NOT NULL DEFAULT '[]',  -- JSON array of strings
    content           TEXT NOT NULL DEFAULT '',
    trigger_strategy  TEXT NOT NULL DEFAULT 'selective',  -- 'constant' | 'selective'
    insertion_order   INTEGER NOT NULL DEFAULT 0,
    probability       INTEGER NOT NULL DEFAULT 100,  -- 0-100
    enabled           INTEGER NOT NULL DEFAULT 1     -- boolean
);
CREATE INDEX idx_lorebook_entries_lorebook ON lorebook_entries(lorebook_id);

CREATE TABLE backend_profiles (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL,
    kind          TEXT NOT NULL,              -- 'llm' | 'image'
    base_url      TEXT NOT NULL,
    model         TEXT NOT NULL DEFAULT '',  -- model name/path sent to the backend; some backends (Ollama) require an exact match, others (llama.cpp, single model loaded) ignore it
    api_key       TEXT,                       -- plain text; protected by the 0600 db file perms, see docs/DESIGN.md
    model_params  TEXT NOT NULL DEFAULT '{}', -- JSON: temperature, top_p, top_k, max_tokens, ...
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE sessions (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    title                  TEXT NOT NULL DEFAULT '',
    persona_id             INTEGER REFERENCES personas(id) ON DELETE SET NULL,
    lorebook_id            INTEGER REFERENCES lorebooks(id) ON DELETE SET NULL,
    backend_profile_id     INTEGER NOT NULL REFERENCES backend_profiles(id),
    model_params_snapshot  TEXT NOT NULL DEFAULT '{}',  -- JSON, copied from the profile at session start
    created_at             TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at             TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE session_characters (
    session_id         INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    character_card_id  INTEGER NOT NULL REFERENCES character_cards(id) ON DELETE CASCADE,
    turn_order         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (session_id, character_card_id)
);

CREATE TABLE messages (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id         INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    role               TEXT NOT NULL,  -- 'user' | 'character' | 'narrator' | 'system' | 'image'
    character_card_id  INTEGER REFERENCES character_cards(id),  -- set when role = 'character'
    content            TEXT NOT NULL,
    image_path         TEXT,  -- set only on role = 'image' (a generated picture; content = its prompt + seed)
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX idx_messages_session_created ON messages(session_id, created_at);

-- +goose Down

DROP TABLE messages;
DROP TABLE session_characters;
DROP TABLE sessions;
DROP TABLE backend_profiles;
DROP TABLE lorebook_entries;
DROP TABLE lorebooks;
DROP TABLE character_cards;
DROP TABLE personas;
```

Notes:

- **`character_cards.raw_json`** keeps the entire card as imported.
  The Character Card V2/V3 spec has fields this v1 doesn't surface (alternate greetings, embedded character-specific lorebook, extensions, creator notes, and so on) — without this column, importing a card and exporting it back would silently drop anything not yet modeled.
- **`model_params` and `model_params_snapshot` are JSON blobs, not columns**, because the parameter set differs between an `llm` profile (temperature, top_p, top_k, ...) and an `image` profile (steps, cfg_scale, sampler, ...) added in v2 — a fixed set of columns would need a migration the day v2 lands.
- **`backend_profiles.api_key` is stored as plain text, protected by file permissions.**
  The SQLite file is created `0600` (owner read/write only) — the same trust boundary `~/.netrc` or a cloud CLI's config file already uses.
  No OS keyring integration for v1: on Linux that would depend on a running Secret Service daemon (gnome-keyring/kwallet), which isn't guaranteed to exist, and would turn a reliable single-binary story into a "works if the desktop environment cooperates" one for a problem a file permission already covers at this trust level.

## Visual stack

Four top-level sections, no nested navigation: **Chat** (sessions), **World** (lorebooks), **Character** (cards), **Settings** (backend profiles, model parameters, and the user's own Persona).
Server-rendered pages, htmx for partial updates and streaming — no single-page-app framework, no separate frontend build.
Starting point for styling: a classless base (Pico.css) with a dark theme by default, overridden as needed.
This is the lowest-stakes decision in this document and the cheapest to revisit; nothing above depends on it.

## License

AGPLv3.
Chosen over MIT to close the one gap that matters for a self-hosted chat app: anyone who takes AIvalon, modifies it, and runs it as a network service for other people has to publish those modifications.
Running it locally, alone, carries no obligation beyond respecting the license on redistribution.

## Internationalization (i18n)

Task t_424b1a52, Lord's own ask: AIvalon's friends aren't all English speakers, French first but the mechanism has to generalize to N languages without a Go code change per language.

**File format and discovery.** `web/static/i18n/<code>.json` — one flat `{"key": "string"}` file per language, shipped next to the rest of `web/static` (same disk-read convention as the CSS/JS there, not `go:embed`: dropping a new file in and restarting is the entire "add a language" procedure, nothing to rebuild). `internal/i18n.LoadDir` scans that directory at startup; the switcher's language list (`Registry.Languages()`) comes straight from what it found — there is no hardcoded list of languages anywhere in Go. Each file's own `"_name"` key (e.g. `"Français"`) is how it names itself in the switcher — never translated through another language's file, so a French speaker always sees "Français" regardless of which language is currently active.

**Threading the chosen language through render.** `internal/web`'s `render` helper and every direct `.Render(r.Context(), …)` call already pass `r.Context()` into templ, and templ threads that same `ctx` through every nested component automatically — so a `withLanguage` middleware (`cmd/aivalon/main.go`) resolving the language once per request (cookie `aivalon-lang` → `AppSettings.Language` → `"en"`) and storing it on that context via `i18n.WithLang` is all the plumbing needed. No templ function signature had to grow a `ctx context.Context` parameter or get threaded through every page handler — `templates.templ`'s `i18n.T(ctx, "nav.chats")` calls just read the ambient `ctx` templ's own generated code already provides.

**Fallback chain.** `i18n.T` resolves a key in the request's language, then English, then a tiny Go-level `builtinFallback` map (not the full catalog — just the handful of keys the shared nav/About chrome needs), then the bracketed key itself — so a missing translation is visibly obvious instead of blank, but never panics or 500s. `builtinFallback` exists because this repo's own tests `.Render(context.Background(), …)` templ components directly, bypassing `main()`'s startup (and `internal/web`'s own test suite loads the real `web/static/i18n` via a `TestMain`, for the FR-specific assertions that need a second language) — it's a resilience net against a missing/damaged directory, not a second source of truth for the real English wording, which stays in `web/static/i18n/en.json`.

**System prompt language (part B).** `buildNarratorPrompt` prepends a short "Respond only in {{language}}…" instruction ahead of the *entire* `SystemPromptTemplate` (default or Lord's own hand-edited one) rather than adding a new `{{language}}` placeholder inside the template. A preamble applies uniformly regardless of which template is active, with zero risk of silently doing nothing on an old or custom template that doesn't know about a new placeholder. The instruction names the language by its own `_name` (not a hardcoded per-language sentence), so a language added later needs no Go change here either. `code == "en"` (or `""`) yields an empty preamble — byte-for-byte the same prompt as before this feature, for every install that never touches the switcher.

**Persistence: cookie + `AppSettings.Language`, not just one.** The browser cookie is what every page reads for instant effect; `AppSettings.Language` (migration 00017) is the server-side source of truth the system prompt draws from — it has nothing to do with any one browser (think: the backend LLM call happens server-side regardless of who's looking at the page). The switcher's `POST /settings/language` sets both at once.

**Full reload on switch, by design.** Lord's own ask said "sans reload complet si possible" — but AIvalon's whole architecture already commits to "every link is a plain `<a>`, there's no SPA route" (see `templates.templ`'s own head-script comment), and the language affects text scattered across the *entire* page (nav, headings, forms), not one htmx-swappable fragment. A full `POST` + redirect is the one mechanism already consistent with how every other state change in this app works (plain forms everywhere), so that's what the switcher does; a partial-DOM retranslation would be new, one-off machinery for a single feature. Documented here as the explicit trade-off this decision made.

**Scope actually shipped vs. follow-up.** This task's budget covered: the full i18n engine above, the nav + sidebar toggle + About page fully converted to translated keys (English/French both complete), and the system-prompt preamble. `character.templ`, `chat.templ`, `world.templ`, `vault.templ`, `settings.templ`, and `import.templ` still have their visible text hardcoded in English — the `t()`/key pattern is established and ready to extend, tracked as a follow-up task rather than rushed. Part C of the original ask (character-card/lorebook language detection + VO/original + translated-copy import) shipped separately — see "Import language detection + VO/VF dual import" below.

## Import language detection + VO/VF dual import

Task t_382b94fb, Lord's own ask (quoted in full in that task's body): detect an imported card/lorebook's language, and if it doesn't match `AppSettings.Language`, warn before import and — on confirmation — keep the original (suffixed "VO") *and* create a translated copy (suffixed "VF" for French, the uppercased language code otherwise, e.g. "DE").

**Detection library and thresholds.** `github.com/abadojack/whatlanggo` (MIT, compatible with this repo's AGPLv3) — pure Go, zero cgo, ~84 languages via trigram matching. Wrapped in `internal/langdetect` rather than called directly from `internal/web`, so the confidence/length judgment call lives in one tested place. Two thresholds, picked empirically rather than taken from the library's own `ReliableConfidenceThreshold` (0.8, tuned for paragraph-length prose and too strict for a card's few short fields): a 40-rune floor on the concatenated text, and a 0.5 confidence floor. Both are needed together — during development, a clean, unambiguous 39-character English sentence still scored as low as 0.19 confidence under a *wrong* guess (Afrikaans); a length floor alone doesn't catch that, and a confidence floor alone would still fire on a 6-character fragment that scores 1.0 by having nothing else to compare against. Below either threshold, `langdetect.Detect` reports `ok=false` — the caller must treat that exactly like "nothing to flag", never as "matches the target".

**Which fields feed detection/translation.** `CharacterCard`: `Description`, `Personality`, `Scenario`, `FirstMessage` — the four fields Lord named explicitly. `ExampleMessages` and `AuthorNote` are excluded from both detection and translation on purpose: an author's note is often meta-commentary (recommended model, how to run the card) that can read as a different language than the card's own voice without that being a real VO/VF case. `Lorebook`: every entry's `Content`, never `Keys` — trigger keys are matched verbatim against the user's own chat messages (see `LorebookEntry`'s own doc comment), so translating them would silently break triggering; they ride through byte-for-byte into the VF copy.

**Display names, never a hardcoded language table.** The warning ("Langue détectée : {{detected}}. L'import conservera l'original et créera une copie en {{target}}." / its English variant, both via `i18n.T` + `import.language_mismatch_warning`) needs human-readable names for both the detected and target languages. `languageDisplayName` (handlers_import.go) prefers `internal/i18n`'s own `Registry.Languages()` → `Language.Name` when the code matches a UI language this install actually ships (the *same* string the nav switcher already shows, e.g. "Français" not "French") — `AppSettings.Language` always matches one, since it can only ever be set to an installed language. For the *detected* language, that lookup usually misses (an install typically ships only `en.json`/`fr.json`, but whatlanggo recognizes dozens more) — the fallback is whatlanggo's own English name for that language (`langdetect.Result.Name`, via its `LangToString`), not a second table AIvalon maintains itself. Either way, no per-language sentence is hardcoded in Go.

**Confirm/cancel flow.** Mirrors the existing `ImportCrossConfirmPage` pattern exactly (same `ImportCache` token handoff, same "POST to resume / plain link to cancel" shape): `ImportLanguageConfirmPage` is rendered *before anything is created* when detection fires; the raw upload bytes are stashed under a token (`ImportCache.Stash`, 5-minute TTL, same as the wrong-importer flow) rather than round-tripped through a hidden form field. Confirm POSTs the token to `/characters/import/confirm-language` or `/world/import/confirm-language`, which redeems it and performs the actual double import. Cancel creates nothing at all — same contract as the cross-confirm page's "No".

**Translation backend and fallback.** Translation goes through `internal/llmclient.StreamChat` against the first `kind="llm"` `BackendProfile` found (`db.ListBackendProfiles` is already name-ordered; `AppSettings` has no separate "default backend" concept worth adding just for this) — never a third-party translation API, consistent with the rest of AIvalon's local-first design. Each field/entry gets its own prompt ("translate into {{language}}, preserve *asterisks*/"quotes"/line breaks/`{{placeholders}}`, output only the translation") and its own 45-second timeout; a lorebook's entries are translated one at a time; sequentially, not batched into one call — simple, but means a very large lorebook's VF copy takes proportionally longer. Translation is all-or-nothing per card/lorebook: if any single field's call fails, the whole VF attempt is abandoned rather than landing a partially-translated copy. Fallback (no `kind="llm"` profile configured, or the attempt failed) always imports VO alone and renders `ImportLanguageResultPage` naming why (`import.translation_unavailable_no_backend` / `_failed`) — the import request itself never fails or 500s over a missing/broken translation backend; only the double-import's second half is skipped. The one exception: if a character's own four fields translate fine but its *embedded lorebook* then fails to translate, the already-created VF character is kept (a degraded-but-real VF card beats discarding it) and the same warning is still shown.

**Result page, not a bare redirect.** `ImportLanguageResultPage` always renders after a confirmed double import (success or fallback) rather than redirecting straight to the new character/lorebook, specifically so the fallback case has somewhere to say "the VF copy wasn't made" — a plain redirect would otherwise silently produce only one of the two entries the user just confirmed expecting.

## Open questions

- ⬜ TLS (self-signed by default, custom certificate importable in Settings) is wanted, but it answers a different question than the one above: it protects the connection to the server, not what is written to `character_cards`/`backend_profiles` on disk.
  The scope question is now answered yes (`AIVALON_ADDR=0.0.0.0:PORT` + `requireLocalHost`'s private-range allowance, see "Default bind" above) — AIvalon is reachable from another device on the same home WiFi today. Traffic there is still plain HTTP: readable by anything else on that WiFi (an accepted risk on a trusted home network, Lord's own call), but still not implemented. Tracked here, not blocking.
