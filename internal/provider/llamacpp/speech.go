package llamacpp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/speech"
)

// GenerateSpeech uses a constrained JSON array and emits each finished segment
// immediately. Delivery metadata never shares a text field with spoken words.
func (c *Client) GenerateSpeech(ctx context.Context, messages []conversation.Message, cues []string, emit func(speech.Utterance) error) error {
	schema := map[string]any{"type": "array", "minItems": 1, "items": map[string]any{
		"type": "object", "properties": map[string]any{
			"cues": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": cues}},
			"text": map[string]any{"type": "string", "minLength": 1, "pattern": `^[- A-Za-z0-9,.;:!?"'()/%&+=@#_вЂ™вЂњвЂќвЂ“вЂ”вЂ¦]+$`},
		}, "required": []string{"cues", "text"}, "additionalProperties": false,
	}}
	format := map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "spoken_reply", "strict": true, "schema": schema}}
	parser := speechJSON{allowed: cues, emit: emit}
	err := c.generate(ctx, messages, format, parser.push)
	if errors.Is(err, conversation.ErrResponseLimit) {
		return err
	}
	if err != nil {
		return err
	}
	if parser.depth != 0 || !json.Valid([]byte(parser.raw)) {
		return fmt.Errorf("incomplete structured speech reply")
	}
	var segments []json.RawMessage
	if err := json.Unmarshal([]byte(parser.raw), &segments); err != nil || len(segments) == 0 {
		return fmt.Errorf("invalid structured speech reply")
	}
	return nil
}

type speechJSON struct {
	raw             string
	start, depth    int
	quoted, escaped bool
	allowed         []string
	emit            func(speech.Utterance) error
}

func (p *speechJSON) push(delta string) error {
	base := len(p.raw)
	p.raw += delta
	if raw := strings.TrimSpace(p.raw); raw != "" && raw[0] != '[' {
		return fmt.Errorf("structured speech must be an array")
	}
	if len(p.raw) > 24000 {
		return fmt.Errorf("structured speech exceeds size limit")
	}
	for i, r := range delta {
		if p.quoted {
			if p.escaped {
				p.escaped = false
				continue
			}
			if r == '\\' {
				p.escaped = true
			} else if r == '"' {
				p.quoted = false
			}
			continue
		}
		if r == '"' {
			p.quoted = true
			continue
		}
		if r == '{' {
			if p.depth == 0 {
				p.start = base + i
			}
			p.depth++
		}
		if r == '}' {
			p.depth--
			if p.depth < 0 {
				return fmt.Errorf("invalid structured speech nesting")
			}
			if p.depth == 0 {
				var segment struct {
					Cues []string `json:"cues"`
					Text string   `json:"text"`
				}
				decoder := json.NewDecoder(bytes.NewReader([]byte(p.raw[p.start : base+i+1])))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&segment); err != nil {
					return err
				}
				if segment.Cues == nil {
					return fmt.Errorf("speech segment lacks cue array")
				}
				if strings.TrimSpace(segment.Text) == "" || strings.ContainsAny(segment.Text, "[]*") || strings.Contains(segment.Text, "<think>") {
					return fmt.Errorf("speech segment contains invalid spoken text")
				}
				u := speech.Utterance{Text: segment.Text}
				for _, name := range segment.Cues {
					valid := false
					for _, allowed := range p.allowed {
						if name == allowed {
							valid = true
							break
						}
					}
					if !valid {
						return fmt.Errorf("unsupported delivery cue %q", name)
					}
					u.Cues = append(u.Cues, speech.Cue{Name: name, Offset: 0})
				}
				if err := p.emit(u); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
