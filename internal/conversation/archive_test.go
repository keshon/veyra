package conversation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func archived(t *testing.T, directory string) *Session {
	t.Helper()
	s := ready(t, &fake{respond: func(ctx context.Context, turn Turn, emit func(Event) error) error {
		return emit(Event{Kind: "text", Text: "Привет. I remember."})
	}})
	if err := s.EnableArchive(directory); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSavedConversationSurvivesRestartAndOverwrite(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	s := archived(t, directory)
	for _, input := range []string{"My name is Alice.", "Remember it."} {
		if err := s.Respond(ctx, Input{Text: input}, func(Event) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.Saved()
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	id := items[0].ID
	fresh := archived(t, directory)
	saved, err := fresh.Resume(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) != 4 || saved.Messages[0].Text != "My name is Alice." || fresh.Status().ConversationID != id {
		t.Fatal(saved)
	}
	if err := fresh.Respond(ctx, Input{Text: "Continue"}, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	snapshot, err := readSaved(directory, id)
	if err != nil || len(snapshot.Messages) != 6 {
		t.Fatal(snapshot, err)
	}
	if err := fresh.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if fresh.Status().ConversationID != "" {
		t.Fatal("new conversation retained saved ID")
	}
	if err := fresh.Respond(ctx, Input{Text: "Another chat"}, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	items, err = fresh.Saved()
	if err != nil || len(items) != 2 {
		t.Fatal(items, err)
	}
	if _, err := fresh.Resume(ctx, id); err != nil {
		t.Fatal("reset deleted previous chat", err)
	}
}

func TestResumeRejectsBusyInvalidAndDifferentInstructions(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	s := archived(t, directory)
	s.Respond(ctx, Input{Text: "hello"}, func(Event) error { return nil })
	id := s.Status().ConversationID
	if _, err := s.Resume(ctx, "../escape"); err == nil {
		t.Fatal("path traversal accepted")
	}
	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()
	if _, err := s.Resume(ctx, id); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.busy = false
	s.preferences.Instruction = "different character"
	s.mu.Unlock()
	if _, err := s.Resume(ctx, id); err == nil {
		t.Fatal("character mismatch accepted")
	}
}

func TestInterruptedAndFailedSaveAreVisible(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	s := ready(t, &fake{respond: func(ctx context.Context, turn Turn, emit func(Event) error) error {
		emit(Event{Kind: "text", Text: "unfinished"})
		return context.Canceled
	}})
	if err := s.EnableArchive(directory); err != nil {
		t.Fatal(err)
	}
	if err := s.Respond(ctx, Input{Text: "story"}, func(Event) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	snapshot, err := readSaved(directory, s.Status().ConversationID)
	if err != nil || !snapshot.Messages[1].Interrupted {
		t.Fatal(snapshot, err)
	}
	broken := archived(t, filepath.Join(directory, "blocked"))
	if err := os.Remove(filepath.Join(directory, "blocked")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "blocked"), []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := broken.Respond(ctx, Input{Text: "hi"}, func(Event) error { return nil }); err == nil {
		t.Fatal("failed save silently accepted")
	}
}

func TestSummarySurvivesResumeAndReset(t *testing.T) {
	directory := t.TempDir()
	s := ready(t, &fake{respond: func(ctx context.Context, turn Turn, emit func(Event) error) error {
		if err := emit(Event{Kind: "context", Context: &ContextUsage{Limit: 4096}, Summary: "User is Alice."}); err != nil {
			return err
		}
		return emit(Event{Kind: "text", Text: "Hello"})
	}})
	if err := s.EnableArchive(directory); err != nil {
		t.Fatal(err)
	}
	if err := s.Respond(context.Background(), Input{Text: "Hi"}, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	fresh := archived(t, directory)
	saved, err := fresh.Resume(context.Background(), s.Status().ConversationID)
	if err != nil || saved.Summary != "User is Alice." || fresh.summary != saved.Summary {
		t.Fatal(saved, err)
	}
	if err := fresh.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fresh.summary != "" {
		t.Fatal("summary survived new conversation")
	}
}

func TestArchivePreservesAndChecksPostHistory(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := archived(t, dir)
	s.preferences.PostHistory = "Stay in character."
	if err := s.Respond(ctx, Input{Text: "Hello"}, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	items, err := s.Saved()
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	other := archived(t, dir)
	if _, err := other.Resume(ctx, items[0].ID); err == nil {
		t.Fatal("different post-history resumed")
	}
	other.preferences.PostHistory = "Stay in character."
	saved, err := other.Resume(ctx, items[0].ID)
	if err != nil || saved.PostHistory != "Stay in character." || other.preferences.PostHistory != saved.PostHistory {
		t.Fatal(saved, err)
	}
}
