package chatterbox

import (
	"context"
	"github.com/keshon/veyra/internal/speech"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInstalledDeliverySamples(t *testing.T) {
	root := os.Getenv("VEYRA_DELIVERY_TEST_ROOT")
	if root == "" {
		t.Skip("installed model opt-in")
	}
	directory := filepath.Join(root, "var", "voice-diagnostics", "structured-delivery")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(directory, "chatterbox.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	variant := os.Getenv("VEYRA_DELIVERY_TEST_VARIANT")
	if variant == "" {
		variant = "nano"
	}
	directory = filepath.Join(directory, variant)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := NewVariant(ctx, filepath.Join(root, "assets", "runtimes", "chatterbox", "python.exe"), filepath.Join(root, "assets", "models", "chatterbox", variant), directory, variant, log)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, cue := range []string{"plain", "whispering", "laugh"} {
		u := speech.Utterance{Text: "I know where the treasure is buried."}
		if cue != "plain" {
			u.Cues = []speech.Cue{{Name: cue, Offset: 0}}
		}
		wav, err := s.SynthesizeUtterance(ctx, u, "en", variant)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, cue+".wav"), wav, 0600); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d bytes", cue, len(wav))
	}
}
