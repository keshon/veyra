# Veyra

A Windows-first local voice companion with a Go service, terminal client and
minimal browser client. Recognition, generation and speech run on your machine.

The main composition is Whisper -> llama.cpp -> Chatterbox Nano. Chatterbox Turbo,
Kokoro and Piper are alternatives. Native MiniCPM is experimental.

## Bootstrap a source checkout

Install these tools on PATH:

- Go 1.26 or newer
- Node.js and npm for the browser build
- MinGW GCC for the Kokoro and terminal speech-detector workers
- FFmpeg and FFplay for terminal recording and playback

A clone contains source and the compiled browser UI, but no model weights or
runtime binaries. Bootstrap currently uses an offline asset bundle from an
existing installation. Public asset downloads and prebuilt releases are pending.

On an existing installation, create a bundle outside the checkout:

```cmd
bundle-assets.cmd -Destination "E:\Bundles\veyra-nano" -Profile chatterbox-nano
```

Supported bundle profiles are `chatterbox-nano`, `chatterbox-turbo` and `kokoro`.
The bundle includes the selected model/runtime assets, Sherpa development headers
and Silero VAD weights. It excludes conversations and user preferences.

On a fresh checkout:

```cmd
bootstrap.cmd -Bundle "E:\Bundles\veyra-nano"
build.cmd
check.cmd
start.cmd
```

Bootstrap verifies file hashes before copying. Matching files are reused;
differing existing assets are rejected rather than overwritten. Building compiles
the web UI and native workers, runs Go tests, and produces `bin/veyra.exe`.
`check.cmd` validates configured assets without loading models or opening devices.

Open [the local web client](http://127.0.0.1:8090). Stop Veyra with Ctrl+C.

## Use

```cmd
start.cmd
start.cmd -profile kokoro
start.cmd -profile chatterbox-turbo
terminal.cmd
```

For a terminal-only build, use `build-terminal.cmd`; it uses the included browser
bundle and needs no Node tooling. The application still serves the local HTTP API.

User data defaults to `<user home>/.veyra`, independent of the installation.
Use `-data-dir` to choose another location. Configuration is explicit:
`-config config.example.json` loads a file; command-line flags override it.

Defaults use English replies, a 4096-token context and CPU text generation.
Recognition and Chatterbox use CUDA. GPU memory requirements depend on the chosen
models and other running applications. Set text offload explicitly with `-ngl`;
use `-asr-cpu` for CPU recognition. Kokoro provides CPU speech.

To select another GGUF, including an external model:

```cmd
start.cmd -thinking-model "E:\Models\model.gguf" -ngl 0
```

### Browser

Click the central visual to record, then click again to send. Recording is capped
at 30 seconds. During a response, click to cancel; after generation, click to stop
playback. Hands-free submits speech after a pause and suspends listening during
replies. Use headphones.

Settings includes microphone selection, a local microphone test with playback,
available voices/languages, light/dark appearance, and three visual skins:
Cloud, Lens and Scanner. Skins do not change speech or models. Animation respects
reduced motion unless explicitly overridden.

The custom prompt below the preset selector overrides that preset when nonempty.
Clear it to restore the preset, then Apply prompt. Prompt changes start a new
conversation. The selection and custom text save in `.veyra/prompt.json`;
an explicit launch prompt or character card takes precedence at startup.

**Settings -> Models** changes installed composed backends and generation models
between turns. Old workers unload before replacements start. Conversation context
and prompt survive; a failed load leaves a retry available. Model choices last
until the application closes. Discovery scans nested `assets/models` folders and
the current generation model's folder.

### Terminal

Type `/` to search commands. Arrows select, Enter runs, Tab completes, and Esc
closes the picker. `/mic`, `/voice`, `/prompt`, `/backend`, `/model` and `/sessions`
open selectors. `/settings` opens the settings menu; `/help` lists commands.

- Ctrl+R: record/send
- Ctrl+L: toggle hands-free
- Ctrl+O: stop playback
- Esc: stop a response or discard a recording

Without a selected microphone, recording opens the microphone selector first.
Terminal hands-free uses CPU Silero VAD; `-mic-vad energy` selects the simpler
energy detector. `/prompt custom YOUR TEXT` overrides the preset; `/prompt clear`
restores it. Custom prompts are limited to 4096 UTF-8 bytes.

## Configuration and structure

`profiles/` contains composition manifests: recognition, generation and speech.
Paths resolve relative to the manifest. Use `-profile-file` for a custom manifest.
JSON/PNG character cards are supported through `-character`; the included example
is `profiles/characters/veyra.json`.

Default prompts are embedded `.txt` files. Custom catalogs can specify
`instruction_file` and `post_history_file`, relative to the catalog:

```cmd
start.cmd -prompts profiles/prompts.files.example.json -prompt partner
```

The catalog examples illustrate the format; supply the referenced text files
before using the file-based example.

- `cmd/veyra`: application entry point
- `internal/service`: provider composition and lifecycle
- `internal/conversation`: history, prompts, preferences and turn admission
- `internal/provider`: adapters for local runtimes
- `internal/terminal`, `internal/httpapi`: consumers of the conversation service
- `web/`: Svelte browser client
- `internal/web/dist`: embedded compiled client
- `assets/runtimes`, `assets/models`: ignored local assets
- `scripts/`: explicit build, bundle and bootstrap operations

The HTTP API is loopback-only and serves one live conversation. Turn responses
are NDJSON. `/api/status`, `/api/models`, `/api/prompt`, `/api/preferences`,
`/api/turn`, `/api/text`, `/api/cancel`, `/api/reset` and `/api/voice-reference`
are local interfaces, not a remote or multi-user API.

## Development and limits

```cmd
npm --prefix web ci
npm --prefix web run check
npm --prefix web run build
npm --prefix web run test:ui
go test ./...
go vet ./...
```

Browser tests use a fixture API and synthetic microphone. They do not establish
real microphone, voice quality or GPU acceptance. A live Piper-to-Kokoro switch
has preserved conversation context; Chatterbox Nano hit its startup timeout in
the latest runtime check, and retrying Kokoro recovered successfully.

Native MiniCPM cannot reconstruct its audio state from text history. Changing
its backend requires restarting Veyra. Native full-duplex speech and reference
voice conditioning remain experimental. Portable releases, public asset setup
and asset redistribution/license review remain unfinished. Model and runtime
licenses apply separately from this source checkout.
