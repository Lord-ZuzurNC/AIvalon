-- +goose Up

-- Both columns hold a path relative to web/static (e.g.
-- "uploads/characters/3.png"), served by the existing GET /static/
-- file server — no new serving endpoint needed. Empty string means "no
-- image uploaded"; the UI falls back to a placeholder rather than
-- treating empty as an error.
ALTER TABLE character_cards ADD COLUMN image_path TEXT NOT NULL DEFAULT '';
ALTER TABLE lorebooks ADD COLUMN image_path TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE character_cards DROP COLUMN image_path;
ALTER TABLE lorebooks DROP COLUMN image_path;
