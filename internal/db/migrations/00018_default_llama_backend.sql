-- +goose Up

-- Default LLM profile pointing at the bundled local llama.cpp server
-- (the "local AIO backend"). An ordinary row: the user can edit or
-- delete it like any other profile.
INSERT INTO backend_profiles (name, kind, base_url, model, api_key, model_params) VALUES
(
    'llama.cpp (local)',
    'llm',
    'http://127.0.0.1:9931/v1',
    'Qwen2.5-3B-Instruct-Q4_K_M.gguf',
    '',
    '{"temperature":1.0,"top_p":0.9}'
);

-- +goose Down

DELETE FROM backend_profiles WHERE name = 'llama.cpp (local)';
