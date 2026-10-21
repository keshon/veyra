package speech

import (
	"context"
	"github.com/keshon/veyra/internal/conversation"
	"strings"
)

// Cue describes delivery at a byte offset in the clean spoken text.
type Cue struct {
	Name   string
	Offset int
}
type Utterance struct {
	Text string
	Cues []Cue
}

// ExpressiveSynthesizer advertises only cues its speech model can render.
type ExpressiveSynthesizer interface {
	DeliveryCues() []string
	SynthesizeUtterance(context.Context, Utterance, string, string) ([]byte, error)
}

// speechGenerator keeps performance metadata separate from spoken text.
type speechGenerator interface {
	GenerateSpeech(context.Context, []conversation.Message, []string, func(Utterance) error) error
}

// deliveryAnnotation accepts the protocol and common model-generated stage
// directions. Unrelated brackets (citations, arrays) remain ordinary text.
func deliveryAnnotation(body string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(body))
	if strings.HasPrefix(name, "cue:") || strings.HasPrefix(name, "delivery:") {
		return strings.TrimSpace(strings.SplitN(name, ":", 2)[1]), true
	}
	aliases := map[string]string{
		"chuckles": "chuckle", "laughs": "laugh", "sighs": "sigh",
		"clears throat": "clear throat", "clearing throat": "clear throat",
		"coughs": "cough", "gasps": "gasp", "groans": "groan", "sniffs": "sniff",
		"whispers": "whispering", "whisper": "whispering", "cries": "crying",
		"shushes": "shush", "surprise": "surprised", "fearful": "fear",
	}
	if cue, ok := aliases[name]; ok {
		return cue, true
	}
	for _, cue := range []string{"advertisement", "angry", "chuckle", "clear throat", "cough", "crying", "dramatic", "fear", "gasp", "groan", "happy", "laugh", "narration", "sarcastic", "shush", "sigh", "sniff", "surprised", "whispering"} {
		if name == cue {
			return name, true
		}
	}
	// Physical stage directions cannot be rendered by a speech model.
	for _, action := range []string{"leans in", "leans closer", "smiles", "smiling", "nods", "shrugs", "winks", "pauses", "pause", "takes a breath", "looks away", "looks at you"} {
		if name == action {
			return "", true
		}
	}
	return "", false
}

// deliveryStream buffers brackets across tokens so aliases cannot leak into
// the transcript before the closing bracket determines their meaning.
type deliveryStream struct{ pending string }

func (s *deliveryStream) push(delta string, flush bool) string {
	s.pending += delta
	var out strings.Builder
	for s.pending != "" {
		i := strings.IndexByte(s.pending, '[')
		if i < 0 {
			out.WriteString(s.pending)
			s.pending = ""
			break
		}
		out.WriteString(s.pending[:i])
		s.pending = s.pending[i:]
		end := strings.IndexByte(s.pending, ']')
		if end < 0 {
			if flush || len(s.pending) > 80 {
				if !strings.HasPrefix(s.pending, "[cue:") && !strings.HasPrefix(s.pending, "[delivery:") {
					out.WriteString(s.pending)
				}
				s.pending = ""
			}
			break
		}
		if _, control := deliveryAnnotation(s.pending[1:end]); !control {
			out.WriteString(s.pending[:end+1])
		}
		s.pending = s.pending[end+1:]
	}
	return out.String()
}

func parseUtterance(raw string, allowed []string) Utterance {
	u := Utterance{}
	for raw != "" {
		i := strings.IndexByte(raw, '[')
		if i < 0 {
			u.Text += raw
			break
		}
		u.Text += raw[:i]
		raw = raw[i:]
		end := strings.IndexByte(raw, ']')
		if end < 0 {
			if !strings.HasPrefix(raw, "[cue:") && !strings.HasPrefix(raw, "[delivery:") {
				u.Text += raw
			}
			break
		}
		name, control := deliveryAnnotation(raw[1:end])
		if !control {
			u.Text += raw[:end+1]
		} else {
			for _, supported := range allowed {
				if name == supported {
					u.Cues = append(u.Cues, Cue{Name: name, Offset: len(u.Text)})
					break
				}
			}
		}
		raw = raw[end+1:]
	}
	return u
}
