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

// Component names a supported protocol and its local assets, not an arbitrary command.
type Component struct {
	Variant   string `json:"variant,omitempty"`
	Provider  string `json:"provider"`
	Name      string `json:"name"`
	Binary    string `json:"binary,omitempty"`
	CPUBinary string `json:"cpu_binary,omitempty"`
	Model     string `json:"model,omitempty"`
	Voices    string `json:"voices,omitempty"`
	Library   string `json:"library,omitempty"`
}
type Native struct {
	ModelDir            string `json:"model_dir"`
	TTSDir              string `json:"tts_dir"`
	Reference           string `json:"reference"`
	SettledCancellation bool   `json:"settled_cancellation,omitempty"`
}
type Profile struct {
	Version   int       `json:"version"`
	Name      string    `json:"name"`
	Mode      string    `json:"mode"`
	Listening Component `json:"listening"`
	Thinking  Component `json:"thinking"`
	Speaking  Component `json:"speaking"`
	Native    *Native   `json:"native,omitempty"`
}

// LoadProfile resolves asset paths against the manifest directory, never the caller's cwd.
func LoadProfile(root, name, path string) (Profile, error) {
	if path == "" {
		path = filepath.Join(root, "profiles", name+".json")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return Profile{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, fmt.Errorf("profile %s: %w", name, err)
	}
	var p Profile
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(&p); err != nil {
		return p, fmt.Errorf("profile: %w", err)
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return p, fmt.Errorf("profile must contain one JSON object")
	}
	if p.Version != 1 || p.Name == "" {
		return p, fmt.Errorf("profile requires version 1 and a name")
	}
	if p.Mode == "composed" {
		if p.Listening.Provider != "whisper" || p.Thinking.Provider != "llamacpp" || (p.Speaking.Provider != "piper" && p.Speaking.Provider != "kokoro" && p.Speaking.Provider != "chatterbox") || p.Native != nil {
			return p, fmt.Errorf("composed profile requires whisper / llamacpp / piper, kokoro or chatterbox")
		}
	} else if p.Mode == "minicpm-native" {
		if p.Listening.Provider != "minicpm" || p.Thinking.Provider != "minicpm" || p.Speaking.Provider != "minicpm" || p.Native == nil {
			return p, fmt.Errorf("native MiniCPM must own all three roles and native assets")
		}
	} else {
		return p, fmt.Errorf("unsupported profile mode %q", p.Mode)
	}
	if p.Speaking.Provider == "chatterbox" {
		if p.Speaking.Variant == "" {
			p.Speaking.Variant = "nano"
		}
		if p.Speaking.Variant != "nano" && p.Speaking.Variant != "turbo" {
			return p, fmt.Errorf("unknown Chatterbox variant %q", p.Speaking.Variant)
		}
	}
	for _, c := range []Component{p.Listening, p.Thinking, p.Speaking} {
		if c.Variant != "" && c.Provider != "chatterbox" {
			return p, fmt.Errorf("variant is only supported for Chatterbox")
		}
	}
	resolve := func(value *string) {
		if *value != "" && !filepath.IsAbs(*value) {
			*value = filepath.Join(filepath.Dir(path), *value)
		}
	}
	for _, c := range []*Component{&p.Listening, &p.Thinking, &p.Speaking} {
		if c.Name == "" {
			return p, fmt.Errorf("each component requires a display name")
		}
		resolve(&c.Binary)
		resolve(&c.CPUBinary)
		resolve(&c.Model)
		resolve(&c.Voices)
		resolve(&c.Library)
	}
	if p.Thinking.Binary == "" || p.Thinking.Model == "" {
		return p, fmt.Errorf("thinking binary and model are required")
	}
	if p.Mode == "composed" && (p.Listening.Binary == "" || p.Listening.Model == "" || p.Speaking.Binary == "" || (p.Speaking.Provider == "piper" && p.Speaking.Voices == "" || p.Speaking.Provider == "kokoro" && (p.Speaking.Model == "" || p.Speaking.Library == "") || p.Speaking.Provider == "chatterbox" && p.Speaking.Model == "")) {
		return p, fmt.Errorf("listening binary/model and speaking binary/voices are required")
	}
	if p.Native != nil {
		if p.Native.ModelDir == "" || p.Native.TTSDir == "" || p.Native.Reference == "" {
			return p, fmt.Errorf("native model_dir, tts_dir and reference are required")
		}
		resolve(&p.Native.ModelDir)
		resolve(&p.Native.TTSDir)
		resolve(&p.Native.Reference)
	}
	return p, nil
}

// LoadProfile applies launch overrides without modifying the reusable manifest.
func (s Settings) LoadProfile() (Profile, error) {
	p, err := LoadProfile(s.Root, s.Profile, s.ProfileFile)
	if err != nil {
		return p, err
	}
	if s.ThinkingModel != "" {
		if p.Mode != "composed" {
			return p, fmt.Errorf("thinking-model requires a composed profile; MiniCPM native owns speech and thinking together")
		}
		model := s.ThinkingModel
		if !filepath.IsAbs(model) {
			model = filepath.Join(s.Root, model)
		}
		p.Thinking.Model = model
	}
	if p.Mode == "composed" {
		p.Thinking.Name = strings.TrimSuffix(filepath.Base(p.Thinking.Model), filepath.Ext(p.Thinking.Model)) + " · llama.cpp"
	}
	return p, nil
}
