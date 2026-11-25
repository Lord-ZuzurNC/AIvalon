-- +goose Up

-- UI / system-prompt language, by code ("en", "fr", ...) — the code
-- matches a web/static/i18n/<code>.json filename stem (internal/i18n).
-- Mirrors the browser's own "aivalon-lang" cookie but lives here too:
-- the system prompt (what the LLM backend sees, nothing to do with
-- any one browser) needs a single server-side source of truth. "en"
-- default so an existing install with no cookie yet renders exactly
-- as before.
ALTER TABLE app_settings ADD COLUMN language TEXT NOT NULL DEFAULT 'en';

-- +goose Down

ALTER TABLE app_settings DROP COLUMN language;
