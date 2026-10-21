package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfilePathsAndStrictComposition(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")
	valid := `{"version":1,"name":"custom","mode":"composed","listening":{"provider":"whisper","name":"ASR","binary":"asr.exe","model":"asr.bin"},"thinking":{"provider":"llamacpp","name":"My model","binary":"llm.exe","model":"llm.gguf"},"speaking":{"provider":"piper","name":"TTS","binary":"tts.exe","voices":"voices.json"}}`
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := LoadProfile("unrelated-root", "ignored", path)
	if err != nil || p.Thinking.Model != filepath.Join(dir, "llm.gguf") || p.Speaking.Voices != filepath.Join(dir, "voices.json") {
		t.Fatal(p, err)
	}
	for _, malformed := range []string{strings.Replace(valid, `"provider":"piper"`, `"provider":"minicpm"`, 1), strings.Replace(valid, `"version":1`, `"version":2`, 1), valid + ` {}`, strings.Replace(valid, `"mode":"composed"`, `"mode":"composed","surprise":true`, 1)} {
		os.WriteFile(path, []byte(malformed), 0600)
		if _, err := LoadProfile(dir, "custom", path); err == nil {
			t.Fatal("invalid manifest accepted", malformed)
		}
	}
}

func TestInstalledProfileManifests(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, name := range []string{"kokoro", "chatterbox", "chatterbox-nano", "chatterbox-turbo", "composed", "minicpm", "minicpm-native"} {
		p, err := LoadProfile(root, name, "")
		if err != nil || p.Name != name {
			t.Fatal(p, err)
		}
	}
}

func TestThinkingModelOverrideIsIsolatedAndLabelled(t *testing.T) {
	root := filepath.Join("..", "..")
	s := Settings{Root: root, Profile: "kokoro", ThinkingModel: filepath.Join("assets", "models", "alternative.gguf")}
	p, err := s.LoadProfile()
	if err != nil {
		t.Fatal(err)
	}
	if p.Thinking.Model != filepath.Join(root, s.ThinkingModel) || p.Thinking.Name != "alternative · llama.cpp" {
		t.Fatal(p.Thinking)
	}
	original, err := LoadProfile(root, "kokoro", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Listening != original.Listening || p.Speaking != original.Speaking || original.Thinking.Model == p.Thinking.Model {
		t.Fatal("override changed other providers or manifest")
	}
	s.Profile = "minicpm-native"
	if _, err = s.LoadProfile(); err == nil {
		t.Fatal("native MiniCPM accepted unrelated thinking model")
	}
}

func TestChatterboxVariantSelection(t *testing.T) {
	for _, variant := range []string{"nano", "turbo"} {
		settings, err := Load([]string{"-root", filepath.Join("..", ".."), "-profile", "chatterbox-" + variant})
		if err != nil {
			t.Fatal(err)
		}
		p, err := settings.LoadProfile()
		if err != nil || p.Speaking.Variant != variant || filepath.Base(p.Speaking.Model) != variant {
			t.Fatal(p, err)
		}
	}
}
