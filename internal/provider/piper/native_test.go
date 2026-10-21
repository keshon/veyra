package piper_test

import (
	"context"
	"crypto/sha256"
	"github.com/keshon/veyra/internal/config"
	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/provider/piper"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInstalledVoices(t *testing.T) {
	root := os.Getenv("VEYRA_NATIVE_TEST_ROOT")
	if root == "" {
		t.Skip("opt-in native voice acceptance")
	}
	catalog, err := config.LoadVoices(root, "profiles/voices.json")
	if err != nil {
		t.Fatal(err)
	}
	voices := make([]piper.Voice, 0, len(catalog))
	for _, v := range catalog {
		voices = append(voices, piper.Voice{Voice: conversation.Voice{ID: v.ID, Language: v.Language, Name: v.Name, Default: v.Default}, Model: v.Model, Speaker: v.Speaker})
	}
	s := piper.New(filepath.Join(root, "assets/runtimes/piper/piper/piper.exe"), t.TempDir(), voices)
	outputs := make(map[[32]byte]string)
	for _, voice := range voices {
		t.Run(voice.ID, func(t *testing.T) {
			text := "The new interface is ready."
			if voice.Language == "ru" {
				text = "Новый интерфейс готов."
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			data, err := s.Synthesize(ctx, text, voice.Language, voice.ID)
			if err != nil || len(data) < 44 {
				t.Fatal(err, len(data))
			}
			digest := sha256.Sum256(data)
			if previous, exists := outputs[digest]; exists {
				t.Fatalf("%s and %s produced identical audio for the voice selection test", previous, voice.ID)
			}
			outputs[digest] = voice.ID
		})
	}
}
