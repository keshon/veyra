package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/keshon/veyra/internal/conversation"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func LoadPrompts(root, path string) ([]conversation.Prompt, error) {
	if path == "" {
		return conversation.DefaultPrompts(), nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > 65536 {
		return nil, fmt.Errorf("prompt catalog exceeds 64 KiB")
	}
	var entries []struct {
		conversation.Prompt
		InstructionFile string `json:"instruction_file"`
		PostHistoryFile string `json:"post_history_file"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("expected one prompt catalog")
	}
	if len(entries) == 0 || len(entries) > 32 {
		return nil, fmt.Errorf("use 1-32 prompts")
	}
	seen := map[string]bool{}
	catalog := make([]conversation.Prompt, 0, len(entries))
	for _, entry := range entries {
		p := entry.Prompt
		for _, field := range []struct {
			file string
			text *string
		}{{entry.InstructionFile, &p.Instruction}, {entry.PostHistoryFile, &p.PostHistory}} {
			if field.file == "" {
				continue
			}
			if *field.text != "" {
				return nil, fmt.Errorf("prompt %q: use inline text or a file, not both", p.ID)
			}
			name := field.file
			if !filepath.IsAbs(name) {
				name = filepath.Join(filepath.Dir(path), name)
			}
			file, err := os.Open(name)
			if err != nil {
				return nil, fmt.Errorf("prompt %q: %w", p.ID, err)
			}
			data, err := io.ReadAll(io.LimitReader(file, 4004))
			file.Close()
			if err != nil {
				return nil, fmt.Errorf("prompt %q: %w", p.ID, err)
			}
			*field.text = strings.TrimPrefix(string(data), "\uFEFF")
		}
		if p.ID == "" || len(p.ID) > 64 || seen[p.ID] || strings.TrimSpace(p.Name) == "" || len(p.Name) > 100 || strings.TrimSpace(p.Instruction) == "" || len(p.Instruction) > 4000 || strings.Contains(p.Instruction, "<|") || len(p.PostHistory) > 4000 || strings.Contains(p.PostHistory, "<|") {
			return nil, fmt.Errorf("invalid or duplicate prompt %q; instructions must be plain text up to 4000 bytes", p.ID)
		}
		if !utf8.ValidString(p.Instruction) || !utf8.ValidString(p.PostHistory) {
			return nil, fmt.Errorf("prompt %q must be UTF-8 text", p.ID)
		}
		seen[p.ID] = true
		catalog = append(catalog, p)
	}
	return catalog, nil
}
