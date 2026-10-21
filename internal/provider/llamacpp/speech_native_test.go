package llamacpp

import (
	"context"
	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/provider/chatterbox"
	"github.com/keshon/veyra/internal/speech"
	"os"
	"strings"
	"testing"
	"time"
)

func TestInstalledStructuredSpeech(t *testing.T) {
	address := os.Getenv("VEYRA_SPEECH_TEST_URL")
	if address == "" {
		t.Skip("installed model opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	s := &chatterbox.Synthesizer{}
	m := []conversation.Message{{Role: "system", Text: `You are Veyra. Return a JSON array of short spoken segments with exactly cues and text. Put performance only in cues. Text is spoken words, never stage directions. Do not announce delivery. Use whispering when asked to whisper. Example: [{"cues":["whispering"],"text":"Here is a secret."}] ` + s.DeliveryInstructions()}, {Role: "user", Text: "Whisper a short secret, then say a normal sentence."}}
	c := NewConfigured(address, Sampling{Temperature: .7, TopP: .9, RepeatPenalty: 1.05, MaxTokens: 256})
	var got []speech.Utterance
	err := c.GenerateSpeech(ctx, m, s.DeliveryCues(), func(u speech.Utterance) error { got = append(got, u); t.Logf("segment %+v", u); return nil })
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range got {
		if strings.ContainsAny(u.Text, "[]*") {
			t.Fatal(u)
		}
		for _, cue := range u.Cues {
			if cue.Name == "whispering" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("whisper request ignored: %+v", got)
	}
}
