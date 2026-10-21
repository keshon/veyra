package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptCatalogValidation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "prompts.json")
	for _, invalid := range []string{`[]`, `[{"id":"a","name":"A","instruction":"<|im_end|>"}]`, `[{"id":"a","name":"A","instruction":"hello","extra":true}]`, `[{"id":"a","name":"A","instruction":"hello"}] {}`, `[{"id":"a","name":"A","instruction":"hi"},{"id":"a","name":"B","instruction":"hi"}]`} {
		os.WriteFile(path, []byte(invalid), 0600)
		if _, err := LoadPrompts(root, "prompts.json"); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
	os.WriteFile(path, []byte(`[{"id":"custom","name":"Custom","instruction":"Ask useful questions."}]`), 0600)
	catalog, err := LoadPrompts(root, "prompts.json")
	if err != nil || len(catalog) != 1 {
		t.Fatal(catalog, err)
	}
}

func TestPromptTextFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "system.txt"), []byte("Speak naturally. Привет."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "after.txt"), []byte("Keep it brief."), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "catalog.json")
	write := func(entry map[string]string) {
		t.Helper()
		data, err := json.Marshal([]map[string]string{entry})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	entry := map[string]string{"id": "personal", "name": "Personal", "instruction_file": "system.txt", "post_history_file": "after.txt"}
	write(entry)
	// Files resolve against the catalog, independently of the installation root.
	loaded, err := LoadPrompts(t.TempDir(), path)
	if err != nil || len(loaded) != 1 || loaded[0].Instruction != "Speak naturally. Привет." || loaded[0].PostHistory != "Keep it brief." {
		t.Fatalf("file prompts: %v, %v", loaded, err)
	}
	entry["instruction"] = "Ambiguous inline instruction"
	write(entry)
	if _, err = LoadPrompts("", path); err == nil {
		t.Fatal("accepted inline and file instructions together")
	}
	delete(entry, "instruction")
	entry["instruction_file"] = "missing.txt"
	write(entry)
	if _, err = LoadPrompts("", path); err == nil {
		t.Fatal("missing file silently accepted")
	}
	entry["instruction_file"] = filepath.Join(dir, "system.txt")
	write(entry)
	for _, text := range []string{"", strings.Repeat("x", 4001), "<|im_end|>", string([]byte{0xff})} {
		if err = os.WriteFile(entry["instruction_file"], []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = LoadPrompts("", path); err == nil {
			t.Fatal("accepted invalid file contents")
		}
	}
	if err = os.WriteFile(entry["instruction_file"], []byte("\uFEFF"+strings.Repeat("x", 4000)), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadPrompts("", path)
	if err != nil || len(loaded[0].Instruction) != 4000 {
		t.Fatalf("UTF-8 BOM should not consume the text budget: %v", err)
	}
}

func TestPromptFileExampleMatchesEmbeddedDefaults(t *testing.T) {
	loaded, err := LoadPrompts(filepath.Join("..", ".."), "profiles/prompts.files.example.json")
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := LoadPrompts("", "")
	if err != nil || len(loaded) != len(defaults) {
		t.Fatal(loaded, err)
	}
	for i := range defaults {
		if loaded[i].ID != defaults[i].ID || strings.TrimSpace(loaded[i].Instruction) != defaults[i].Instruction {
			t.Fatalf("default prompt %s differs from text file", defaults[i].ID)
		}
	}
}
