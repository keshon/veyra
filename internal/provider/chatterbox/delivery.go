package chatterbox

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/keshon/veyra/internal/speech"
)

// This vocabulary matches the pinned Nano checkpoint's added_tokens.json.
var deliveryCues = [...]string{
	"advertisement", "angry", "chuckle", "clear throat", "cough", "crying",
	"dramatic", "fear", "gasp", "groan", "happy", "laugh", "narration",
	"sarcastic", "shush", "sigh", "sniff", "surprised", "whispering",
}

func (s *Synthesizer) DeliveryCues() []string { return append([]string(nil), deliveryCues[:]...) }

//go:embed prompts/delivery.txt
var deliveryPrompt string

func (s *Synthesizer) DeliveryInstructions() string {
	return strings.TrimSpace(deliveryPrompt)
}

func (s *Synthesizer) SynthesizeUtterance(ctx context.Context, u speech.Utterance, language, voice string) ([]byte, error) {
	if s.log != nil && len(u.Cues) > 0 {
		names := make([]string, 0, len(u.Cues))
		for _, cue := range u.Cues {
			names = append(names, cue.Name)
		}
		fmt.Fprintf(s.log, "Delivery cues: %s\n", strings.Join(names, ", "))
	}
	return s.Synthesize(ctx, renderDelivery(u), language, voice)
}

func renderDelivery(u speech.Utterance) string {
	var out strings.Builder
	start := 0
	for _, cue := range u.Cues {
		if cue.Offset < start || cue.Offset > len(u.Text) {
			continue
		}
		supported := false
		for _, name := range deliveryCues {
			if cue.Name == name {
				supported = true
				break
			}
		}
		if !supported {
			continue
		}
		out.WriteString(u.Text[start:cue.Offset])
		out.WriteString("[" + cue.Name + "] ")
		start = cue.Offset
	}
	out.WriteString(u.Text[start:])
	return out.String()
}
