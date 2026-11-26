# Changelog

Newest release first.

## v0.2-beta-1

- **One-shot AIO installer** for Linux (`scripts/install.sh`) and Windows (`scripts/install.ps1`). It asks for an install directory, then downloads and unpacks everything with no manual extraction step: the newest llama.cpp CPU build from llama.cpp's GitHub releases, a bundled low-end default model (Qwen2.5-3B-Instruct, `Q4_K_M` GGUF, ~2 GB), and the AIvalon release binary with its `web/` folder. It writes a launcher (`run-aivalon.sh` / `run-aivalon.ps1`) and offers to create a shortcut at the end (a desktop shortcut on Windows, an application-menu entry on Linux). Re-running it skips the parts that are already installed. On Linux an interrupted model download resumes where it stopped.
- **Built-in llama.cpp sidecar supervisor.** Set `AIVALON_LLAMA_BIN` and `AIVALON_LLAMA_MODELS_DIR` (the installer's launcher sets both) and AIvalon starts `llama-server` itself, in router mode on the models folder, on the port of the "llama.cpp (local)" profile. It logs to `llama-server.log` next to the database and polls `/health` until the server answers `{"status":"ok"}` (60 s timeout). When AIvalon exits it shuts the server down: SIGTERM, a 5 s grace period, then SIGKILL. On Linux this goes to the whole process group, so per-model child processes are stopped too, and if AIvalon crashes the kernel still sends SIGTERM to `llama-server`. If a healthy server is already listening on that port, AIvalon uses it and doesn't start a second one. If the port is taken by something else, or only one of the two variables is set, AIvalon exits with a clear error. Leave both variables unset and AIvalon works exactly as before, with you running your own backend. *Windows caveat:* the Windows side is untested. Only `llama-server` itself gets killed there, so per-model child processes can be left running.
- **Preconfigured default backend profile "llama.cpp (local)"**, added by a database migration. It points at `http://127.0.0.1:9931/v1` with model `Qwen2.5-3B-Instruct-Q4_K_M.gguf`, temperature 1.0 and top P 0.9, which matches what the installer sets up. It is an ordinary profile that you can edit or delete like any other.
- **Loading screen** while the local backend warms up: a plain page with the AIvalon title and an animated "Loading" status (translated, French included) that reloads every second and turns into the app once llama.cpp is healthy. If the backend fails to start, the page shows the reason.

## v.01-beta-1

First release. A local, single-user web app for AI roleplay against any OpenAI-compatible LLM backend, with no inference engine built in.

- **Character cards** (Character Card V2/V3, the SillyTavern-compatible format): import from PNG or JSON, from a file or a URL, or write one by hand. Multiple reference images per card, and any of them can be made the avatar.
- **Personas**: your own profile as the human player, separate from the characters the AI plays.
- **Lorebooks** (World Info): per-entry trigger strategy, insertion order, probability. You can import them on their own, and one embedded in a character card is extracted automatically.
- **Chat**: multiple characters per session, token-by-token streaming (SSE), regenerate/edit/branch/delete per message, session duplication, custom background per session.
- **Backend profiles** for any OpenAI-compatible `chat/completions` endpoint (llama.cpp, Ollama, a hosted API), with a connection test that detects and lists available models.
- **The Vault**: a PIN-gated space (4, 6 or 8 digits, lockout after repeated wrong guesses) for characters, lorebooks and sessions. Everything hidden in it is encrypted at rest (AES-256-GCM).
- **Image generation** against an AUTOMATIC1111/Forge backend, with named presets. The code is complete but it is **disabled by default** in release builds because it hasn't been validated yet.
- **Settings**: backend profiles, personas, system prompt template, context budget, and the hostname whitelist for importing cards from a URL.
- UI language switcher (English and French). Only part of the UI is translated so far.

Known limitations: changing the Vault PIN doesn't re-encrypt files that are already hidden, and personas can't be hidden in the Vault.
