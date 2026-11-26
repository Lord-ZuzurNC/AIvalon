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

**Upper length bound, added later (task t_b471483e): whatlanggo's confidence formula breaks down on long input, and the break is NOT a simple sign flip.** Lord imported a real card (`veyra.png`, English, ~700-char `description`/`first_message`) with `AppSettings.Language="fr"` and got no warning at all. Root cause: `whatlanggo.calculateConfidence` computes `rate := (score1-score2)/score2`; `score2` (the runner-up language's score) can be *negative* once its trigram distance exceeds `maxTotalDistance` — something that only starts happening once the input is long enough that the winning language's match is extremely dominant. When `score2 < 0`, `rate` and therefore `Confidence` come out as a large *negative* number, which the old code's `info.Confidence < minConfidence` check rejected as "not reliable" — exactly backwards for this case: the real card's concatenated fields (≈7900 runes) scored `confidence ≈ -39` while whatlanggo's own top guess (English) was correct.

The obvious-looking fix — "negative confidence means no real competitor, so treat it as reliable" (`Confidence <= 0` ⟹ trust it) — was tested against a much wider sample than the original bug and **rejected, because it is unsafe**: it was disproven by direct counter-example, not just reasoned away.
- Real card text + 6 Wikipedia articles (en/fr/de/es/eo), all genuinely single-language and correctly top-guessed, scored strongly negative confidence (`-39` to `-273`) when fed to whatlanggo unmodified — consistent with the "no competitor" theory.
- But 9 Wikipedia articles in languages whatlanggo has **no profile for at all** (Catalan, Basque, Occitan, Breton, Welsh, Frisian, Galician — all forced to the nearest of its ~84 supported languages) also went negative, and were **wrong** (e.g. a long, clean Catalan article top-guessed as Hungarian at confidence `-6.0`; Occitan as French at confidence `-38.1`; Frisian — linguistically close to Dutch — as Dutch at confidence `-95.5`, a *larger* magnitude than several of the genuinely-correct French/German/Spanish samples above).
- Because the wrong-and-negative sample's magnitude range (`-6` to `-95`) overlaps the correct-and-negative sample's range (`-39` to `-273`), **no threshold on sign or magnitude of `Confidence` can separate the two groups** — not `<= 0`, not any fixed cutoff. The raw float is not a calibrated probability once `score2` goes negative; whatlanggo's own `Info.IsReliable()` (`Confidence > ReliableConfidenceThreshold`) already treats negative values as unreliable by the library's own stated design, so "reinterpreting" them is overriding the author's intent based on one anecdote, not a documented guarantee.

**The actual fix: dodge the pathological region instead of reinterpreting it.** `maxDetectLength` (2000 runes) truncates the text *before* it reaches `whatlanggo.Detect`. Empirically (same sample set above, truncated at several lengths): confidence stayed in the well-behaved `[0, 1]` range for every sample up to 3000 runes; negative confidence started appearing at 4000+ runes, including for genuinely correct English/French/German detections. 2000 runes leaves a wide safety margin (33% below the 3000-rune "still fine" ceiling, 50% below the first observed failure at 4000) while comfortably exceeding any single card field's typical length — a card's four fields rarely each run past a few hundred to ~1000 characters, so truncation essentially never discards real signal, it only prevents whatlanggo's own internal math from entering a region its author didn't calibrate. This leaves `minConfidence`'s existing `[0, 1]` interpretation completely untouched — no new interpretation of `Confidence`'s sign or magnitude, just keeping the input inside the range the library was actually built for. Regression test: `internal/langdetect/testdata/veyra_card_text.txt` (the real card text from this bug) plus `TestDetect_RealCardFailsPriorToLengthCap` (proves the untruncated failure is real, calling whatlanggo directly) and `TestDetect_RealCardIsDetectedAfterLengthCap` (proves `langdetect.Detect` now succeeds on it).

