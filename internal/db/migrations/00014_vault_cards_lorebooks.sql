-- +goose Up

-- Same convention as sessions.is_vaulted (00007): hidden from browsing
-- lists, still resolvable by id for anything already referencing it.
ALTER TABLE character_cards ADD COLUMN is_vaulted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE lorebooks ADD COLUMN is_vaulted INTEGER NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE lorebooks DROP COLUMN is_vaulted;
ALTER TABLE character_cards DROP COLUMN is_vaulted;
