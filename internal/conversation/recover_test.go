package conversation

import (
	"context"
	"errors"
	"testing"

	"github.com/keshon/veyra/internal/language"
)

type recoverFake struct{ *fake }

func (f *recoverFake) Recover(ctx context.Context) error { return ctx.Err() }
func TestRecoveryPreservesContextAndMarksInterrupted(t *testing.T) {
	entered := make(chan struct{})
	backend := &recoverFake{&fake{respond: func(ctx context.Context, turn Turn, emit func(Event) error) error {
		emit(Event{Kind: "text", Text: "An incomplete answer"})
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}}}
	s, err := New(backend, language.Preferences{Reply: "auto", Fallback: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- s.Respond(context.Background(), Input{Text: "Original question"}, func(Event) error { return nil })
	}()
	<-entered
	if err := s.Recover(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatal("recovered while workers were active", err)
	}
	s.Cancel()
	<-done
	if err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Status().State != "ready" || len(s.history) != 2 || s.history[0].Text != "Original question" || !s.history[1].Interrupted {
		t.Fatal("recovery erased or misrepresented context")
	}
}
func TestStatefulBackendCannotInheritRecovery(t *testing.T) {
	s := ready(t, &fake{})
	if err := s.Recover(context.Background()); err == nil {
		t.Fatal("backend without recovery was admitted")
	}
	s.Fail(errors.New("child exited"))
	if err := s.Recover(context.Background()); err == nil {
		t.Fatal("failure was cleared")
	}
}

func TestMarkInterruptedRejectsStalePlayback(t *testing.T) {
	s := ready(t, &fake{})
	s.nextID = 2
	s.history = []Message{{Role: "user", Text: "Hi"}, {Role: "assistant", Text: "Hello"}}
	if err := s.MarkInterrupted(1); !errors.Is(err, ErrBusy) {
		t.Fatalf("stale callback: %v", err)
	}
	if s.history[1].Interrupted {
		t.Fatal("stale playback changed history")
	}
	if err := s.MarkInterrupted(2); err != nil {
		t.Fatal(err)
	}
	if !s.history[1].Interrupted {
		t.Fatal("missing interruption marker")
	}
}
