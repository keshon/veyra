// Package conversation owns a session's history, preferences and admitted turn.
package conversation

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/keshon/veyra/internal/audio"
	"github.com/keshon/veyra/internal/language"
)

var ErrResponseLimit = errors.New("LLM token limit reached; answer incomplete")

var ErrBusy = errors.New("a conversation operation is already running")
var ErrNotReady = errors.New("conversation is not ready")

type Input struct {
	Audio []byte
	Text  string
}
type Message struct {
	PostHistory string `json:"post_history,omitempty"`
	Role        string `json:"role"`
	Text        string `json:"text"`
	Interrupted bool   `json:"interrupted,omitempty"`
}
type Turn struct {
	Summary          string
	ID               uint64
	Input            Input
	Preferences      language.Preferences
	History          []Message
	PreviousLanguage string
}
type Event struct {
	Summary  string
	Context  *ContextUsage
	Kind     string
	Phase    string
	Text     string
	Language string
	Audio    []byte
	TurnID   uint64
	Sequence uint64
}
type Capabilities struct {
	InterruptionMode      string   `json:"interruption_mode,omitempty"`
	Interruptions         bool     `json:"interruptions"`
	FullDuplex            bool     `json:"full_duplex"`
	Languages             []string `json:"languages"`
	AutomaticLanguage     bool     `json:"automatic_language"`
	TextInput             bool     `json:"text_input"`
	Profile               string   `json:"profile"`
	Voices                []Voice  `json:"voices"`
	Listening             string   `json:"listening"`
	Thinking              string   `json:"thinking"`
	Speaking              string   `json:"speaking"`
	ReferenceConditioning bool     `json:"reference_conditioning"`
}

type Voice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Language string `json:"language"`
	Default  bool   `json:"default"`
}

