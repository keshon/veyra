package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// Generation contains the editable text-generation settings shared by consumers.
type Generation struct {
	Context       int     `json:"context"`
	MaxTokens     int     `json:"max_tokens"`
	Temperature   float64 `json:"temperature"`
	TopP          float64 `json:"top_p"`
	TopK          int     `json:"top_k"`
	RepeatPenalty float64 `json:"repeat_penalty"`
	Seed          int     `json:"seed"`
}
type Preferences struct {
	Generation Generation `json:"generation"`
	Character  string     `json:"character"`
	User       string     `json:"user"`
}

func (s Settings) Generation() Generation {
	return Generation{s.Context, s.MaxTokens, s.Temperature, s.TopP, s.TopK, s.RepeatPenalty, s.Seed}
}
func (g Generation) Validate() error {
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	if !finite(g.Temperature) || !finite(g.TopP) || !finite(g.RepeatPenalty) || g.Context < 512 || g.Context > 32768 || g.MaxTokens < 16 || g.MaxTokens > 2048 || g.MaxTokens+64 >= g.Context || g.Temperature < 0 || g.Temperature > 2 || g.TopP <= 0 || g.TopP > 1 || g.TopK < 0 || g.TopK > 1000 || g.RepeatPenalty < 1 || g.RepeatPenalty > 2 || g.Seed < -1 || g.Seed > 2147483647 {
		return fmt.Errorf("invalid generation settings: context 512–32768, reply 16–2048 (below context budget), temperature 0–2, top-p >0–1, top-k 0–1000, repetition penalty 1–2, seed -1–2147483647")
	}
	return nil
}
func (s *Settings) SetGeneration(g Generation) {
	s.Context = g.Context
	s.MaxTokens = g.MaxTokens
	s.Temperature = g.Temperature
	s.TopP = g.TopP
	s.TopK = g.TopK
	s.RepeatPenalty = g.RepeatPenalty
	s.Seed = g.Seed
}
func (s Settings) Preferences() Preferences { return Preferences{s.Generation(), s.Character, s.User} }
func (s Settings) SavePreferences() error {
	if err := os.MkdirAll(s.DataDir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.Preferences(), "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.DataDir, "preferences-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(s.DataDir, "preferences.json"))
}
func (s *Settings) loadPreferences(explicit map[string]bool) error {
	data, err := os.ReadFile(filepath.Join(s.DataDir, "preferences.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var p Preferences
	if err = json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("saved preferences: %w", err)
	}
	if err = p.Generation.Validate(); err != nil {
		return err
	}
	g := s.Generation()
	saved := p.Generation
	if !explicit["ctx"] {
		g.Context = saved.Context
	}
	if !explicit["max-tokens"] {
		g.MaxTokens = saved.MaxTokens
	}
	if !explicit["temp"] {
		g.Temperature = saved.Temperature
	}
	if !explicit["top-p"] {
		g.TopP = saved.TopP
	}
	if !explicit["top-k"] {
		g.TopK = saved.TopK
	}
	if !explicit["repeat-penalty"] {
		g.RepeatPenalty = saved.RepeatPenalty
	}
	if !explicit["seed"] {
		g.Seed = saved.Seed
	}
	s.SetGeneration(g)
	if !explicit["character"] && !explicit["character-png"] {
		s.Character = p.Character
	}
	if !explicit["user"] && strings.TrimSpace(p.User) != "" {
		s.User = p.User
	}
	return nil
}
