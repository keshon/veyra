package service

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/keshon/veyra/internal/config"
)

type modelChoice struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Unavailable  string `json:"unavailable,omitempty"`
	Mode         string `json:"mode,omitempty"`
	RequiresNew  bool   `json:"requires_new,omitempty"`
	Experimental bool   `json:"experimental,omitempty"`
}
type modelCatalog struct {
	Profile    string        `json:"profile"`
	Model      string        `json:"model"`
	Profiles   []modelChoice `json:"profiles"`
	Models     []modelChoice `json:"models"`
	Switchable bool          `json:"switchable"`
	CanRestore bool          `json:"can_restore"`
}

// modelCatalogFor lists installed manifests and GGUF files, including nested
// model directories. Paths come from the installation or current explicit model.
func modelCatalogFor(s config.Settings) modelCatalog {
	c := modelCatalog{Profile: s.Profile, Model: s.ThinkingModel, Profiles: []modelChoice{}, Models: []modelChoice{}}
	current, err := s.LoadProfile()
	if err != nil {
		return c
	}
	c.Switchable = true
	c.Model = current.Thinking.Model
	seen := map[string]bool{}
	add := func(path string) {
		path = filepath.Clean(path)
		if seen[path] {
			return
		}
		seen[path] = true
		if config.CheckChatModel(path) == nil {
			c.Models = append(c.Models, modelChoice{ID: path, Name: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))})
		}
	}
	if current.Mode == "composed" {
		add(c.Model)
	}
	roots := []string{filepath.Join(s.Root, "assets", "models"), filepath.Dir(c.Model)}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".gguf") {
				add(path)
			}
			return nil
		})
	}
	entries, _ := filepath.Glob(filepath.Join(s.Root, "profiles", "*.json"))
	if s.ProfileFile == "" {
		entries = append([]string{filepath.Join(s.Root, "profiles", s.Profile+".json")}, entries...)
	}
	seenProfiles := map[string]bool{}
	for _, path := range entries {
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		candidate := s
		candidate.Profile, candidate.ProfileFile = id, ""
		candidate.ThinkingModel, candidate.Voice = "", ""
		candidate.Reply, candidate.Fallback = "en", "en"
		p, err := candidate.LoadProfile()
		if err != nil {
			continue
		}
		key := fmt.Sprintf("%s|%s|%s|%s|%s|%s", p.Listening.Model, p.Speaking.Provider, p.Speaking.Model, p.Speaking.Variant, p.Speaking.Voices, p.Speaking.Binary+"|"+p.Mode)
		if seenProfiles[key] {
			continue
		}
		seenProfiles[key] = true
		item := modelChoice{ID: id, Name: strings.Split(p.Speaking.Name, " · ")[0], Mode: p.Mode, RequiresNew: current.Mode != "composed" || p.Mode != "composed", Experimental: p.Mode != "composed"}
		if p.Mode != "composed" {
			item.Name = "MiniCPM-o 4.5"
		}
		if p.Mode == "composed" && len(c.Models) > 0 {
			candidate.ThinkingModel = c.Models[0].ID
		}
		if err := Check(candidate); err != nil {
			item.Unavailable = err.Error()
		}
		c.Profiles = append(c.Profiles, item)
	}
	return c
}
