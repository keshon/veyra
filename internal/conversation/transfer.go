package conversation

import "fmt"

// CopyContextTo carries composed context across a provider replacement. Callers
// must stop admitting operations on the source until the replacement is complete.
func (s *Session) CopyContextTo(next *Session) error {
	if !s.Capabilities().TextInput || !next.Capabilities().TextInput {
		return fmt.Errorf("native audio state cannot be transferred")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return ErrBusy
	}
	next.mu.Lock()
	defer next.mu.Unlock()
	next.history = append([]Message(nil), s.history...)
	next.summary, next.previousLanguage = s.summary, s.previousLanguage
	next.nextID, next.savedID, next.savedTitle = s.nextID, s.savedID, s.savedTitle
	caps := next.backend.Capabilities()
	if s.preferences.Validate(caps.Languages, caps.AutomaticLanguage) == nil {
		next.preferences.Reply, next.preferences.Fallback = s.preferences.Reply, s.preferences.Fallback
	}
	next.preferences.Voices = map[string]string{}
	for _, voice := range caps.Voices {
		if s.preferences.Voices[voice.Language] == voice.ID {
			next.preferences.Voices[voice.Language] = voice.ID
		}
	}
	next.preferences.PromptID = s.preferences.PromptID
	next.preferences.Instruction, next.preferences.PostHistory = s.preferences.Instruction, s.preferences.PostHistory
	next.preferences.CustomPrompt = s.preferences.CustomPrompt
	return nil
}
