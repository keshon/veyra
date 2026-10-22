package terminal

import (
	"encoding/json"
	"github.com/keshon/veyra/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerationCommandUsesSharedSettingsAndPreservesOtherValues(t *testing.T) {
	s, err := config.Load([]string{"-data-dir", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	current := editableSettings{Preferences: s.Preferences()}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(current)
			return
		}
		var body struct {
			Preferences config.Preferences `json:"preferences"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests++
		if body.Preferences.Generation.Temperature != .3 || body.Preferences.Generation.MaxTokens != s.MaxTokens {
			t.Error("unrelated settings lost", body)
		}
		w.Write([]byte("{}"))
	}))
	defer server.Close()
	m := commandModel()
	m.client = client{address: server.URL, http: server.Client()}
	msg := m.preferencesCommand("/generation", "temp 0.3")()
	if result, ok := msg.(resultMsg); !ok || result.err != nil {
		t.Fatal(msg)
	}
	if requests != 1 {
		t.Fatal(requests)
	}
	msg = m.preferencesCommand("/generation", "")()
	choices, ok := msg.(preferenceChoicesMsg)
	if !ok || len(choices.choices) != 7 {
		t.Fatal("missing generation editor", msg)
	}
}
