# AIvalon 0.1

First release. A local, single-user web app for AI roleplay: character cards, personas, lorebooks, and chat against any OpenAI-compatible LLM backend (llama.cpp, Ollama, a hosted API) — nothing leaves your machine, no inference engine embedded.

See `README.md` (🇫🇷 `README_fr.md`) for the full getting-started walkthrough with screenshots — installing a local LLM, picking a model for your GPU, importing a character card, and starting your first chat.

## What's in this release

- **Character cards** (Character Card V2/V3, the SillyTavern-compatible format): import from PNG or JSON, from a file or a URL, or write one by hand. Multiple reference images per card.
- **Personas**: your own profile as the human player, separate from the characters the AI plays.
- **Lorebooks** (World Info): per-entry trigger strategy, insertion order, probability — importable standalone or auto-extracted from a character card that embeds one.
- **Chat**: multiple characters per session for group roleplay, token-by-token streaming, regenerate/edit/branch/delete per message, session duplication, custom background per session.
- **Backend profiles**: point at any OpenAI-compatible `chat/completions` endpoint, with a one-click connection test that detects and lists available models.
- **The Vault**: a PIN-gated space (4, 6, or 8 digits) to pull a character, lorebook, or whole session out of casual view, with a lockout after repeated wrong guesses. Everything hidden in it — portraits, covers, session images — is **encrypted at rest** (AES-256-GCM), not just hidden behind a UI gate.
- **Settings**: the system prompt template, a context budget (character cap per reply, with a visible warning when a chat gets truncated), and a hostname whitelist for importing cards from a URL.

## Known limitations

- **Image generation is disabled by default in this release.** The code path against an AUTOMATIC1111/Forge backend exists (presets, prompt suggestion via the LLM, VRAM unload after generation) but hasn't had a real validation pass yet — treat it as work in progress if you turn it on.
- Changing the Vault's PIN doesn't re-encrypt files that were already hidden under the previous one.
- Personas can't be hidden in the Vault (only chats, characters, and worlds can).

## Installing

Download the archive for your OS below, extract it anywhere, and run the executable inside — no installer, no separate runtime. You'll need a local LLM backend running first (llama.cpp or Ollama); see the README's Getting Started section for the exact commands and model picks per GPU tier.

- **Linux (x86_64)**: `aivalon_0.1_linux_amd64.tar.gz`
- **Windows (x86_64)**: `aivalon_0.1_windows_amd64.zip`

Each archive includes a `README.txt` (English) and `LISEZ-MOI.txt` (French) with the platform-specific notes (keeping the `web/` folder alongside the binary, Windows SmartScreen on an unsigned executable).

## License

AGPLv3 — see `LICENSE`. Running it locally for yourself carries no obligation; modifying it and running it as a network service for others means publishing those modifications.
