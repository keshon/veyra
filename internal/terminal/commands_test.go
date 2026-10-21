package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/language"
)

func commandModel() *model {
	input := textinput.New()
	input.Focus()
	return &model{ctx: context.Background(), input: input, viewport: viewport.New(viewport.WithWidth(78), viewport.WithHeight(17)), width: 80, height: 24, assistant: -1}
}

func TestSlashCompletionAndDismissal(t *testing.T) {
	m := commandModel()
	m.input.SetValue("/micro")
	if items := m.completions(); len(items) != 1 || items[0].value != "/mic" {
		t.Fatal("description search did not find microphone")
	}
	m.input.SetValue("/voi")
	handled, cmd := m.commandKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if !handled || cmd != nil || m.input.Value() != "/voice " {
		t.Fatal("Tab should insert without running")
	}
	m.input.SetValue("/")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.busy, m.turnCancel = true, cancel
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if ctx.Err() != nil || len(m.completions()) != 0 {
		t.Fatal("Escape must dismiss completion without canceling")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if ctx.Err() == nil {
		t.Fatal("Escape outside the picker must stop the response")
	}
}

func TestVoicePickerUsesServicePreferences(t *testing.T) {
	var received language.Preferences
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/preferences" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.Write([]byte("{}"))
	}))
	defer server.Close()
	m := commandModel()
	m.client = client{server.URL, server.Client()}
	m.status.Capabilities.Voices = []conversation.Voice{{ID: "heart", Name: "Heart", Language: "en"}, {ID: "bella", Name: "Bella", Language: "en"}}
	m.status.Preferences = language.Preferences{Reply: "en", Fallback: "en"}
	m.input.SetValue("preserved draft")
	m.voicePicker()
	m.picker.query.SetValue("Bella")
	_, cmd := m.commandKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || m.picker != nil || m.input.Value() != "preserved draft" {
		t.Fatal("picker lost draft or action")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("expected focus and service action")
	}
	for _, action := range batch {
		if result, ok := action().(resultMsg); ok && result.err != nil {
			t.Fatal(result.err)
		}
	}
	if received.Voices["en"] != "bella" || received.Reply != "en" {
		t.Fatal("voice choice did not preserve preferences")
	}
}

func TestPickerNoMatchesResizeAndFailure(t *testing.T) {
	m := commandModel()
	m.input.SetValue("draft")
	m.openPicker("Choices", []choice{{"/help", "Help", ""}})
	m.picker.query.SetValue("missing")
	if handled, cmd := m.commandKey(tea.KeyPressMsg{Code: tea.KeyEnter}); !handled || cmd != nil {
		t.Fatal("empty picker ran an action")
	}
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	view := m.View().Content
	if !strings.Contains(view, "No matching choices") || len(strings.Split(view, "\n")) != 20 {
		t.Fatal("picker does not fit terminal height")
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 50 {
			t.Fatal("picker overflows terminal width")
		}
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.picker != nil || m.input.Value() != "draft" || !m.input.Focused() {
		t.Fatal("picker did not restore composer")
	}
	m.history = []string{"existing conversation"}
	m.Update(promptChangedMsg{errors.New("backend refused change")})
	if len(m.history) != 1 {
		t.Fatal("failed prompt change cleared history")
	}
}

func TestPickerSanitizesUnselectedChoices(t *testing.T) {
	m := commandModel()
	m.openPicker("Microphones", []choice{{"/mic safe", "Safe", ""}, {"/mic other", "\x1b[2JOther", ""}})
	if strings.Contains(m.pickerView(), "\x1b[2J") {
		t.Fatal("unselected choice retained a terminal control sequence")
	}
}

func TestModelSelectorsUseSharedService(t *testing.T) {
	var chosen map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(modelsCatalog{Profile: "nano", Model: "first.gguf", Switchable: true, Profiles: []modelEntry{{ID: "nano", Name: "Nano"}, {ID: "missing", Name: "Missing", Unavailable: "not installed"}}, Models: []modelEntry{{ID: "second model.gguf", Name: "Second"}}})
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&chosen); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()
	m := commandModel()
	m.client = client{server.URL, server.Client()}
	m.history = []string{"existing history"}
	message := m.command("/backend")()
	m.Update(message)
	if m.picker == nil || len(m.picker.items) != 1 || m.picker.items[0].label != "Nano" {
		t.Fatal("picker did not hide unavailable profiles")
	}
	result := m.command("/model second model.gguf")().(resultMsg)
	if result.err != nil || chosen["model"] != "second model.gguf" || chosen["profile"] != "nano" {
		t.Fatal(chosen, result.err)
	}
	if len(m.history) != 1 {
		t.Fatal("model switch cleared displayed history")
	}
	m.liveWanted = true
	if m.command("/backend") != nil {
		t.Fatal("switch allowed during live capture")
	}
}
