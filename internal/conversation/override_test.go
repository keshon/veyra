package conversation

import (
	"context"
	"github.com/keshon/veyra/internal/language"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomPromptOverrideAndPresetRestore(t *testing.T) {
	ctx := context.Background()
	var used Turn
	s := ready(t, &fake{respond: func(_ context.Context, turn Turn, emit func(Event) error) error {
		used = turn
		return emit(Event{Kind: "text", Text: "hello"})
	}})
	path := filepath.Join(t.TempDir(), "prompt.json")
	s.StorePrompts(path)
	if err := s.SetPromptOverride(ctx, "partner", "  Speak gently.  "); err != nil {
		t.Fatal(err)
	}
	if err := s.Respond(ctx, Input{Text: "hi"}, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if used.Preferences.Instruction != "Speak gently." || used.Preferences.PromptID != "partner" {
		t.Fatal(used.Preferences)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "Speak gently.") {
		t.Fatal(string(data), err)
	}
	if err := s.SetPromptOverride(ctx, "partner", "  "); err != nil {
		t.Fatal(err)
	}
	if s.Status().Preferences.CustomPrompt != "" {
		t.Fatal("override not cleared")
	}
	for _, p := range s.Prompts() {
		if p.ID == "partner" && s.Status().Preferences.Instruction != p.Instruction {
			t.Fatal("preset not restored")
		}
	}
	if err := s.SetPromptOverride(ctx, "partner", strings.Repeat("x", 4097)); err == nil {
		t.Fatal("oversize prompt accepted")
	}
}
func TestContextTransferKeepsIdentityAndPrompt(t *testing.T) {
	ctx := context.Background()
	s := ready(t, &fake{respond: func(_ context.Context, turn Turn, emit func(Event) error) error {
		return emit(Event{Kind: "text", Text: "reply"})
	}})
	if err := s.SetPromptOverride(ctx, "partner", "Custom instruction"); err != nil {
		t.Fatal(err)
	}
	if err := s.Respond(ctx, Input{Text: "first"}, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	next, err := New(&fake{}, language.Preferences{Reply: "en", Fallback: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CopyContextTo(next); err != nil {
		t.Fatal(err)
	}
	if len(next.history) != 2 || next.nextID != s.nextID || next.preferences.CustomPrompt != "Custom instruction" {
		t.Fatal("context lost")
	}
	next.history[0].Text = "different"
	if s.history[0].Text == "different" {
		t.Fatal("history aliased")
	}
}
