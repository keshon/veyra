# Local runtime assets

`runtimes/` groups native executables and accompanying DLLs by runtime. `models/`
groups weights by provider: minicpm, whisper, qwen and piper. These local downloaded
directories are excluded from version control. Native MiniCPM model subdirectories
remain intact because the upstream loader expects that layout.

Composition manifests under `profiles/` select recognition, generation and speech
providers. Paths resolve relative to the manifest; absolute paths are supported.
Use `-profile-file` for a custom composition or `-thinking-model` for a local GGUF.
Chatterbox uses the promoted assets here, not the experimental lab directory.
User files belong in the selected `.veyra` data directory, separately from assets.
Asset packaging scripts live in `scripts/`;
`bundle-assets.cmd` creates a selected offline asset bundle, and `bootstrap.cmd`
verifies and imports it on a fresh checkout. See [bootstrap instructions](../README.md#bootstrap-a-source-checkout).
Public downloads and a prebuilt application release remain pending.

`chatterbox-manifest.json` is a generated inventory of a local promoted installation.
It is ignored by Git; `promote-chatterbox.cmd` regenerates it for its local checker.
