// Package language defines reply policy independently of recognition and synthesis.
package language

import (
	"fmt"
	"strings"
)

type Preferences struct {
	CustomPrompt string            `json:"custom_prompt,omitempty"`
	PostHistory  string            `json:"-"`
	PromptID     string            `json:"prompt,omitempty"`
	Instruction  string            `json:"-"`
	Reply        string            `json:"reply"` // "auto" follows recognition; a language code pins replies.
	Fallback     string            `json:"fallback"`
	Voices       map[string]string `json:"voices,omitempty"` // Language → provider voice ID.
}

func (p Preferences) Clone() Preferences {
	if p.Voices != nil {
		voices := make(map[string]string, len(p.Voices))
		for k, v := range p.Voices {
			voices[k] = v
		}
		p.Voices = voices
	}
	return p
}

func (p Preferences) Validate(languages []string, automatic bool) error {
	if p.Fallback == "auto" {
		return fmt.Errorf("fallback must be a language code")
	}
	if p.Reply == "auto" && !automatic {
		return fmt.Errorf("this profile does not support automatic reply-language routing")
	}
	for _, code := range []string{p.Reply, p.Fallback} {
		if code == "auto" {
			continue
		}
		found := false
		for _, supported := range languages {
			if code == supported {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unsupported reply language %q", code)
		}
	}
	return nil
}

func (p Preferences) Resolve(detected string) string {
	if p.Reply != "auto" {
		return p.Reply
	}
	if detected != "" {
		return detected
	}
	return p.Fallback
}

func (p Preferences) Prompt(reply string) string {
	name := map[string]string{"en": "English", "ru": "Russian", "zh": "Chinese"}[reply]
	if name == "" {
		name = reply
	}
	instruction := p.Instruction
	if instruction == "" {
		instruction = "You are Veyra, a local voice assistant."
	}
	return strings.Join([]string{instruction, "Reply in " + name + " using one or two short spoken sentences.", "No Markdown or hidden reasoning. /no_think"}, " ")
}
