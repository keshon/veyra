package conversation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/keshon/veyra/internal/language"
)

type fake struct {
	respond    func(context.Context, Turn, func(Event) error) error
	initialize func(context.Context) error
	voices     []Voice
}

func (f *fake) Initialize(c context.Context) error {
	if f.initialize != nil {
		return f.initialize(c)
	}
	return nil
}
func (f *fake) Reset(context.Context) error { return nil }
func (f *fake) Capabilities() Capabilities {
	return Capabilities{Languages: []string{"en", "ru"}, AutomaticLanguage: true, TextInput: true, Voices: f.voices}
}

func TestVoicePreferencesAreValidatedAndCopied(t *testing.T) {
	s := ready(t, &fake{voices: []Voice{{ID: "ru-a", Language: "ru", Default: true}, {ID: "ru-b", Language: "ru"}}, respond: func(c context.Context, turn Turn, emit func(Event) error) error {
		if turn.Preferences.Voices["ru"] != "ru-b" {
			t.Fatal(turn.Preferences)
		}
		turn.Preferences.Voices["ru"] = "ru-a"
		return emit(Event{Kind: "text", Text: "reply"})
	}})
	input := language.Preferences{Reply: "auto", Fallback: "en", Voices: map[string]string{"ru": "ru-b"}}
	if err := s.UpdatePreferences(input); err != nil {
		t.Fatal(err)
	}
	input.Voices["ru"] = "ru-a"
	status := s.Status()
	status.Preferences.Voices["ru"] = "ru-a"
	if err := s.Respond(context.Background(), Input{Text: "hello"}, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Status().Preferences.Voices["ru"] != "ru-b" {
		t.Fatal("voice selection lost")
	}
	for lang, id := range map[string]string{"en": "ru-a", "ru": "unknown"} {
		if err := s.UpdatePreferences(language.Preferences{Reply: "auto", Fallback: "en", Voices: map[string]string{lang: id}}); err == nil {
			t.Fatal("invalid voice accepted")
		}
	}
}
func (f *fake) Respond(c context.Context, t Turn, e func(Event) error) error {
	return f.respond(c, t, e)
}
func ready(t *testing.T, f *fake) *Session {
	t.Helper()
	s, err := New(f, language.Preferences{Reply: "auto", Fallback: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestAdmissionCancellationAndReset(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan error, 1)
	var late func(Event) error
	s := ready(t, &fake{respond: func(c context.Context, turn Turn, e func(Event) error) error {
		late = e
		close(started)
		<-c.Done()
		return c.Err()
	}})
	go func() {
		finished <- s.Respond(context.Background(), Input{Text: "hello"}, func(Event) error { return nil })
	}()
	<-started
	if err := s.Respond(context.Background(), Input{Text: "another"}, func(Event) error { return nil }); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if err := s.UpdatePreferences(language.Preferences{Reply: "ru", Fallback: "en"}); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	s.Cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := late(Event{Kind: "audio", Audio: []byte{1}}); !errors.Is(err, context.Canceled) {
		t.Fatal("late output accepted", err)
	}
	if err := s.UpdatePreferences(language.Preferences{Reply: "ru", Fallback: "en"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Status().Preferences.Reply != "ru" || s.Status().State != "ready" {
		t.Fatal(s.Status())
	}
}
func TestStatusAndCancelDoNotWaitForSink(t *testing.T) {
	blocked := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	s := ready(t, &fake{respond: func(c context.Context, turn Turn, e func(Event) error) error {
		return e(Event{Kind: "text", Text: "hello"})
	}})
	go func() {
		finished <- s.Respond(context.Background(), Input{Text: "hi"}, func(Event) error { close(blocked); <-release; return nil })
	}()
	<-blocked
	snapshot := make(chan struct{})
	go func() { s.Status(); s.Cancel(); close(snapshot) }()
	select {
	case <-snapshot:
	case <-time.After(time.Second):
		t.Fatal("state lock held during delivery")
	}
	close(release)
	<-finished
}
func TestHistoryAndPreferencesAreSnapshots(t *testing.T) {
	count := 0
	s := ready(t, &fake{respond: func(c context.Context, turn Turn, e func(Event) error) error {
		count++
		if count == 2 {
			if len(turn.History) != 2 || turn.History[0].Text != "hello" || turn.Preferences.Reply != "ru" {
				t.Fatal(turn)
			}
			turn.History[0].Text = "mutated"
		}
		if count == 3 && turn.History[0].Text != "hello" {
			t.Fatal("history shared")
		}
		return e(Event{Kind: "text", Text: "reply"})
	}})
	for i := 0; i < 3; i++ {
		if i == 1 {
			s.UpdatePreferences(language.Preferences{Reply: "ru", Fallback: "en"})
		}
		if err := s.Respond(context.Background(), Input{Text: "hello"}, func(Event) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
}
func TestFatalExitCannotBeClearedByInitialization(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	s, _ := New(&fake{initialize: func(context.Context) error { close(entered); <-release; return nil }}, language.Preferences{Reply: "auto", Fallback: "en"})
	go func() { done <- s.Initialize(context.Background()) }()
	<-entered
	fatal := errors.New("owned child exited")
	s.Fail(fatal)
	close(release)
	if err := <-done; !errors.Is(err, fatal) {
		t.Fatal(err)
	}
	if s.Status().State != "error" {
		t.Fatal(s.Status())
	}
	if err := s.Reset(context.Background()); !errors.Is(err, fatal) {
		t.Fatal(err)
	}
}
