-- +goose Up

-- "Author's note": some Character Card V2/V3 exports carry a creator's
-- note field (often tips on how to run the character, intended model,
-- etc — see https://github.com/malfoyslastname/character-card-spec-v2,
-- "creator_notes"). AIvalon never modeled it, so a manually-created card
-- had nowhere to put one. Additive column, default '', existing rows
-- unaffected.
ALTER TABLE character_cards ADD COLUMN author_note TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE character_cards DROP COLUMN author_note;
