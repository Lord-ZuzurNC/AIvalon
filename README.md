# AIvalon

A local, single-user web app for AI roleplay.
It replaces rate-limited hosted apps like OOC: you talk to an LLM that runs on your own machine, through persona cards and lorebooks you control, and nothing leaves your computer.

>**Status: early development.**
>The server, database, and page shell boot and are verified working; the actual features (chat, character/persona/lorebook management, the LLM client) are not built yet.
>This README describes the app AIvalon is becoming, not a finished product — check `docs/DESIGN.md` for what's actually decided versus still open.

## What it does

- **Character cards**, following the Character Card V2/V3 spec (the same format used across the existing SillyTavern-adjacent ecosystem): import one from the web, or make your own.
- **Personas**: your own self-profile as the human player, separate from the characters the AI plays.
- **Lorebooks** (World Info): shared lore for a session, kept separate from character cards on purpose.
- **Chat**, with multiple character cards in one session for group roleplay.
- **Any OpenAI-compatible LLM backend**: point it at Ollama, a llama.cpp server, or a hosted API — AIvalon never embeds inference itself.
- **v2 (later)**: on-demand image generation via an A1111-compatible backend.

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

### Build and run

```sh
git clone https://github.com/Lord-ZuzurNC/AIvalon.git
cd AIvalon
go build -o aivalon ./cmd/aivalon
./aivalon
```

AIvalon listens on `http://127.0.0.1:4140` and is not reachable from the network by default.
Point it at your LLM backend from the Settings page once that page exists — for now, the four sections are an empty shell (see Status above).

## Tech stack (summary)

- **Go**, single static binary, no runtime to install for end users once releases exist.
- **SQLite** (`modernc.org/sqlite`, pure Go, no cgo) for storage; `goose` for migrations.
- **htmx** + server-rendered Go templates (`templ`) for the UI — no separate JavaScript build, no Node/npm dependency.
- **Server-Sent Events** for streaming chat tokens.
- Talks to any OpenAI-compatible `chat/completions` API; never embeds an inference engine itself.

Full reasoning for every choice above — including what was considered and rejected — lives in `docs/DESIGN.md`.

## License

AGPLv3 — see `LICENSE`.
Running AIvalon locally, for yourself, carries no obligation.
Modifying it and running it as a network service for other people means publishing those modifications.
