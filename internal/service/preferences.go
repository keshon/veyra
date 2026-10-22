package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/keshon/veyra/internal/character"
)

type characterChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (m *runtimeManager) preferencesHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	s, c := m.settings, m.catalog
	reload := m.current == nil || m.current.apply == nil
	warning := m.errorDetails
	m.mu.Unlock()
	dir := filepath.Join(s.DataDir, "characters")
	switch {
	case r.URL.Path == "/api/settings" && r.Method == "GET":
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"preferences": s.Preferences(), "reload_sampling": reload, "models": c, "warning": warning})
	case r.URL.Path == "/api/settings" && r.Method == "POST":
		var input struct {
			Preferences     json.RawMessage `json:"preferences"`
			NewConversation bool            `json:"new_conversation"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
		d.DisallowUnknownFields()
		if err := d.Decode(&input); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if d.Decode(new(any)) != io.EOF {
			http.Error(w, "expected one JSON object", 400)
			return
		}
		if len(input.Preferences) == 0 || bytes.Equal(input.Preferences, []byte("null")) {
			http.Error(w, "preferences are required", 400)
			return
		}
		// The model-switch path owns validation, admission and runtime replacement.
		data, _ := json.Marshal(map[string]any{"profile": c.Profile, "model": c.Model, "preferences": input.Preferences, "new_conversation": input.NewConversation})
		r.Body = io.NopCloser(bytes.NewReader(data))
		m.change(w, r)
	case r.URL.Path == "/api/characters" && r.Method == "GET":
		choices := []characterChoice{{ID: "", Name: "No character"}}
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			http.Error(w, err.Error(), 500)
			return
		}
		found := false
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			card, err := character.Load(path)
			if err == nil {
				choices = append(choices, characterChoice{path, card.Name})
				found = found || path == s.Character
			}
		}
		if s.Character != "" && !found {
			if card, err := character.Load(resolve(s.Root, s.Character)); err == nil {
				choices = append(choices, characterChoice{s.Character, card.Name})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(choices)
	case r.URL.Path == "/api/characters/import" && r.Method == "POST":
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
		if err != nil {
			http.Error(w, "Card must be a JSON or PNG file under 32 MiB.", 400)
			return
		}
		if err = os.MkdirAll(dir, 0700); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		file, err := os.CreateTemp(dir, "import-*")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer os.Remove(file.Name())
		_, err = file.Write(data)
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		card, err := character.Load(file.Name())
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		ext := ".json"
		if bytes.HasPrefix(data, []byte{137, 80, 78, 71}) {
			ext = ".png"
		}
		sum := sha256.Sum256(data)
		path := filepath.Join(dir, fmt.Sprintf("%x%s", sum[:12], ext))
		if err = os.Rename(file.Name(), path); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(characterChoice{path, strings.TrimSpace(card.Name)})
	default:
		http.Error(w, "method not allowed", 405)
	}
}
