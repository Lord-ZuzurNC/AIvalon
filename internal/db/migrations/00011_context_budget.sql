-- +goose Up

-- Upper bound, in characters (~4 chars per token), on the system prompt
-- plus chat history sent to the LLM per reply — the oldest history
-- messages are left out first once it's exceeded (see internal/web/
-- handlers_chat.go's truncateHistoryToBudget). 0 means no limit.
-- 24000 ≈ 6k tokens: fits an 8k-context backend with room for the reply.
ALTER TABLE app_settings ADD COLUMN context_budget_chars INTEGER NOT NULL DEFAULT 24000;

-- +goose Down

ALTER TABLE app_settings DROP COLUMN context_budget_chars;