This does not fix whatlanggo's inherent inability to recognize languages outside its ~84-language set (Catalan, Occitan, etc. will keep being silently mapped to their nearest supported relative, as they already were before this task, at confidence 1.0 in the short/medium case) — that is a pre-existing, documented limitation of the chosen library, not something this fix introduced or could reasonably resolve without a different detection library entirely.

**Which fields feed detection/translation.** `CharacterCard`: detection reads exactly `Description`, `Personality`, `Scenario`, `FirstMessage` — the four fields Lord named explicitly; `ExampleMessages` and `AuthorNote` are excluded from DETECTION on purpose, since an author's note is often meta-commentary (recommended model, how to run the card) that can read as a different language than the card's own voice without that being a real VO/VF case. TRANSLATION (what the VF copy actually gets) covers one more field than detection: `AuthorNote` is translated too (task t_166318f0/E), even though it plays no part in deciding whether a mismatch fires — it ends up injected into the character's own system prompt (`substituteCardMacros`, handlers_chat.go), so leaving it in the VO's language on the VF copy would hand the model a system prompt mixing two languages. `Lorebook`: every entry's `Content`, and — reversed from this task's original call — every entry's `Keys` too (task t_166318f0/D, superseding Part C's "never the keys" below). DETECTION still never looks at `Keys` (`lorebookLanguageText` is unchanged: they carry no prose-language signal), only translation changed.
  - *Superseded reasoning, kept for the record:* Part C shipped translation skipping `Keys` entirely, on the theory that they're matched verbatim against the user's own chat messages (see `LorebookEntry`'s own doc comment) and translating them was pure risk for no benefit. Lord's real-world test of a translated (VF) lorebook disproved this: its Keys stayed in the original language, so a player typing in the *target* language (French) never matched any of them — the translated copy's lorebook silently never triggered, defeating the entire point of making a VF copy. Keys are now translated through a dedicated prompt (`translateKeywordText`, import_language.go) rather than reusing the prose prompt (`translateText`): a bare 1-3 word trigger key fed the prose prompt risked the model "helpfully" expanding it into a descriptive sentence, which would break word-boundary matching (`keysMatch`, lorebook_resolve.go) just as badly as leaving it untranslated. The keyword prompt explicitly instructs "keep it just as short, never a sentence" instead.

**Display names, never a hardcoded language table.** The warning ("Langue détectée : {{detected}}. L'import conservera l'original et créera une copie en {{target}}." / its English variant, both via `i18n.T` + `import.language_mismatch_warning`) needs human-readable names for both the detected and target languages. `languageDisplayName` (handlers_import.go) prefers `internal/i18n`'s own `Registry.Languages()` → `Language.Name` when the code matches a UI language this install actually ships (the *same* string the nav switcher already shows, e.g. "Français" not "French") — `AppSettings.Language` always matches one, since it can only ever be set to an installed language. For the *detected* language, that lookup usually misses (an install typically ships only `en.json`/`fr.json`, but whatlanggo recognizes dozens more) — the fallback is whatlanggo's own English name for that language (`langdetect.Result.Name`, via its `LangToString`), not a second table AIvalon maintains itself. Either way, no per-language sentence is hardcoded in Go.

**Confirm/cancel flow.** Mirrors the existing `ImportCrossConfirmPage` pattern exactly (same `ImportCache` token handoff, same "POST to resume / plain link to cancel" shape): `ImportLanguageConfirmPage` is rendered *before anything is created* when detection fires; the raw upload bytes are stashed under a token (`ImportCache.Stash`, 5-minute TTL, same as the wrong-importer flow) rather than round-tripped through a hidden form field. Confirm POSTs the token to `/characters/import/confirm-language` or `/world/import/confirm-language`, which redeems it and performs the actual double import. Cancel creates nothing at all — same contract as the cross-confirm page's "No".

**Translation backend and fallback.** Translation goes through `internal/llmclient.StreamChat` against the first `kind="llm"` `BackendProfile` found (`db.ListBackendProfiles` is already name-ordered; `AppSettings` has no separate "default backend" concept worth adding just for this) — never a third-party translation API, consistent with the rest of AIvalon's local-first design. Each field/entry gets its own prompt ("translate into {{language}}, preserve *asterisks*/"quotes"/line breaks/`{{placeholders}}`, output only the translation") and its own `translateFieldTimeout` (200s by default, see below) — a lorebook's entries are translated one at a time, sequentially, not batched into one call — simple, but means a very large lorebook's VF copy takes proportionally longer. Translation is all-or-nothing per card/lorebook: if any single field's call fails, the whole VF attempt is abandoned rather than landing a partially-translated copy. Fallback (no `kind="llm"` profile configured, or the attempt failed) always imports VO alone and renders `ImportLanguageResultPage` naming why (`import.translation_unavailable_no_backend` / `_failed`) — the import request itself never fails or 500s over a missing/broken translation backend; only the double-import's second half is skipped. The one exception: if a character's own four fields translate fine but its *embedded lorebook* then fails to translate, the already-created VF character is kept (a degraded-but-real VF card beats discarding it) and the same warning is still shown.

**Per-call `max_tokens` cap, added later (task t_3a415d4d): the 45s timeout alone doesn't stop a model that won't stop itself.** Lord re-imported `veyra.png` and got repeated translation failures; his pasted `llama.cpp` server log showed the real mechanism — a translation call still streaming at 1878+ tokens, growing steadily at ~44 tok/s with no sign of ending, until `translateFieldTimeout` (45s) cut the HTTP connection client-side (`http client error: Failed to read connection` / `cancel task`). The model (a locally-run "abliterated"/uncensored fine-tune, less reliable at strictly obeying a short-output instruction) was not malfunctioning — generation was healthy, just unbounded. Root cause: `llmclient.StreamChat` only ever sends `messages`/`stream` plus whatever `profile.ModelParams` already holds (Lord's own chat tuning, e.g. `{"temperature":1,"top_p":0.9}` — no `max_tokens` in it, since for a real roleplay turn Lord wants no cap at all). Nothing in the translation path ever told the backend to stop at a sane length.

The fix stays entirely inside `import_language.go` — zero change to `llmclient.StreamChat` or to the `BackendProfile`/`ModelParams` type, so normal chat keeps its own uncapped behavior untouched. `withMaxTokens(profile, n)` builds a fresh copy of `profile` whose `ModelParams` map carries `"max_tokens": n` (overriding any existing value in that copy only — the caller's own `BackendProfile`/map is never mutated), and both `translateText` and `translateKeywordText` call it before handing the profile to `StreamChat`. Two different caps, matched to what's actually being translated:
- `translateTextMaxTokens = 1024` for prose fields (description/personality/first_message/author note/lorebook entry content) — generous for any realistic field, and at the ~44 tok/s observed in Lord's own logs that's ~23s, leaving a wide margin under the 45s timeout.
- `translateKeywordMaxTokens = 64` for a single trigger keyword/short phrase (`translateKeywordText`) — a correct translation never gets remotely close to this; hitting it at all is itself a sign of the same "won't stop" failure mode on a much smaller scale.

This is deliberately a translation-only fix, not a general chat change: for an actual roleplay conversation "no cap" is Lord's own choice (full control over generation length), and nothing about that path is touched. Test coverage (`internal/web/import_language_test.go`): `TestTranslateText_SendsMaxTokensCap` / `TestTranslateKeywordText_SendsLowerMaxTokensCap` use a mock backend that streams indefinitely unless the request body itself carries `max_tokens`, in which case it stops at exactly that count — proving the real per-call HTTP payload now carries the cap, and that the call finishes almost immediately instead of running out the clock. `TestTranslateText_MaxTokensCapOverridesExistingProfileSetting` proves the override is per-call only (the original `BackendProfile.ModelParams` is left exactly as configured for chat use). `TestConfirmLanguageImport_Character_FallsBackOnTranslationTimeout` reproduces the *other* half of the original incident end-to-end: a backend that accepts the connection and never answers at all, so the only thing that ends the call is `translateFieldTimeout`'s own context deadline (shrunk to 200ms for the test, saved/restored around it — hence `translateFieldTimeout` being a package `var`, not a `const`) — confirming the result is still the existing VO-only fallback page, never a 500.

**Timeout raised 45s→200s, made configurable, and failure causes now logged server-side (task t_45a55587).** `max_tokens` (above) stops a model that won't stop itself, but it never addressed the OTHER half of the same incident: 45s was simply too short for a *normal, healthy* local generation to even finish prompt processing plus up to `translateTextMaxTokens` (1024) tokens of output. Lord re-imported the same card a second time (wanting to see the `(2)` dedup suffix — confirmed still working, see `TestConfirmLanguageImport_Character_SecondImportGetsNumberedSuffix`) and got three straight translation failures; his pasted real `llama.cpp` server log was unambiguous:
```
print_timing: id 0 | task 22445 | n_gen = 1878, tg = 44.53 t/s, tg_3s = 45.58 t/s
srv operator(): http client error: Failed to read connection
W srv stop: cancel task, id_task = 22445
slot release: id 0 | task 22445 | stop processing: n_tokens = 3570, truncated = 0
```
The server was still actively generating — 1878 tokens already produced at ~44 tok/s, 3570 tokens of context — when `translateFieldTimeout` (45s) cut the client-side connection. Not a stuck model: a healthy one that simply hadn't finished. At ~44 tok/s, `translateTextMaxTokens`'s own 1024-token cap can legitimately take up to ~23s of pure generation, before counting a multi-thousand-token prompt's own processing time on a loaded local box.

`translateFieldTimeoutDefault` is now `200 * time.Second` (≈9x the 23s generation-only estimate above) — chosen to comfortably survive hardware meaningfully slower than the 44 tok/s observed here, while still bounding a single stuck field to a few minutes rather than hanging the import request forever. `AIVALON_TRANSLATE_TIMEOUT_SECONDS` (positive integer seconds; unset/non-numeric/zero/negative all fall back to the 200s default, logged once) lets Lord tune this further per his own hardware without recompiling — same `envOr`-style convention as `AIVALON_ADDR`/`AIVALON_DB` (`cmd/aivalon/main.go`), but living in `internal/web` since `translateFieldTimeout` is a `web`-package var, not read via `main.go`. Test coverage: `TestTranslateFieldTimeoutFromEnv` (every unset/valid/invalid env value) and `TestTranslateCardFields_RealisticTimeoutSurvivesASlowButFiniteBackend`, which proves the fix has a REAL effect rather than just a changed constant — it runs the exact same slow-but-finite mock backend twice, failing against a timeout shorter than the backend's response time and succeeding against a longer one, with nothing else different between the two calls (durations scaled to milliseconds so the test runs instantly; what matters is the delay-vs-timeout ratio, the same ratio separating the real 45s/200s numbers).

**Timeout vs. real backend failure, now distinguished in the server log only.** Both a timeout and a genuine backend error (unreachable host, non-200 HTTP response, connection reset mid-stream) fall into the exact same user-facing VO-only fallback (`import.translation_unavailable_failed`) — that UX choice is unchanged, and correct: the user doesn't need three different error messages for "the translation didn't happen." But diagnosing *why* previously meant Lord pulling raw `llama.cpp` server logs off his own machine, as happened for this very task. `classifyTranslationError` (`import_language.go`) now logs one of three labels via the standard `log` package (stdout, server-side only, never the UI) at every translation failure point (`translateCardFields`'s per-field loop, `translateLorebookEntries`'s per-entry and per-key loops): `timeout` (checked via `fieldCtx.Err() == context.DeadlineExceeded` — the context this file itself created, not a guess from the error's text), `http_status` (a non-2xx response — `llmclient.StreamChat`'s own literal `"backend returned <status>: ..."` wording, matched as a string rather than a new sentinel error type so this stays a translation-path-only classification with zero change to `llmclient` itself), or `network` (anything else: connection refused, DNS failure, reset mid-stream, ...). Confirmed against real failure modes, not just read from the code: `TestConfirmLanguageImport_Character_FallsBackOnTranslationTimeout` logs `(timeout): context deadline exceeded`, `TestConfirmLanguageImport_Character_FallsBackOnTranslationError` (a `BaseURL` pointing at a port nothing listens on) logs `(network): calling backend: ... connection refused`.

**Result page, not a bare redirect.** `ImportLanguageResultPage` always renders after a confirmed double import (success or fallback) rather than redirecting straight to the new character/lorebook, specifically so the fallback case has somewhere to say "the VF copy wasn't made" — a plain redirect would otherwise silently produce only one of the two entries the user just confirmed expecting.

**Modal shape + loading feedback (task t_166318f0/A-C).** Lord tested this flow for real against `veyra.png` and read the ~1-2 minute silent wait during `ConfirmLanguageImport` (sequential per-field/per-entry translation calls, no batching — see "Translation backend and fallback" above) as a *failed* import, when in fact everything had actually succeeded; he'd simply navigated away before the response came back. Two independent fixes: (1) `ImportLanguageConfirmPage`/`ImportLanguageResultPage` were plain `<h1>/<p>` full-width pages — visually indistinguishable from a blank navigated-to page — now reshaped into the same `<dialog open><article>` convention every other modal in this app uses (`DeleteConfirmPage`), with the Confirm/Cancel pair sharing a `min-width` so the long "Confirmer le double import" string doesn't dwarf "Annuler" next to it. (2) A second, initially-hidden `<dialog id="import-loading-dialog">` is opened by the confirm form's own `onsubmit` (`showModal()`, closing the confirm dialog first) right as the POST fires — plain inline JS, no htmx/SSE needed, since the form is still a normal POST that navigates away the moment the server responds; the loading dialog simply disappears along with the rest of the old document at that point.

## Multi-file import

Task t_b471483e, Lord's own ask: pick several character-card/lorebook files in `ImportModal` and import them all in one action instead of one at a time.

**HTML side.** `ImportModal`'s "From your Computer" `<input type="file" name="file">` gained a bare `multiple` attribute — nothing else about the form changed. A browser that submits N files this way sends them as N parts all named `"file"` in the same multipart body; `r.MultipartForm.File["file"]` already returns the full slice, so the server side only needed to start reading that slice when it has more than one entry.

**Dispatch: single-file path stays byte-for-byte unchanged.** `ImportCharacterJSON`/`ImportLorebookJSON` call a new `parseMultipartFileHeaders` first, which is a no-op (`isMultipart=false`, no error) for the "From URL" tab (not a multipart request at all) and otherwise just returns the `*multipart.FileHeader` slice without reading any bytes. Only when `len(headers) > 1` does the handler branch into the new batch path (`importCharacterBatch`/`importLorebookBatch`); a single file (or a URL) falls straight through to the exact same `readUploadOrURL` + `importCharacterUpload`/`importLorebookUpload` call that existed before this task — every single-file test in this repo keeps passing unmodified, because that code path is untouched, not just "equivalent".

**UX decision: one combined confirmation screen, not N sequential ones.** If N files are imported and M of them trigger a language mismatch, Lord's own options were "one page listing every mismatch with Confirm all/Cancel all" vs "walk through them one at a time". The combined screen (`ImportBatchLanguageConfirmPage`) was chosen for two reasons: (1) N round trips for N mismatched files is worse UX than one shared decision, especially since VO/VF is an all-or-nothing choice per Lord's own existing design (there's no partial-translate option even for a single file today); (2) the existing `ImportCache` token mechanism (see import_cache.go) already supports stashing arbitrarily many independent files under independent tokens with zero changes — the batch confirm page just renders N hidden `<input name="tokens">` fields and one shared Confirm/Cancel pair, posting all of them to one new endpoint (`/characters/import/confirm-language-batch`, `/world/import/confirm-language-batch`) that loops over `r.Form["tokens"]`. A sequential per-file flow would have needed new session/cursor state to track "which file am I confirming now" across page loads; the combined screen needs none.

**Files that don't need confirmation import immediately, in the same request.** A batch's non-mismatched files are created right away during the first pass (same as a single-file import would), *before* the confirm screen even renders — they are never "pending". Their names are carried forward as hidden `already_done` fields on the confirm screen purely so the FINAL report (after Confirm/Cancel) still lists them; nothing about them is touched again. "Cancel all" only prevents the still-pending (mismatched) files from being created — it cannot and does not undo the already-done ones, same contract as a single file's "No" never undoing something a separate request already created.

**Partial failure never aborts the batch.** Each file is processed independently inside one loop; a parse error, a wrong-kind upload (a Lorebook dropped into Character's batch importer, or vice versa), or a DB error on one file is recorded as a `batchFileError{filename, reason}` and the loop moves on to the next file. The one simplification from the single-file flow: a wrong-kind file in a BATCH is reported as a plain per-file error (`import.batch_wrong_kind_lorebook` / `_character`) rather than offered `ImportCrossConfirmPage`'s own interactive "import it there instead?" detour — that detour is inherently a single-file, single-decision UI and has no clean way to scale to "N of these M files look like the wrong kind" without its own sub-batch-within-a-batch complexity. Documented trade-off: Lord re-imports a misrouted file through the right importer directly rather than getting an in-place redirect offer, for batch uploads only (a single misrouted file still gets the existing interactive offer).

**Code reuse, not duplication.** `importCharacterDual`/`importLorebookDual` (the single-file VO+VF pipeline) were split into a thin HTTP-rendering wrapper plus a `*Core` function that takes a `context.Context` instead of `http.ResponseWriter`/`*http.Request` and returns values instead of writing a response — the single-file handlers and the batch confirm handlers both call the same `*Core` function now, so there is exactly one place that creates a VO+VF pair, not two. Likewise `createCharacterFromUpload`/`createLorebookFromUpload` extract the "dedupe name, create row, save image, create embedded lorebook" tail that both the single-file and batch "no mismatch" paths share.

**Final report, always, no silent drops.** `ImportBatchResultPage` lists every successfully created name and every `batchFileError` with its reason — a batch that succeeds completely, fails completely, or lands somewhere in between all render the same page, so Lord never has to guess what happened to a file that doesn't show up anywhere. Per-item VO/VF/warning nuance (`ImportLanguageResultPage`'s `voOnly`/`warning` fields) is deliberately NOT carried through to the batch report per-item — `batchResultName` just folds a confirmed item into `"VO / VF"` or plain `"VO"` — documented simplification, since surfacing N independent translation-fallback warnings inline would clutter a report whose job is mainly "what landed, what didn't".

## Language switcher visible in the collapsed sidebar

Task t_b471483e, Lord's own ask: the `languageSwitcher()` badge (above the About link, see templates.templ) disappeared entirely when the sidebar is collapsed to icons-only.

**Root cause: `display: none`, not a sizing problem.** `languageSwitcher()` was already unconditionally rendered by `Layout` in both sidebar modes — the only thing hiding it in collapsed mode was `html.sidebar-collapsed .lang-switcher { display: none; }` in aivalon.css, added defensively ("v1", no glyph-only treatment attempted) when the collapse feature first shipped. The trigger itself is already exactly the compact form the task asked for (a 2-letter code badge, e.g. "EN"/"FR", no separate label text) — nothing about the component needed a NEW compact variant, just un-hiding the existing one.

**A real layout bug surfaced the moment it was un-hidden, found only by actually rendering it (chromium headless, not just reading the CSS).** `.nav-footer` has no `display` rule of its own — a plain block `<nav>`, which happened to work fine when it held only one visible child (the `<ul>` with the About link; `.lang-switcher` contributed nothing to layout while `display:none`). The instant `.lang-switcher` became a real (flex) box, block-flow stacking between it and the following `<ul>` stopped behaving as a simple top-to-bottom stack — confirmed by screenshotting the collapsed sidebar with debug outlines: the About `<a>` was still present in the DOM (same size/position data) but visually landing beside the EN badge instead of below it. Fix: `html.sidebar-collapsed .nav-footer { display: flex; flex-direction: column; align-items: center; }` — an explicit flex column, matching what the collapsed rail's icon-only items already look like visually. Scoped to `.sidebar-collapsed` only, so the already-shipped expanded layout (verified unaffected, see below) stays untouched.

**Dropdown positioning: `position: fixed`, not `absolute`, only when collapsed.** The dropdown's menu items are full language names ("Français"), wider than the collapsed rail's 56px (see `html.sidebar-collapsed > body > header`'s `max-width`), and the rail has `overflow-x: hidden` (needed for the width-transition animation). An absolutely-positioned dropdown anchored to the trigger (the expanded mode's own approach) would get clipped by that ancestor overflow well before reaching its own `min-width`. `position: fixed` escapes that containing block (no ancestor in this chain sets `transform`, confirmed by grep, so fixed truly resolves against the viewport) — offsets (`left: 64px`, `bottom: 3.5rem`) are hardcoded against the rail's own fixed collapsed width and the nav-footer's fixed bottom slot, both CSS constants here, not computed values that could silently drift.

**Verification: real rendering, not code inspection.** Built the binary, ran it, fetched the actual rendered `/chat` page over HTTP, and rendered it through real Chromium (`chromium --headless --screenshot`) in three states: collapsed/closed, collapsed/dropdown-open, and expanded (unmodified, for regression comparison) — not a visual guess from reading the CSS. Confirmed: the EN badge sits directly above a visible, correctly-shaped About icon with no overlap in collapsed mode; the opened dropdown renders its language options without being clipped by the sidebar edge; the expanded-mode screenshot shows the exact same nav/badge/About layout as before this task (the new CSS rule is `html.sidebar-collapsed`-scoped and never applies there). Screenshots aren't part of this repo (one-off debugging artifacts, not fixtures) — this section records the finding and the fix, not a committed asset.

**Follow-up regression found by Lord (task t_166318f0/H): the ACTIVE language in the dropdown rendered as an empty box.** Not a sizing or background-color coincidence as first suspected — a genuine selector/markup mismatch, confirmed only by an actual before/after Chromium screenshot (same method as above), not by re-reading the CSS. `languageSwitcher()` (templates.templ) marks the active option with `aria-current?={ l.Code == current }`; templ's `?=` renders this as a bare boolean attribute (`aria-current`, no value) when true and omits it entirely when false — it never emits the literal string `aria-current="true"`. The CSS rule meant to bold/gold the active item was `.lang-switcher-dropdown button[aria-current="true"]` — a value-equality selector that consequently never matched anything, active or not. With that rule inert, the active button fell through to Pico's own built-in `button:is([aria-current]:not([aria-current=false]))` hover/active-state rule (which DOES match a bare `aria-current`, and at higher specificity than this file's `.options-dropdown button` color reset), setting its text color to `--pico-primary-inverse` — a near-black tone picked for *filled* primary buttons, nearly indistinguishable from the dark dropdown background here. Fix: `[aria-current]` (presence-only, matching what templ actually emits) instead of `[aria-current="true"]`, same specificity, now fires correctly — confirmed by a before/after screenshot pair showing the French option switch from an invisible box to bold gold text.

## Open questions

- ⬜ TLS (self-signed by default, custom certificate importable in Settings) is wanted, but it answers a different question than the one above: it protects the connection to the server, not what is written to `character_cards`/`backend_profiles` on disk.
  The scope question is now answered yes (`AIVALON_ADDR=0.0.0.0:PORT` + `requireLocalHost`'s private-range allowance, see "Default bind" above) — AIvalon is reachable from another device on the same home WiFi today. Traffic there is still plain HTTP: readable by anything else on that WiFi (an accepted risk on a trusted home network, Lord's own call), but still not implemented. Tracked here, not blocking.
