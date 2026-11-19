-- +goose Up

-- Same pattern as 00005's character/lorebook image_path: a path
-- relative to web/static (e.g. "uploads/personas/3.png"), served by
-- the existing GET /static/ file server. Empty string means "no
-- image uploaded" — the UI falls back to Avatar's initial-letter
-- placeholder rather than treating empty as an error.
ALTER TABLE personas ADD COLUMN image_path TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE personas DROP COLUMN image_path;
