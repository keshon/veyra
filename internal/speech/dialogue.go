// Package speech coordinates recognition, streaming generation and ordered synthesis.
package speech

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/keshon/veyra/internal/conversation"
)

type Transcript struct{ Text, Language string }
type Recognizer interface {
	Recognize(context.Context, []byte) (Transcript, error)
}
type Generator interface {
	Generate(context.Context, []conversation.Message, func(string) error) error
}

// contextPreparer is optional; tokenizer protocols remain in concrete adapters.
type contextPreparer interface {
	Prepare(context.Context, []conversation.Message) ([]conversation.Message, conversation.ContextUsage, error)
}
type Synthesizer interface {
	Synthesize(context.Context, string, string, string) ([]byte, error)
}

type voiceCatalog interface{ Voices() []conversation.Voice }

type Dialogue struct {
	recognizer   Recognizer
	generator    Generator
	synthesizer  Synthesizer
	profile      string
	capabilities *conversation.Capabilities
}

func New(r Recognizer, g Generator, s Synthesizer) *Dialogue { return NewProfile("composed", r, g, s) }

// NewProfile labels a composition without coupling the dialogue to a model provider.
func NewProfile(profile string, r Recognizer, g Generator, s Synthesizer) *Dialogue {
	return &Dialogue{recognizer: r, generator: g, synthesizer: s, profile: profile}
}
func (d *Dialogue) Capabilities() conversation.Capabilities {
	if d.capabilities != nil {
		return *d.capabilities
	}
	c := conversation.Capabilities{Languages: []string{"en", "ru"}, AutomaticLanguage: true, TextInput: true, Interruptions: true, InterruptionMode: "cancel", Profile: d.profile}
	c.Listening = "Whisper small · whisper.cpp"
	c.Thinking = "Qwen3-1.7B · llama.cpp"
	if d.profile == "minicpm" {
		c.Thinking = "MiniCPM-o 4.5 · llama.cpp text chat"
	}
	c.Speaking = "Piper · installed ONNX voices"
	if catalog, ok := d.synthesizer.(voiceCatalog); ok {
		c.Voices = catalog.Voices()
	}
	return c
}

// NewConfigured uses manifest metadata while keeping model protocols in their adapters.
func NewConfigured(r Recognizer, g Generator, s Synthesizer, c conversation.Capabilities) *Dialogue {
	c.Interruptions = true
	c.InterruptionMode = "cancel"
	if catalog, ok := s.(voiceCatalog); ok {
		c.Voices = catalog.Voices()
	}
	return &Dialogue{recognizer: r, generator: g, synthesizer: s, profile: c.Profile, capabilities: &c}
}
func (d *Dialogue) Initialize(ctx context.Context) error { return ctx.Err() }

// Recover is safe after Respond has joined generation and synthesis.
func (d *Dialogue) Recover(ctx context.Context) error { return ctx.Err() }
func (d *Dialogue) Reset(ctx context.Context) error   { return ctx.Err() }

