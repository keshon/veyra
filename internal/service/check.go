package service

import (
	"fmt"
	"github.com/keshon/veyra/internal/character"
	"github.com/keshon/veyra/internal/config"
	"github.com/keshon/veyra/internal/httpapi"
	"github.com/keshon/veyra/internal/provider/kokoro"
	"os"
	"os/exec"
	"path/filepath"
)

func resolve(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}
func Metadata(s config.Settings) (httpapi.Metadata, error) {
	m := httpapi.Metadata{User: s.User, Generation: map[string]any{"context_tokens": s.Context, "temperature": s.Temperature, "top_k": s.TopK, "seed": s.Seed, "top_p": s.TopP, "repeat_penalty": s.RepeatPenalty, "max_tokens": s.MaxTokens}}
	if s.Character != "" {
		c, err := character.Load(resolve(s.Root, s.Character))
		if err != nil {
			return m, err
		}
		m.Warnings = c.Warnings
		if s.Profile == "minicpm-native" && s.PreferCharacterPostHistory && c.PostHistory != "" {
			return m, fmt.Errorf("native MiniCPM does not support post-history directives; use a composed profile or -character-post-history=false")
		}
		m.Character = c.Name
		m.Introduction = c.Render(c.FirstMessage, s.User, "")
	}
	return m, nil
}

// Check validates the configured installation without loading a model or opening devices.
func Check(s config.Settings) error {
	p, err := s.LoadProfile()
	if err != nil {
		return err
	}
	if _, err = Metadata(s); err != nil {
		return err
	}
	prompts, err := config.LoadPrompts(s.Root, s.Prompts)
	if err != nil {
		return err
	}
	if s.Character != "" {
		for _, p := range prompts {
			if p.ID == "character" {
				return fmt.Errorf("prompt ID character is reserved when loading a character card")
			}
		}
	} else if s.Prompt != "" {
		found := false
		for _, p := range prompts {
			if p.ID == s.Prompt {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unknown system prompt %q", s.Prompt)
		}
	}
	paths := []string{p.Thinking.Binary, p.Thinking.Model}
	if p.Mode == "composed" {
		asr := p.Listening.Binary
		if s.ASRCPU {
			asr = p.Listening.CPUBinary
		}
		paths = append(paths, asr, p.Listening.Model, p.Speaking.Binary)
		if p.Speaking.Provider == "kokoro" {
			if s.Reply != "en" || s.Fallback != "en" {
				return fmt.Errorf("installed Kokoro profile requires English replies and fallback")
			}
			for _, name := range []string{"model.onnx", "voices.bin", "tokens.txt", "espeak-ng-data"} {
				paths = append(paths, filepath.Join(p.Speaking.Model, name))
			}
			for _, name := range []string{"sherpa-onnx-c-api.dll", "onnxruntime.dll", "onnxruntime_providers_shared.dll"} {
				paths = append(paths, filepath.Join(p.Speaking.Library, name))
			}
			voices, e := kokoro.LoadVoices(p.Speaking.Voices)
			if e != nil {
				return e
			}
			found := s.Voice == ""
			for _, v := range voices {
				if v.ID == s.Voice {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("unknown Kokoro voice %q", s.Voice)
			}
		} else if p.Speaking.Provider == "chatterbox" {
			if s.Reply != "en" || s.Fallback != "en" {
				return fmt.Errorf("Chatterbox requires English replies and fallback")
			}
			if s.Voice != "" && s.Voice != p.Speaking.Variant {
				return fmt.Errorf("unknown Chatterbox voice %q", s.Voice)
			}
			for _, name := range []string{"t3_" + p.Speaking.Variant + "_v1.safetensors", "s3gen_meanflow.safetensors", "ve.safetensors", "conds.pt", "tokenizer_config.json", "vocab.json", "merges.txt"} {
				paths = append(paths, filepath.Join(p.Speaking.Model, name))
			}
		} else {
			path := p.Speaking.Voices
			if s.VoiceCatalog != "profiles/voices.json" {
				path = resolve(s.Root, s.VoiceCatalog)
			}
			if _, err = config.LoadVoices(s.Root, path); err != nil {
				return err
			}
		}
	} else {
		if s.Reply != "en" || s.Fallback != "en" {
			return fmt.Errorf("native MiniCPM requires English reply and fallback")
		}
		paths = append(paths, p.Native.ModelDir, p.Native.TTSDir, p.Native.Reference)
		if s.Voice != "" {
			return fmt.Errorf("native MiniCPM has reference conditioning, not named Kokoro voices")
		}
	}
	if s.TUI && s.Live && s.MicVAD == "silero" {
		paths = append(paths, filepath.Join(s.Root, "bin", "vad-worker.exe"), filepath.Join(s.Root, "assets", "models", "vad", "silero_vad.onnx"))
		for _, name := range []string{"sherpa-onnx-c-api.dll", "onnxruntime.dll", "onnxruntime_providers_shared.dll"} {
			paths = append(paths, filepath.Join(s.Root, "assets", "runtimes", "sherpa", "lib", name))
		}
	}
	for _, path := range paths {
		if path == "" {
			return fmt.Errorf("missing configured asset path")
		}
		if _, err = os.Stat(path); err != nil {
			return fmt.Errorf("asset %s: %w", path, err)
		}
	}
	if s.TUI {
		for _, binary := range []string{"ffmpeg", "ffplay"} {
			if _, err = exec.LookPath(binary); err != nil {
				return fmt.Errorf("terminal audio requires %s on PATH: %w", binary, err)
			}
		}
	}
	return nil
}
