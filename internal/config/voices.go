package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Voice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Language string `json:"language"`
	Model    string `json:"model"`
	Speaker  *int   `json:"speaker,omitempty"`
	Default  bool   `json:"default"`
}

func LoadVoices(root, path string) ([]Voice, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var catalog struct {
		Voices []Voice `json:"voices"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&catalog); err != nil {
		return nil, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("voice catalog requires one JSON object")
	}
	ids := map[string]bool{}
	defaults := map[string]int{}
	for i, v := range catalog.Voices {
		if v.ID == "" || v.Name == "" || ids[v.ID] || v.Language != "en" && v.Language != "ru" || v.Speaker != nil && *v.Speaker < 0 {
			return nil, fmt.Errorf("invalid or duplicate voice %q", v.ID)
		}
		ids[v.ID] = true
		if v.Default {
			defaults[v.Language]++
		}
		if v.Model == "" {
			return nil, fmt.Errorf("voice %q requires a model", v.ID)
		}
		if !filepath.IsAbs(v.Model) {
			v.Model = filepath.Join(root, v.Model)
		}
		for _, p := range []string{v.Model, v.Model + ".json"} {
			if _, err = os.Stat(p); err != nil {
				return nil, fmt.Errorf("voice %q: %w", v.ID, err)
			}
		}
		metadata, err := os.ReadFile(v.Model + ".json")
		if err != nil {
			return nil, err
		}
		var model struct {
			Speakers int `json:"num_speakers"`
			Language struct {
				Code string `json:"code"`
			} `json:"language"`
		}
		if err = json.Unmarshal(metadata, &model); err != nil {
			return nil, fmt.Errorf("voice %q model metadata: %w", v.ID, err)
		}
		if model.Speakers < 1 || v.Speaker != nil && *v.Speaker >= model.Speakers {
			return nil, fmt.Errorf("voice %q has an invalid speaker ID", v.ID)
		}
		if code := strings.Split(strings.ReplaceAll(strings.ToLower(model.Language.Code), "-", "_"), "_")[0]; code != v.Language {
			return nil, fmt.Errorf("voice %q model language does not match %s", v.ID, v.Language)
		}
		catalog.Voices[i] = v
	}
	for _, lang := range []string{"en", "ru"} {
		if defaults[lang] != 1 {
			return nil, fmt.Errorf("configure exactly one default voice for %s", lang)
		}
	}
	return catalog.Voices, nil
}
