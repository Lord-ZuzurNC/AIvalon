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
    role               TEXT NOT NULL,  -- 'user' | 'character' | 'system'
    character_card_id  INTEGER REFERENCES character_cards(id),  -- set when role = 'character'
    content            TEXT NOT NULL,
    image_path         TEXT,  -- v2
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
