-- +goose Up

-- Empty string (default) means "no custom background — just the app's
-- own theme". Lord's ask: let a session borrow one of its attached
-- Lorebook's or Characters' already-uploaded images as a wallpaper
-- behind the chat, rather than adding a whole separate upload flow for
-- it.
ALTER TABLE sessions ADD COLUMN background_image_path TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE sessions DROP COLUMN background_image_path;
