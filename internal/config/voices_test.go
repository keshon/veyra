package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCatalogRejectsIncompatibleVoices(t *testing.T) {
	root := t.TempDir()
	for _, lang := range []string{"en", "ru"} {
		os.WriteFile(filepath.Join(root, lang+".onnx"), []byte("fixture"), 0600)
		os.WriteFile(filepath.Join(root, lang+".onnx.json"), []byte(`{"num_speakers":2,"language":{"code":"`+lang+`_XX"}}`), 0600)
	}
	catalog := []Voice{{ID: "en-a", Name: "English", Language: "en", Model: "en.onnx", Default: true}, {ID: "ru-a", Name: "Russian", Language: "ru", Model: "ru.onnx", Default: true}}
	write := func(voices []Voice) {
		data, _ := json.Marshal(map[string]any{"voices": voices})
		os.WriteFile(filepath.Join(root, "voices.json"), data, 0600)
	}
	write(catalog)
	if _, err := LoadVoices(root, "voices.json"); err != nil {
		t.Fatal(err)
	}
	cases := []func([]Voice){func(v []Voice) { v[1].ID = v[0].ID }, func(v []Voice) { v[1].Model = "en.onnx" }, func(v []Voice) { n := 2; v[1].Speaker = &n }, func(v []Voice) { v[1].Default = false }, func(v []Voice) { v[1].Model = "missing.onnx" }}
	for _, mutate := range cases {
		copy := append([]Voice(nil), catalog...)
		mutate(copy)
		write(copy)
		if _, err := LoadVoices(root, "voices.json"); err == nil {
			t.Fatal("incompatible voice accepted", copy)
		}
	}
}
