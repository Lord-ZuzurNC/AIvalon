# AIvalon

A local, single-user web app for AI roleplay.
It replaces rate-limited hosted apps like OOC: you talk to an LLM that runs on your own machine, through persona cards and lorebooks you control, and nothing leaves your computer.

> **Status: pre-release, no empty shells left.**
> Chat, character cards, personas, lorebooks, backend profiles, and the Vault work end-to-end against a real LLM backend.
> Image generation (A1111 backend, presets) is code-complete but **not yet validated** — no real test pass against a live A1111 instance — treat it as work in progress.
> No binary release exists yet; building from source is the only install path. `docs/DESIGN.md` is still the source of truth for what's settled versus still open.

## What it does

- **Character cards**, following the Character Card V2/V3 spec (the same format used across the existing SillyTavern-adjacent ecosystem): import from a PNG (with the card embedded in a `tEXt` chunk) or plain JSON, from a file or a URL, or write one by hand. Multiple reference images per card, with one promotable to primary/avatar.
- **Personas**: your own self-profile as the human player, separate from the characters the AI plays. Optional — a session with none picked falls back to a built-in default.
- **Lorebooks** (World Info): shared lore kept separate from character cards on purpose, with per-entry trigger strategy (`constant` or whole-word `selective`), insertion order, and probability. Importable the same way as character cards.
- **Chat**, with multiple character cards in one session for group roleplay: streamed token-by-token over SSE, with per-message regenerate, edit, branch (fork the conversation from any point), and delete, plus session duplication and a custom background per session.
- **Any OpenAI-compatible LLM backend**: point one or more backend profiles at Ollama, a llama.cpp server, or a hosted API, with a connection test/ping from Settings — AIvalon never embeds inference itself.
- **On-demand image generation** *(work in progress, not yet validated)*, against an AUTOMATIC1111 (A1111) Stable Diffusion WebUI backend. Named image presets (steps, CFG scale, sampler, size, seed, negative prompt, style suffix) are managed once in Settings — three built-ins ship (Realistic, Manga/Anime, Cyberpunk) — and a session just picks one; the in-chat dialog only ever asks for the prompt. The code path exists but hasn't had a real pass against a live A1111 instance yet.
- **The Vault**: a PIN-gated space to pull a character card, lorebook, or session out of casual view. Four-digit PIN, re-prompted every time the Vault is opened, with a lockout after repeated wrong guesses to keep brute-forcing it slow.
- **Settings**: backend profiles, personas, the system prompt template, a context budget (character cap on what's sent per reply), and the hostname whitelist for "import a card from a URL".

Full rationale for every one of these choices is in `docs/DESIGN.md`.

## Installing (for users)

No pre-built release exists yet, so building from source is currently the only way to run it.

### Prerequisites

1. **Go 1.22 or newer**, only to build the binary — [go.dev/dl](https://go.dev/dl/).
2. **A local LLM backend.** Pick one:
   - [Ollama](https://ollama.com/) — simplest to set up, has an OpenAI-compatible API built in.
   - [llama.cpp](https://github.com/ggml-org/llama.cpp)'s `llama-server` — more control over quantization and GPU offload.
   - Any other server exposing an OpenAI-compatible `chat/completions` endpoint (including a paid hosted API, if you'd rather not run a model locally at all).
3. **A model**, in GGUF format for Ollama/llama.cpp. Pick the tier that matches your GPU's VRAM:

   | Tier            | Model                                                                                                           | VRAM (approx.)     | Notes                                                                                                                           |
   | --------------- | --------------------------------------------------------------------------------------------------------------- | ------------------ | ------------------------------------------------------------------------------------------------------------------------------- |
   | Low-end (~3B)   | [Qwen2.5-3B-Instruct-GGUF](https://huggingface.co/bartowski/Qwen2.5-3B-Instruct-GGUF)                           | ~2 GB at Q4_K_M    | General-purpose, not roleplay-tuned specifically, but the strongest all-rounder this small. Runs on almost anything with a GPU. |
   | Mid-end (~8B)   | [Huihui-Qwen3-8B-abliterated-v2](https://huggingface.co/huihui-ai/Huihui-Qwen3-8B-abliterated-v2)               | ~5 GB at Q4_K_M    | Good character-card adherence, fits comfortably on an 8 GB card.                                                                |
   | Mid-end (~12B)  | [Huihui-gemma-4-12B-it-abliterated](https://huggingface.co/mradermacher/Huihui-gemma-4-12B-it-abliterated-GGUF) | ~9-10 GB at Q4/Q5  | A step up if you have 12 GB (e.g. an RTX 4070 Ti), noticeably more coherent over long scenes.                                   |
   | High-end (~24B) | [Dolphin3.0-Mistral-24B](https://huggingface.co/dphn/Dolphin3.0-Mistral-24B)                                    | ~16-20 GB at Q4-Q6 | Dense 24B, the established pick for long-form creative/roleplay output once you have the VRAM for it.                           |

   "Abliterated" means the model's refusal/alignment layer has been removed — the usual reason a model is listed here instead of its plain instruct version is that standard instruct models tend to break character and insert safety disclaimers mid-scene. Swap in the non-abliterated GGUF for the same base model if you'd rather keep that layer; AIvalon doesn't care which you point it at.
4. **(Optional, WIP feature) An A1111 instance**, only needed for the image generation feature — any AUTOMATIC1111-compatible Stable Diffusion WebUI reachable over HTTP, local or remote. Not yet validated end-to-end; see Status above.

### Build and run

```sh
git clone https://github.com/Lord-ZuzurNC/AIvalon.git
cd AIvalon
go build -o aivalon ./cmd/aivalon
./aivalon
```

AIvalon listens on `http://127.0.0.1:4140` and is not reachable from the network by default (`AIVALON_ADDR` overrides this for same-LAN access — see `docs/DESIGN.md`).
First run: open Settings, add a backend profile pointing at your LLM server, then create a Persona and a Character and start a chat.

### Environment variables

| Variable       | Default          | Purpose                                                                                                                       |
| -------------- | ---------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| `AIVALON_ADDR` | `127.0.0.1:4140` | Listen address. Setting a non-loopback address relaxes the DNS-rebinding guard for private-LAN Host headers on the same port. |
| `AIVALON_DB`   | `aivalon.db`     | SQLite file path, so two instances don't fight over the same database.                                                        |

## Tech stack (summary)

- **Go**, single static binary, no runtime to install for end users once releases exist.
- **SQLite** (`modernc.org/sqlite`, pure Go, no cgo) for storage; `goose` for migrations.
- **htmx** + server-rendered Go templates (`templ`) for the UI — no separate JavaScript build, no Node/npm dependency.
- **Server-Sent Events** for streaming chat tokens.
- Talks to any OpenAI-compatible `chat/completions` API and to an A1111-compatible `/sdapi/v1/txt2img` endpoint; never embeds an inference or image-generation engine itself.

Full reasoning for every choice above — including what was considered and rejected — lives in `docs/DESIGN.md`.

## Development

```sh
go install github.com/a-h/templ/cmd/templ@latest   # regenerate .templ -> _templ.go after editing a template
go install github.com/pressly/goose/v3/cmd/goose@latest   # add/run DB migrations

templ generate && go build -o aivalon ./cmd/aivalon
go test ./...
```

Migrations live as plain `.sql` files in `internal/db/migrations/`; add a new one rather than editing an existing migration. Generated `_templ.go` files are committed, so a bare `go build` works without the `templ` CLI installed — only editing a `.templ` file requires it.

## License

AGPLv3 — see `LICENSE`.
Running AIvalon locally, for yourself, carries no obligation.
Modifying it and running it as a network service for other people means publishing those modifications.
</content>
