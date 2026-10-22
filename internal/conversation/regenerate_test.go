package conversation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRegenerateTruncatesModelContextAndKeepsEarlierExchanges(t *testing.T) {
	var captured Turn
	s := ready(t, &fake{respond: func(_ context.Context, turn Turn, emit func(Event) error) error {
		captured = turn
		return emit(Event{Kind: "text", Text: "reply"})
	}})
	emit := func(Event) error { return nil }
	for _, text := range []string{"one", "two", "three"} {
		if err := s.Respond(context.Background(), Input{Text: text}, emit); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Respond(context.Background(), Input{Regenerate: 2}, emit); err != nil {
		t.Fatal(err)
	}
	if captured.Input.Text != "two" || len(captured.History) != 2 || len(s.history) != 4 {
		t.Fatalf("wrong branch: %+v history %v", captured, s.history)
	}
	if err := s.Respond(context.Background(), Input{Regenerate: 3}, emit); err == nil {
		t.Fatal("discarded exchange accepted")
	}
	if len(s.history) != 4 {
		t.Fatal("rejected request changed context")
	}
}

func TestRegenerateSaveFailureKeepsHistory(t *testing.T) {
	s := ready(t, &fake{respond: func(_ context.Context, _ Turn, emit func(Event) error) error {
		return emit(Event{Kind: "text", Text: "reply"})
	}})
	if err := s.EnableArchive(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	emit := func(Event) error { return nil }
	if err := s.Respond(context.Background(), Input{Text: "one"}, emit); err != nil {
		t.Fatal(err)
	}
	if err := s.Respond(context.Background(), Input{Text: "two"}, emit); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	blocked := filepath.Join(dir, "file")
	if err := os.WriteFile(blocked, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	s.archiveDir = blocked
	if err := s.Respond(context.Background(), Input{Regenerate: 1}, emit); err == nil {
		t.Fatal("expected save failure")
	}
	if len(s.history) != 4 || s.history[2].Text != "two" {
		t.Fatal("rejected regeneration destroyed history")
	}
}
