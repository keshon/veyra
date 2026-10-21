package conversation

import (
	"context"
	"errors"
	"github.com/keshon/veyra/internal/language"
	"testing"
)

type compactFake struct {
	*fake
	compact func(context.Context, Turn) (string, error)
}

func (f compactFake) CompactHistory(ctx context.Context, turn Turn) (string, error) {
	return f.compact(ctx, turn)
}

func TestManualCompactionKeepsRecentExchangesAndCancellationKeepsAll(t *testing.T) {
	started := make(chan struct{})
	blocked := true
	f := compactFake{fake: &fake{respond: func(ctx context.Context, turn Turn, emit func(Event) error) error {
		return emit(Event{Kind: "text", Text: "reply"})
	}}, compact: func(ctx context.Context, turn Turn) (string, error) {
		if len(turn.History) != 4 {
			t.Error("recent history sent to summary", len(turn.History))
		}
		if blocked {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		}
		return "User enjoys ReBoot.", nil
	}}
	s, err := New(f, language.Preferences{Reply: "en", Fallback: "en"})
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	s.EnableArchive(directory)
	s.Initialize(context.Background())
	for i := 0; i < 6; i++ {
		if err := s.Respond(context.Background(), Input{Text: "ReBoot"}, func(Event) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() { _, err := s.Compact(context.Background()); done <- err }()
	<-started
	s.Cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(s.history) != 12 || s.summary != "" || s.Status().State != "ready" {
		t.Fatal("failed compaction changed conversation")
	}
	blocked = false
	saved, err := s.Compact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) != 8 || saved.Summary != "User enjoys ReBoot." {
		t.Fatal(saved)
	}
	snapshot, err := readSaved(directory, saved.ID)
	if err != nil || snapshot.Summary != saved.Summary || len(snapshot.Messages) != 8 {
		t.Fatal(snapshot, err)
	}
}
