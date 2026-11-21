-- +goose Up

-- Named txt2img parameter sets for image generation (see internal/
-- imageclient). style_suffix is appended to the user's prompt
-- (", <suffix>") — it's what actually makes a "manga" preset look
-- different from a "realistic" one, beyond sampler/steps/cfg.
-- Name is unique case-insensitively: saving over an existing name is
-- refused unless the user explicitly asks to overwrite.
CREATE TABLE image_presets (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT NOT NULL UNIQUE COLLATE NOCASE,
    style_suffix     TEXT NOT NULL DEFAULT '',
    negative_prompt  TEXT NOT NULL DEFAULT '',
    steps            INTEGER NOT NULL DEFAULT 20,
    cfg_scale        REAL NOT NULL DEFAULT 7,
    width            INTEGER NOT NULL DEFAULT 512,
    height           INTEGER NOT NULL DEFAULT 512,
    sampler_name     TEXT NOT NULL DEFAULT 'Euler a',
    seed             INTEGER NOT NULL DEFAULT -1,  -- -1 = random
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Starting points, not gospel — ordinary rows the user can edit or
-- delete. Sizes are SD 1.5-friendly (512 base); bump to ~1024 for SDXL.
INSERT INTO image_presets (name, style_suffix, negative_prompt, steps, cfg_scale, width, height, sampler_name, seed) VALUES
(
    'Realistic',
    'photorealistic, RAW photo, 35mm lens, natural lighting, sharp focus, highly detailed skin texture',
    'cartoon, anime, illustration, painting, drawing, 3d render, cgi, lowres, blurry, bad anatomy, extra fingers, deformed, watermark, text, signature',
    30, 6, 512, 768, 'DPM++ 2M Karras', -1
),
(
    'Manga / Anime',
    'anime style, manga illustration, clean lineart, cel shading, vibrant colors, detailed eyes',
    'photorealistic, realistic, photo, 3d, lowres, bad anatomy, bad hands, extra digits, fewer digits, blurry, jpeg artifacts, watermark, text, signature',
    25, 7, 512, 768, 'Euler a', -1
),
(
    'Cyberpunk',
    'cyberpunk, neon lights, rain-soaked city at night, holographic signs, high contrast, cinematic lighting, highly detailed',
    'daylight, pastoral, washed out, lowres, blurry, bad anatomy, deformed, watermark, text, signature',
    28, 7, 768, 512, 'DPM++ 2M Karras', -1
);

-- +goose Down

DROP TABLE image_presets;
