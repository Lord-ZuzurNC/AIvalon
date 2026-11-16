-- +goose Up

-- A character card's image gallery: every uploaded image lives here
-- (one row per file), with character_cards.primary_image_id pointing
-- at whichever one is "the" avatar shown everywhere else (list rows,
-- chat, etc.) — see internal/web/handlers_character.go for the upload
-- flow (first image on a card auto-becomes primary; later ones are
-- added as secondary until explicitly promoted).
CREATE TABLE character_card_images (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    character_card_id  INTEGER NOT NULL REFERENCES character_cards(id) ON DELETE CASCADE,
    image_path         TEXT NOT NULL,
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX idx_character_card_images_card ON character_card_images(character_card_id);

-- ON DELETE SET NULL: deleting the gallery row that happens to be the
-- current primary clears the pointer automatically — the app falls
-- back to Avatar's initial-letter display rather than needing to pick
-- a new primary itself.
ALTER TABLE character_cards ADD COLUMN primary_image_id INTEGER REFERENCES character_card_images(id) ON DELETE SET NULL;

-- Backfill: a character that already had the old single image_path
-- (e.g. a portrait uploaded before this migration) gets a gallery row
-- for it, with primary_image_id pointing there — so existing avatars
-- keep showing up unchanged, just resolved through the new table from
-- here on. The legacy character_cards.image_path column itself is left
-- in place (unused from now on) rather than dropped: SQLite's DROP
-- COLUMN support is fine here, but there's no reason to risk a data
-- loss bug in the Down migration for a column nothing reads anymore.
INSERT INTO character_card_images (character_card_id, image_path)
SELECT id, image_path FROM character_cards WHERE image_path != '';

UPDATE character_cards
SET primary_image_id = (
    SELECT id FROM character_card_images
    WHERE character_card_images.character_card_id = character_cards.id
    ORDER BY character_card_images.id ASC
    LIMIT 1
)
WHERE image_path != '';

-- +goose Down

ALTER TABLE character_cards DROP COLUMN primary_image_id;
DROP TABLE character_card_images;
