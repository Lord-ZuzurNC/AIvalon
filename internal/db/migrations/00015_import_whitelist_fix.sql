-- +goose Up

-- sillytavern.us turned out to not actually be a card host — a
-- same-named but unrelated "AI girlfriend" chat site with no
-- downloadable Character Card file at all (Lord ran into this).
-- Dropped from the default. github.com and chub.ai (the current
-- domain for the "characterhub.org" site already whitelisted) added:
-- both can serve a direct, fetchable Character Card file.
UPDATE app_settings
SET png_import_whitelist = TRIM(
    REPLACE(
        REPLACE(
            REPLACE(png_import_whitelist, char(10) || 'sillytavern.us' || char(10), char(10)),
            char(10) || 'sillytavern.us', ''
        ),
        'sillytavern.us' || char(10), ''
    ),
    char(10)
)
WHERE png_import_whitelist LIKE '%sillytavern.us%';

UPDATE app_settings
SET png_import_whitelist = png_import_whitelist || char(10) || 'github.com'
WHERE png_import_whitelist NOT LIKE '%github.com%';

UPDATE app_settings
SET png_import_whitelist = png_import_whitelist || char(10) || 'chub.ai'
WHERE png_import_whitelist NOT LIKE '%chub.ai%';

-- +goose Down

UPDATE app_settings
SET png_import_whitelist = REPLACE(REPLACE(png_import_whitelist, char(10) || 'github.com', ''), char(10) || 'chub.ai', '') || char(10) || 'sillytavern.us';
