package service

import (
	"context"
	"encoding/json"
	"github.com/keshon/veyra/internal/config"
	"testing"
)

func TestSharedPreferencesApplyWithoutReloadAndImport(t *testing.T) {
	s, err := config.Load([]string{"-data-dir", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	old := testRuntime(t)
	applied := false
	old.apply = func(next config.Settings) { applied = next.Temperature == .3 }
	m := &runtimeManager{ctx: context.Background(), settings: s, current: old, catalog: modelCatalog{Switchable: true, Profile: "nano", Model: "model.gguf", Profiles: []modelChoice{{ID: "nano", Mode: "composed"}}, Models: []modelChoice{{ID: "model.gguf"}}}, check: func(config.Settings) error { return nil }}
	p := s.Preferences()
	p.Generation.Temperature = .3
	data, _ := json.Marshal(map[string]any{"preferences": p})
	w := requestModels(m, "POST", "/api/settings", string(data))
	if w.Code != 200 || !applied {
		t.Fatalf("hot apply %d %s", w.Code, w.Body)
	}
	p.Generation.TopK = -1
	data, _ = json.Marshal(map[string]any{"preferences": p})
	if w = requestModels(m, "POST", "/api/settings", string(data)); w.Code != 400 {
		t.Fatalf("invalid accepted %d", w.Code)
	}
	if m.settings.Temperature != .3 {
		t.Fatal("invalid request changed settings")
	}
	w = requestModels(m, "POST", "/api/characters/import", `{"name":"Vera","description":"Hello {{user}}"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var card characterChoice
	if err = json.Unmarshal(w.Body.Bytes(), &card); err != nil {
		t.Fatal(err)
	}
	w = requestModels(m, "GET", "/api/characters", "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	p = m.settings.Preferences()
	p.Character = card.ID
	data, _ = json.Marshal(map[string]any{"preferences": p})
	if w = requestModels(m, "POST", "/api/settings", string(data)); w.Code != 409 {
		t.Fatal("character accepted without consent", w.Code)
	}
	m.start = func(context.Context, config.Settings) (*runtime, error) { return testRuntime(t), nil }
	data, _ = json.Marshal(map[string]any{"preferences": p, "new_conversation": true})
	if w = requestModels(m, "POST", "/api/settings", string(data)); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	waitSwitch(t, m)
	if m.settings.Character != card.ID {
		t.Fatal("successful reload did not commit character")
	}
	if w = requestModels(m, "POST", "/api/characters/import", `{"broken":true}`); w.Code != 400 {
		t.Fatal("invalid card accepted", w.Code)
	}
}
