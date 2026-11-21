-- +goose Up

-- The session's chosen image preset (picked in the chat's session
-- panel, Gallery tab). NULL = none chosen: generation falls back to
-- defaultImageParams. Deleting a preset just resets its sessions.
ALTER TABLE sessions ADD COLUMN image_preset_id INTEGER REFERENCES image_presets(id) ON DELETE SET NULL;

-- +goose Down

-- SQLite refuses DROP COLUMN on a column with a FOREIGN KEY, so this
-- needs a table rebuild; not worth it for a nullable column nothing
-- else depends on — leaving it in place is harmless.
SELECT 1;
