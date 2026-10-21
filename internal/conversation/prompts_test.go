package conversation

import (
	"context"
	"errors"
	"github.com/keshon/veyra/internal/language"
	"testing"
)

type promptFake struct {
	fake
	applied string
	failure error
}

func (f *promptFake) SetPrompt(ctx context.Context, instruction string) error {
	if f.failure != nil {
		return f.failure
	}
	f.applied = instruction
	return nil
}
func TestPromptAdmissionAndProviderState(t *testing.T) {
	f := &promptFake{}
	s, err := New(f, language.Preferences{Reply: "en", Fallback: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPrompt(context.Background(), "missing"); err == nil {
		t.Fatal("unknown prompt accepted")
	}
	s.busy = true
	if err := s.SetPrompt(context.Background(), "partner"); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	s.busy = false
	f.failure = errors.New("native failed")
	if err := s.SetPrompt(context.Background(), "partner"); err == nil {
		t.Fatal("failure ignored")
	}
	if s.Status().Preferences.PromptID != "assistant" {
		t.Fatal("failed update published")
	}
	f.failure = nil
	if err := s.SetPrompt(context.Background(), "partner"); err != nil {
		t.Fatal(err)
	}
	if f.applied != s.Status().Preferences.Instruction || s.Status().Preferences.PromptID != "partner" {
		t.Fatal("provider and session differ")
	}
	prefs := s.Status().Preferences
	prefs.Reply = "ru"
	prefs.Instruction = "spoofed"
	if err := s.UpdatePreferences(prefs); err != nil {
		t.Fatal(err)
	}
	if s.Status().Preferences.Instruction == "spoofed" {
		t.Fatal("instruction bypass")
	}
	if err := s.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Status().Preferences.PromptID != "partner" {
		t.Fatal("reset lost prompt")
	}
}
