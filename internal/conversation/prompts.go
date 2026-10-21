package conversation

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"unicode/utf8"
)

type Prompt struct {
	PostHistory string `json:"post_history,omitempty"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Instruction string `json:"instruction"`
}

var (
	//go:embed prompts/assistant.txt
	assistantPrompt string
	//go:embed prompts/partner.txt
	partnerPrompt string
	//go:embed prompts/tutor.txt
	tutorPrompt string
)

func DefaultPrompts() []Prompt {
	return []Prompt{
		{ID: "assistant", Name: "Everyday assistant", Instruction: strings.TrimSpace(assistantPrompt)},
		{ID: "partner", Name: "Thinking partner", Instruction: strings.TrimSpace(partnerPrompt)},
		{ID: "tutor", Name: "Patient tutor", Instruction: strings.TrimSpace(tutorPrompt)},
	}
}

type promptSetter interface {
	SetPrompt(context.Context, string) error
}

// SetPrompt changes native system KV state under the same admission lock as turns.
// Composed providers receive the selected instruction in each turn.
func (s *Session) SetPrompt(ctx context.Context, id string) error {
	return s.SetPromptOverride(ctx, id, "")
}

// SetPromptOverride uses custom text when present; the preset remains selected.
func (s *Session) SetPromptOverride(ctx context.Context, id, custom string) error {
	custom = strings.TrimSpace(custom)
	if len(custom) > 4096 || !utf8.ValidString(custom) {
		return fmt.Errorf("custom prompt must be valid UTF-8, at most 4096 bytes")
	}
	var selected *Prompt
	for i := range s.prompts {
		if s.prompts[i].ID == id {
			selected = &s.prompts[i]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("unknown system prompt %q", id)
	}
	if selected.PostHistory != "" && !s.Capabilities().TextInput {
		return fmt.Errorf("this native backend does not support post-history directives")
	}
	instruction, post := selected.Instruction, selected.PostHistory
	if custom != "" {
		instruction, post = custom, ""
	}
	return s.reset(ctx, func(ctx context.Context) error {
		if provider, ok := s.backend.(promptSetter); ok {
			if err := provider.SetPrompt(ctx, instruction); err != nil {
				return err
			}
		} else if err := s.backend.Reset(ctx); err != nil {
			return err
		}
		s.mu.Lock()
		next := s.preferences.Clone()
		next.PromptID, next.Instruction, next.CustomPrompt, next.PostHistory = id, instruction, custom, post
		err := s.storePrompt(next)
		if err == nil {
			s.preferences = next
		}
		s.mu.Unlock()
		if err != nil {
			return err
		}
		return nil
	}, "")
}

func (s *Session) Prompts() []Prompt { return append([]Prompt(nil), s.prompts...) }