func validatePreferences(p language.Preferences, c Capabilities) error {
	if err := p.Validate(c.Languages, c.AutomaticLanguage); err != nil {
		return err
	}
	for lang, id := range p.Voices {
		found := false
		for _, voice := range c.Voices {
			if voice.Language == lang && voice.ID == id {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("voice %q is not available for %q", id, lang)
		}
	}
	return nil
}

// Backend is defined by its consumer. Concrete providers own protocol state.
type Backend interface {
	Initialize(context.Context) error
	Respond(context.Context, Turn, func(Event) error) error
	Reset(context.Context) error
	Capabilities() Capabilities
}

type Status struct {
	HasHistory     bool                 `json:"has_history"`
	ConversationID string               `json:"conversation_id,omitempty"`
	Context        ContextUsage         `json:"context_usage"`
	State          string               `json:"state"`
	Detail         string               `json:"detail"`
	Preferences    language.Preferences `json:"preferences"`
	ActiveTurn     uint64               `json:"active_turn"`
	Reference      string               `json:"reference"`
}
type Session struct {
	summary                         string
	archiveDir, savedID, savedTitle string
	contextUsage                    ContextUsage
	prompts                         []Prompt
	mu                              sync.Mutex
	backend                         Backend
	state, detail                   string
	preferences                     language.Preferences
	history                         []Message
	previousLanguage                string
	busy                            bool
	nextID, activeID                uint64
	cancel                          context.CancelFunc
	failure                         error // A dead owned process requires an application restart.
	promptPath                      string
	reference                       string
}

func New(backend Backend, prefs language.Preferences) (*Session, error) {
	return NewWithPrompts(backend, prefs, DefaultPrompts())
}
func NewWithPrompts(backend Backend, prefs language.Preferences, prompts []Prompt) (*Session, error) {
	c := backend.Capabilities()
	if err := validatePreferences(prefs, c); err != nil {
		return nil, err
	}
	if len(prompts) == 0 {
		return nil, fmt.Errorf("system prompt catalog is empty")
	}
	if prefs.PromptID == "" {
		prefs.PromptID = prompts[0].ID
	}
	found := false
	for _, p := range prompts {
		if p.ID == prefs.PromptID {
			prefs.Instruction = p.Instruction
			if prefs.CustomPrompt != "" {
				prefs.Instruction = prefs.CustomPrompt
			}
			if p.PostHistory != "" && !c.TextInput {
				return nil, fmt.Errorf("this native backend does not support post-history directives")
			}
			prefs.PostHistory = p.PostHistory
			if prefs.CustomPrompt != "" {
				prefs.PostHistory = ""
			}
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("unknown system prompt %q", prefs.PromptID)
	}
	return &Session{prompts: append([]Prompt(nil), prompts...), backend: backend, state: "loading", detail: "loading voice profile", preferences: prefs.Clone()}, nil
}
func (s *Session) Capabilities() Capabilities {
	c := s.backend.Capabilities()
	c.Languages = append([]string(nil), c.Languages...)
	c.Voices = append([]Voice(nil), c.Voices...)
	return c
}
func (s *Session) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{HasHistory: len(s.history) > 0 || s.summary != "", State: s.state, Detail: s.detail, Preferences: s.preferences.Clone(), ActiveTurn: s.activeID, Reference: s.reference, Context: s.contextUsage, ConversationID: s.savedID}
}
func (s *Session) Initialize(ctx context.Context) error {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return ErrBusy
	}
	s.busy = true
	s.mu.Unlock()
	var err error
	if provider, ok := s.backend.(interface{ ConfigurePrompt(string) }); ok {
		provider.ConfigurePrompt(s.preferences.Instruction)
	}
	err = s.backend.Initialize(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = false
	if s.failure != nil {
		return s.failure
	}
	if err != nil {
		s.state = "error"
		s.detail = err.Error()
	} else {
		s.state = "ready"
		s.detail = "microphone ready"
	}
	return err
}
func (s *Session) Fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = "error"
	s.detail = err.Error()
	s.failure = err
	if s.cancel != nil {
		s.cancel()
	}
}
func (s *Session) UpdatePreferences(p language.Preferences) error {
	c := s.Capabilities()
	if err := validatePreferences(p, c); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return ErrBusy
	}
	if p.PromptID != "" && p.PromptID != s.preferences.PromptID {
		return fmt.Errorf("use the prompt endpoint to change system instructions")
	}
	p.CustomPrompt = s.preferences.CustomPrompt
	p.PromptID = s.preferences.PromptID
	p.Instruction = s.preferences.Instruction
	p.PostHistory = s.preferences.PostHistory
	s.preferences = p.Clone()
	return nil
}
func (s *Session) Cancel() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (s *Session) Reset(ctx context.Context) error {
	return s.reset(ctx, s.backend.Reset, "")
}

// referenceSetter is optional: cloning belongs to providers that support it.
type referenceSetter interface {
	SetReference(context.Context, []byte) error
}

// SetReference replaces the native voice prompt and starts a new conversation.
// An empty recording restores the provider's original reference.
func (s *Session) SetReference(ctx context.Context, recording []byte) error {
	provider, ok := s.backend.(referenceSetter)
	if !ok || !s.Capabilities().ReferenceConditioning {
		return fmt.Errorf("this speech provider does not support reference voice cloning")
	}
	if len(recording) > 0 {
		if err := audio.ValidateReference(recording); err != nil {
			return err
		}
	}
	label := "Original reference"
	if len(recording) > 0 {
		label = "Custom reference"
	}
	return s.reset(ctx, func(ctx context.Context) error { return provider.SetReference(ctx, recording) }, label)
}

