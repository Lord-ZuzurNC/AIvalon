-- +goose Up

-- Singleton row (id always 1): the vault has exactly one PIN, not one
-- per session — "ranger des sessions secrètes derrière un seul code"
-- is the whole feature, not a multi-tenant vault system.
CREATE TABLE vault (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    pin_hash   TEXT NOT NULL,
    pin_salt   TEXT NOT NULL,
    digits     INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

ALTER TABLE sessions ADD COLUMN is_vaulted INTEGER NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE sessions DROP COLUMN is_vaulted;
DROP TABLE vault;
