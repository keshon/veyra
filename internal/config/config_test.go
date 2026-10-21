package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFileThenFlagOverrides(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"reply":"ru","gpu_layers":12}`), 0600)
	s, err := Load([]string{"-config", p, "-ngl", "6"})
	if err != nil || s.Reply != "ru" || s.GPULayers != 6 {
		t.Fatal(s, err)
	}
}
func TestUserDataDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load([]string{"-root", t.TempDir()})
	if err != nil || s.DataDir != filepath.Join(home, ".veyra") {
		t.Fatalf("default data directory: %q, %v", s.DataDir, err)
	}
	// Loading configuration must not create directories or require installed models.
	dir := filepath.Join(t.TempDir(), "Documents", "Личные данные", ".veyra")
	p := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(map[string]string{"data_dir": dir})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err = Load([]string{"-config", p})
	if err != nil || s.DataDir != dir {
		t.Fatalf("configured data directory: %q, %v", s.DataDir, err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("configuration created data directory: %v", err)
	}
	override := filepath.Join(t.TempDir(), ".veyra")
	s, err = Load([]string{"-config", p, "-data-dir", override})
	if err != nil || s.DataDir != override {
		t.Fatalf("data directory override: %q, %v", s.DataDir, err)
	}
	want, err := filepath.Abs("portable/.veyra")
	if err != nil {
		t.Fatal(err)
	}
	s, err = Load([]string{"-data-dir", "portable/.veyra"})
	if err != nil || s.DataDir != want {
		t.Fatalf("relative data directory: %q, %v", s.DataDir, err)
	}
}

func TestRejectInvalidSettings(t *testing.T) {
	for _, args := range [][]string{{"-profile", "unknown"}, {"-listen", "0.0.0.0:8090"}, {"-ctx", "0"}, {"-profile", "composed", "-asr-port", "19081"}, {"-model-port", "8090"}} {
		if _, err := Load(args); err == nil {
			t.Fatal(args)
		}
	}
}

func TestSharedProfilesRequireSeparateRecognitionPort(t *testing.T) {
	for _, profile := range []string{"composed", "minicpm"} {
		if _, err := Load([]string{"-profile", profile, "-asr-port", "19081"}); err == nil {
			t.Fatalf("%s allowed shared native ports", profile)
		}
	}
	if _, err := Load([]string{"-profile", "minicpm-native", "-reply", "en", "-asr-port", "19081"}); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultVoiceProfile(t *testing.T) {
	s, err := Load(nil)
	if err != nil || s.Profile != "chatterbox-nano" || s.Reply != "en" || s.Context != 4096 || s.GPULayers != 0 || s.Microphone != "" {
		t.Fatal(s, err)
	}
}

func TestTerminalSettingsAndSampling(t *testing.T) {
	s, err := Load([]string{"-profile", "kokoro", "-tui", "-temp", "0", "-top-p", "0.8", "-max-tokens", "320", "-character", "profiles/characters/veyra.json"})
	if err != nil || !s.TUI || s.Temperature != 0 || s.TopP != 0.8 || s.MaxTokens != 320 {
		t.Fatal(s, err)
	}
	for _, args := range [][]string{{"-temp", "NaN"}, {"-temp", "3"}, {"-top-p", "0"}, {"-repeat-penalty", "0.5"}, {"-max-tokens", "0"}} {
		if _, err := Load(args); err == nil {
			t.Fatal("invalid settings", args)
		}
	}
}

func TestMicrophoneGateSettings(t *testing.T) {
	defaults, err := Load(nil)
	if err != nil || defaults.MicVAD != "silero" {
		t.Fatal(defaults, err)
	}
	fallback, err := Load([]string{"-mic-vad", "energy"})
	if err != nil || fallback.MicVAD != "energy" {
		t.Fatal(fallback, err)
	}
	if _, err := Load([]string{"-mic-vad", "unknown"}); err == nil {
		t.Fatal("invalid detector accepted")
	}
	s, err := Load([]string{"-mic-threshold", "0.025", "-mic-start-ms", "350"})
	if err != nil || s.MicThreshold != .025 || s.MicStartMS != 350 {
		t.Fatal(s, err)
	}
	for _, args := range [][]string{{"-mic-threshold", "NaN"}, {"-mic-threshold", "0"}, {"-mic-start-ms", "900"}} {
		if _, err = Load(args); err == nil {
			t.Fatal("invalid microphone setting accepted", args)
		}
	}
}

func TestMicrophoneEndpointSettings(t *testing.T) {
	s, err := Load([]string{"-profile", "chatterbox", "-mic-end-ms", "2200", "-mic-max-seconds", "90"})
	if err != nil || s.MicEndMS != 2200 || s.MicMaxSeconds != 90 {
		t.Fatal(s, err)
	}
	for _, args := range [][]string{{"-mic-end-ms", "0"}, {"-mic-end-ms", "6000"}, {"-mic-max-seconds", "121"}} {
		if _, err := Load(args); err == nil {
			t.Fatal(args)
		}
	}
}

func TestPNGCharacterAliasOverridesWrapperDefault(t *testing.T) {
	path := `D:\Cards\My Character.png`
	s, err := Load([]string{"-character", "profiles/characters/veyra.json", "-character-png", path})
	if err != nil || s.Character != path {
		t.Fatal(s, err)
	}
}

func TestCharacterPromptPreferences(t *testing.T) {
	s, err := Load([]string{"-character-main-prompt=false", "-character-post-history=false"})
	if err != nil || s.PreferCharacterPrompt || s.PreferCharacterPostHistory {
		t.Fatal(s, err)
	}
	s, err = Load(nil)
	if err != nil || !s.PreferCharacterPrompt || !s.PreferCharacterPostHistory {
		t.Fatal("character prompt defaults", s, err)
	}
}
