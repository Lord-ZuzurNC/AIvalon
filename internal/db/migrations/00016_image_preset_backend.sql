-- +goose Up

-- Ties a preset to the image backend it's meant to run on (Lord's
-- report: "Realistic" always means Juggernaut, "Manga" always means
-- Pony — nothing should make him re-pick the backend every single
-- generation). NULL = unset: the generation dialog falls back to its
-- manual backend picker, same as before this column existed.
ALTER TABLE image_presets ADD COLUMN backend_profile_id INTEGER REFERENCES backend_profiles(id) ON DELETE SET NULL;

-- +goose Down

-- SQLite refuses DROP COLUMN on a column with a FOREIGN KEY (see
-- 00013_session_image_preset.sql); not worth a table rebuild for a
-- nullable column nothing else depends on — leaving it in place is harmless.
SELECT 1;
