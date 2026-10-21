package speech

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/language"
)

type generateFunc func(context.Context, []conversation.Message, func(string) error) error

func (f generateFunc) Generate(c context.Context, m []conversation.Message, e func(string) error) error {
	return f(c, m, e)
}

type synthFunc func(context.Context, string, string, string) ([]byte, error)

func (f synthFunc) Synthesize(c context.Context, s, l, voice string) ([]byte, error) {
	return f(c, s, l, voice)
}
func TestSpeechStartsBeforeGenerationEndsAndUsesPreference(t *testing.T) {
	spoken := make(chan struct{})
	var phrases []string
	d := New(nil, generateFunc(func(c context.Context, m []conversation.Message, e func(string) error) error {
		if !strings.Contains(m[0].Text, "Russian") {
			t.Fatal(m)
		}
		if err := e("Первое предложение. "); err != nil {
			return err
		}
		select {
		case <-spoken:
		case <-time.After(time.Second):
			return errors.New("TTS waited for generation")
		}
		return e("Второе предложение.")
	}), synthFunc(func(c context.Context, s, l, voice string) ([]byte, error) {
		if voice != "ru-b" {
			t.Fatal("selected voice was not routed", voice)
		}
		if l != "ru" {
			t.Fatal(l)
		}
		phrases = append(phrases, s)
		if len(phrases) == 1 {
			close(spoken)
		}
		return []byte(s), nil
	}))
	turn := conversation.Turn{Input: conversation.Input{Text: "Explain streaming"}, Preferences: language.Preferences{Reply: "ru", Fallback: "en", Voices: map[string]string{"ru": "ru-b"}}}
	if err := d.Respond(context.Background(), turn, func(conversation.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(phrases) != 2 || phrases[0] != "Первое предложение." {
		t.Fatal(phrases)
	}
}
func TestSpeakerFailureCancelsBlockedGenerator(t *testing.T) {
	failed := errors.New("speaker failed")
	generatorStopped := make(chan struct{})
	d := New(nil, generateFunc(func(c context.Context, m []conversation.Message, e func(string) error) error {
		defer close(generatorStopped)
		if err := e("First. "); err != nil {
			return err
		}
		<-c.Done()
		return c.Err()
	}), synthFunc(func(context.Context, string, string, string) ([]byte, error) { return nil, failed }))
	done := make(chan error, 1)
	go func() {
		done <- d.Respond(context.Background(), conversation.Turn{Input: conversation.Input{Text: "hello"}, Preferences: language.Preferences{Reply: "auto", Fallback: "en"}}, func(conversation.Event) error { return nil })
	}()
	select {
	case err := <-done:
		if !errors.Is(err, failed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker deadlock")
	}
	<-generatorStopped
}

func TestResponseLimitWarnsWithoutDisablingConversation(t *testing.T) {
	d := New(nil, generateFunc(func(ctx context.Context, m []conversation.Message, emit func(string) error) error {
		if m[len(m)-1].Text != "Hello" {
			t.Error("provider-specific control text inserted into user message")
		}
		if err := emit("A complete sentence."); err != nil {
			return err
		}
		return conversation.ErrResponseLimit
	}), synthFunc(func(context.Context, string, string, string) ([]byte, error) { return []byte("audio"), nil }))
	session, err := conversation.New(d, language.Preferences{Reply: "en", Fallback: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if err = session.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		warnings, done, audio := 0, 0, 0
		err = session.Respond(context.Background(), conversation.Input{Text: "Hello"}, func(e conversation.Event) error {
			switch e.Kind {
			case "warning":
				warnings++
			case "done":
				done++
			case "audio":
				audio++
			}
			return nil
		})
		if err != nil || session.Status().State != "ready" {
			t.Fatalf("limited turn disabled session: %v / %s", err, session.Status().State)
		}
		if warnings != 1 || done != 1 || audio != 1 {
			t.Fatalf("warning=%d done=%d audio=%d", warnings, done, audio)
		}
	}
}
