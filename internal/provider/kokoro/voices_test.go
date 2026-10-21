package kokoro

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnglishVoiceTable(t *testing.T) {
	voices, err := LoadVoices("../../../profiles/kokoro-v1_0-voices.json")
	if err != nil || len(voices) != 28 {
		t.Fatal(len(voices), err)
	}
	for _, tc := range []struct {
		id  string
		sid uint32
	}{{"", 3}, {"af_heart", 3}, {"af_bella", 2}, {"am_michael", 16}, {"bm_lewis", 27}} {
		got, err := speaker(voices, tc.id)
		if err != nil || got != tc.sid {
			t.Fatal(tc, got, err)
		}
	}
	if _, err := speaker(voices, "af_maple"); err == nil {
		t.Fatal("v1.1 voice accepted in v1.0 bank")
	}
}

func TestRejectInvalidVoiceCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "voices.json")
	for _, data := range []string{
		`{"voices":[]}`,
		`{"voices":[{"id":"x","name":"X","language":"en","speaker":-1,"default":true}]}`,
		`{"voices":[{"id":"x","name":"X","language":"en","speaker":0,"default":true},{"id":"y","name":"Y","language":"en","speaker":0}]}`,
		`{"voices":[{"id":"x","name":"X","language":"zh","speaker":0,"default":true}]}`,
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadVoices(path); err == nil {
			t.Fatal("invalid catalog accepted", data)
		}
	}
}

// Synthesizes each installed voice without loading a thinking model or microphone.
func TestInstalledVoices(t *testing.T) {
	if os.Getenv("VEYRA_KOKORO_TEST") != "1" {
		t.Skip("set VEYRA_KOKORO_TEST=1 for installed voice acceptance")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	voices, err := LoadVoices(filepath.Join(root, "profiles/kokoro-v1_0-voices.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s := New(ctx, filepath.Join(root, "bin/kokoro-worker.exe"), filepath.Join(root, "assets/runtimes/sherpa/lib"), filepath.Join(root, "assets/models/kokoro-v1_0"), voices, io.Discard)
	defer s.Close()
	out := filepath.Join(root, "var/voice-samples/kokoro-v1_0")
	if err := os.MkdirAll(out, 0700); err != nil {
		t.Fatal(err)
	}
	for _, voice := range voices {
		wav, err := s.Synthesize(ctx, "Hello. I'm Veyra. Which voice do you prefer?", "en", voice.ID)
		if err != nil {
			t.Fatal(voice.ID, err)
		}
		if len(wav) < 44 || binary.LittleEndian.Uint32(wav[24:]) != 24000 {
			t.Fatal("invalid WAV", voice.ID)
		}
		if err := os.WriteFile(filepath.Join(out, voice.ID+".wav"), wav, 0600); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %.2f seconds", voice.ID, float64(len(wav)-44)/48000)
	}
}