func (d *Dialogue) Respond(parent context.Context, turn conversation.Turn, emit func(conversation.Event) error) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	text, detected := turn.Input.Text, ""
	if len(turn.Input.Audio) > 0 {
		if err := emit(conversation.Event{Kind: "phase", Phase: "prefill"}); err != nil {
			return err
		}
		transcript, err := d.recognizer.Recognize(ctx, turn.Input.Audio)
		if err != nil {
			return err
		}
		text, detected = transcript.Text, transcript.Language
	} else {
		for _, r := range text {
			if unicode.In(r, unicode.Cyrillic) {
				detected = "ru"
				break
			}
			if unicode.IsLetter(r) {
				detected = "en"
			}
		}
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("no recognized speech")
	}
	if err := emit(conversation.Event{Kind: "transcript", Text: text, Language: detected}); err != nil {
		return err
	}
	if detected == "" {
		detected = turn.PreviousLanguage
	}
	reply := turn.Preferences.Resolve(detected)
	if reply != "en" && reply != "ru" {
		return fmt.Errorf("no voice configured for language %q", reply)
	}
	messages, err := d.prepareContext(ctx, turn, text, reply, emit)
	if err != nil {
		return err
	}
	expressive, hasDelivery := d.synthesizer.(ExpressiveSynthesizer)
	var allowed []string
	if hasDelivery {
		allowed = expressive.DeliveryCues()
	}
	phrases := make(chan Utterance, 2)
	finished := make(chan error, 1)
	go func() {
		for phrase := range phrases {
			var audio []byte
			var err error
			if hasDelivery {
				audio, err = expressive.SynthesizeUtterance(ctx, phrase, reply, turn.Preferences.Voices[reply])
			} else {
				audio, err = d.synthesizer.Synthesize(ctx, phrase.Text, reply, turn.Preferences.Voices[reply])
			}
			if err == nil {
				err = emit(conversation.Event{Kind: "audio", Audio: audio, Language: reply})
			}
			if err != nil {
				cancel()
				finished <- err
				return
			}
		}
		finished <- ctx.Err()
	}()
	queue := func(text string) error {
		utterance := parseUtterance(text, allowed)
		if strings.TrimSpace(utterance.Text) == "" {
			return nil
		}
		select {
		case phrases <- utterance:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	pending := ""
	var delivery deliveryStream
	total := 0
	if err := emit(conversation.Event{Kind: "phase", Phase: "decode"}); err != nil {
		cancel()
		close(phrases)
		<-finished
		return err
	}
	if structured, ok := d.generator.(speechGenerator); ok && hasDelivery {
		err = structured.GenerateSpeech(ctx, messages, allowed, func(u Utterance) error {
			total += len(u.Text)
			if total > 8000 {
				return fmt.Errorf("generated response exceeds text limit")
			}
			if err := emit(conversation.Event{Kind: "text", Text: u.Text + " ", Language: reply}); err != nil {
				return err
			}
			select {
			case phrases <- u:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	} else {
		err = d.generator.Generate(ctx, messages, func(delta string) error {
			total += len(delta)
			if total > 8000 {
				return fmt.Errorf("generated response exceeds text limit")
			}
			pending += delta
			// This supported profile suppresses reasoning upstream; unexpected raw tags fail closed.
			if strings.Contains(pending, "<think>") || strings.Contains(pending, "</think>") {
				return fmt.Errorf("unexpected reasoning markup in response")
			}
			if clean := delivery.push(delta, false); clean != "" {
				if err := emit(conversation.Event{Kind: "text", Text: clean, Language: reply}); err != nil {
					return err
				}
			}
			ready, rest := splitPhrases(pending, false)
			pending = rest
			for _, phrase := range ready {
				if err := queue(phrase); err != nil {
					return err
				}
			}
			return nil
		})
	}
	limited := errors.Is(err, conversation.ErrResponseLimit)
	if limited {
		err = nil
	}
	if err == nil {
		if clean := delivery.push("", true); clean != "" {
			err = emit(conversation.Event{Kind: "text", Text: clean, Language: reply})
		}
	}
	if err == nil {
		ready, _ := splitPhrases(pending, true)
		for _, phrase := range ready {
			if e := queue(phrase); e != nil {
				err = e
				break
			}
		}
	} else {
		cancel()
	}
	close(phrases)
	speechErr := <-finished
	if speechErr != nil {
		if ctx.Err() != nil && speechErr == ctx.Err() && err != nil {
			return err
		}
		return speechErr
	}
	if err != nil {
		return err
	}
	if total == 0 {
		return fmt.Errorf("no generated response")
	}
	if limited {
		if err := emit(conversation.Event{Kind: "warning", Text: "Response reached the token limit; you can ask to continue."}); err != nil {
			return err
		}
	}
	return emit(conversation.Event{Kind: "done"})
}