func (s *Session) reset(ctx context.Context, operation func(context.Context) error, reference string) error {
	s.mu.Lock()
	if s.failure != nil {
		err := s.failure
		s.mu.Unlock()
		return err
	}
	if s.busy {
		s.mu.Unlock()
		return ErrBusy
	}
	s.busy = true
	s.state = "loading"
	s.detail = "resetting conversation"
	s.mu.Unlock()
	err := operation(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = false
	if s.failure != nil {
		return s.failure
	}
	if err != nil {
		s.state = "error"
		s.detail = err.Error()
		return err
	}
	s.history = nil
	s.summary = ""
	s.savedID, s.savedTitle = "", ""
	s.contextUsage = ContextUsage{}
	if reference != "" {
		s.reference = reference
	}
	s.previousLanguage = ""
	s.state = "ready"
	s.detail = "new conversation"
	return nil
}
func (s *Session) Respond(ctx context.Context, input Input, emit func(Event) error) error {
	if (len(input.Audio) > 0) == (input.Text != "") {
		return fmt.Errorf("provide exactly one audio or text input")
	}
	if len(input.Audio) > 0 {
		if err := audio.ValidateInput(input.Audio); err != nil {
			return err
		}
	}
	if len(input.Text) > 4000 {
		return fmt.Errorf("text input is too long")
	}
	if input.Text != "" && !s.Capabilities().TextInput {
		return fmt.Errorf("this profile requires audio input")
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return ErrBusy
	}
	if s.state != "ready" {
		s.mu.Unlock()
		return ErrNotReady
	}
	s.nextID++
	id := s.nextID
	s.activeID = id
	s.busy = true
	s.state = "busy"
	s.detail = "processing turn"
	turnCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	turn := Turn{ID: id, Input: Input{Audio: append([]byte(nil), input.Audio...), Text: input.Text}, Preferences: s.preferences.Clone(), History: append([]Message(nil), s.history...), PreviousLanguage: s.previousLanguage, Summary: s.summary}
	s.mu.Unlock()
	defer cancel()
	// Delivery is serialized separately from the state mutex. It may apply I/O backpressure.
	var delivery sync.Mutex
	var sequence uint64
	userText, replyText, detected := input.Text, "", ""
	dropped := 0
	err := s.backend.Respond(turnCtx, turn, func(event Event) error {
		delivery.Lock()
		defer delivery.Unlock()
		if err := turnCtx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		current := s.activeID == id
		if current && event.Context != nil {
			if event.Summary != "" {
				s.summary = event.Summary
			}
			s.contextUsage = *event.Context
			dropped = event.Context.DroppedMessages
		}
		s.mu.Unlock()
		if !current {
			return context.Canceled
		}
		sequence++
		event.TurnID = id
		event.Sequence = sequence
		event.Audio = append([]byte(nil), event.Audio...)
		if event.Kind == "transcript" {
			userText = event.Text
			detected = event.Language
		}
		if event.Kind == "text" {
			replyText += event.Text
		}
		return emit(event)
	})
	if err == nil {
		err = turnCtx.Err()
	}
	delivery.Lock()
	defer delivery.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeID != id {
		return context.Canceled
	}
	s.busy = false
	s.activeID = 0
	s.cancel = nil
	if dropped > 0 && dropped <= len(s.history) && dropped%2 == 0 {
		s.history = append([]Message(nil), s.history[dropped:]...)
	}
	if userText != "" && replyText != "" {
		s.history = append(s.history, Message{Role: "user", Text: userText})
		if replyText != "" {
			s.history = append(s.history, Message{Role: "assistant", Text: replyText, Interrupted: err != nil})
		}
	}
	// This is a storage safeguard; the text provider budgets model context.
	byteLimit := 3000
	if s.contextUsage.Limit > 0 {
		byteLimit = 256 * 1024
	}
	chars := 0
	cut := len(s.history)
	for cut > 0 {
		n := len(s.history[cut-1].Text)
		if chars+n > byteLimit || len(s.history)-cut >= 256 {
			break
		}
		chars += n
		cut--
	}
	if cut%2 != 0 {
		cut++
	}
	s.history = append([]Message(nil), s.history[cut:]...)
	if s.failure != nil {
		return s.failure
	}
	if err != nil {
		s.state = "error"
		s.detail = err.Error() + "; reset the conversation before retrying"
		if saveErr := s.saveLocked(); saveErr != nil {
			s.detail += "; save failed: " + saveErr.Error()
		}
		return err
	}
	if detected != "" {
		s.previousLanguage = detected
	}
	s.state = "ready"
	s.detail = "microphone ready"
	if saveErr := s.saveLocked(); saveErr != nil {
		s.detail = "conversation save failed: " + saveErr.Error()
		return fmt.Errorf("%s", s.detail)
	}
	return nil
}
