package chatterbox

import (
	"github.com/keshon/veyra/internal/speech"
	"testing"
)

func TestRenderDeliveryMapsOnlySupportedCues(t *testing.T) {
	u := speech.Utterance{Text: "Hey you.", Cues: []speech.Cue{{Name: "chuckle", Offset: 4}, {Name: "invented", Offset: 4}, {Name: "laugh", Offset: 99}}}
	if got := renderDelivery(u); got != "Hey [chuckle] you." {
		t.Fatal(got)
	}
}

func TestEveryCheckpointCueIsRendered(t *testing.T) {
	if len(deliveryCues) != 19 {
		t.Fatal("incomplete vocabulary")
	}
	for _, name := range deliveryCues {
		u := speech.Utterance{Text: "Hello.", Cues: []speech.Cue{{Name: name, Offset: 0}}}
		if got := renderDelivery(u); got != "["+name+"] Hello." {
			t.Fatalf("%s: %q", name, got)
		}
	}
	u := speech.Utterance{Text: "Hello again.", Cues: []speech.Cue{{Name: "whispering", Offset: 0}, {Name: "chuckle", Offset: 6}, {Name: "happy", Offset: 6}}}
	if got := renderDelivery(u); got != "[whispering] Hello [chuckle] [happy] again." {
		t.Fatal(got)
	}
}

func TestVariantVoiceCatalog(t *testing.T) {
	for _, variant := range []string{"nano", "turbo"} {
		s := &Synthesizer{variant: variant}
		if voices := s.Voices(); len(voices) != 1 || voices[0].ID != variant {
			t.Fatal(voices)
		}
	}
	if _, err := NewVariant(nil, "", "", t.TempDir(), "unknown", nil); err == nil {
		t.Fatal("unknown variant accepted")
	}
}
