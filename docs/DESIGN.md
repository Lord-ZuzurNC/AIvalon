# AIvalon — Design

AIvalon is a local, single-user web app for AI roleplay.
It replaces OOC: instead of a rate-limited hosted service, it talks to a locally-running LLM and keeps everything — characters, world lore, chat history — on the user's own machine.
No account system, no multi-tenancy, no network exposure by default.

## Scope

**v1** ships text-only roleplay: chat, character cards, lorebooks, and the settings needed to point AIvalon at a local model.
**v2** adds on-demand image generation inside a chat session.
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
The v2 `image` kind targets the **AUTOMATIC1111 (A1111) Stable Diffusion WebUI API** (`/sdapi/v1/txt2img` and friends) — picked for being the simplest of the common options, not implemented until v2.

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

## Open questions

- ⬜ TLS (self-signed by default, custom certificate importable in Settings) is wanted, but it answers a different question than the one above: it protects the connection to the server, not what is written to `character_cards`/`backend_profiles` on disk.
  The scope question is now answered yes (`AIVALON_ADDR=0.0.0.0:PORT` + `requireLocalHost`'s private-range allowance, see "Default bind" above) — AIvalon is reachable from another device on the same home WiFi today. Traffic there is still plain HTTP: readable by anything else on that WiFi (an accepted risk on a trusted home network, Lord's own call), but still not implemented. Tracked here, not blocking.
