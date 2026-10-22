package config

import (
	"math"
	"testing"
)

func TestPreferencesPersistAndExplicitFlagsWin(t *testing.T) {
	dir := t.TempDir()
	s, err := Load([]string{"-data-dir", dir})
	if err != nil {
		t.Fatal(err)
	}
	s.Temperature = .25
	s.TopK = 17
	s.Seed = 42
	s.User = "Alex"
	if err = s.SavePreferences(); err != nil {
		t.Fatal(err)
	}
	s.Temperature = .4
	if err = s.SavePreferences(); err != nil {
		t.Fatal("replace preferences", err)
	}
	restored, err := Load([]string{"-data-dir", dir, "-temp", "0"})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Temperature != 0 || restored.TopK != 17 || restored.Seed != 42 || restored.User != "Alex" {
		t.Fatalf("precedence: %+v", restored.Preferences())
	}
}
func TestGenerationRejectsInvalidBudgetAndNonfinite(t *testing.T) {
	s, _ := Load([]string{"-data-dir", t.TempDir()})
	g := s.Generation()
	g.MaxTokens = g.Context
	if g.Validate() == nil {
		t.Fatal("accepted invalid budget")
	}
	g = s.Generation()
	g.TopP = math.NaN()
	if g.Validate() == nil {
		t.Fatal("accepted NaN")
	}
}
