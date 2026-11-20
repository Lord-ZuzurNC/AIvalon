-- +goose Up

-- Hosts (besides loopback, which is always allowed) that Character /
-- World's "From URL" import may fetch from — see internal/web/
-- handlers_import.go's checkImportHost. One hostname per line, matched
-- exactly and case-insensitively, port ignored. Anything fetched from
-- one of these must be a Character Card PNG; JSON imports stay
-- loopback-only. Seeded with the common card-sharing sites.
ALTER TABLE app_settings ADD COLUMN png_import_whitelist TEXT NOT NULL DEFAULT 'character-tavern.com
aicharactercards.com
charavault.net
sillytavern.us
www.characterhub.org
taverncard.com';

-- +goose Down

ALTER TABLE app_settings DROP COLUMN png_import_whitelist;
