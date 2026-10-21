package terminal

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

func TestV2InputResizeAndView(t *testing.T) {
	input := textinput.New()
	input.Focus()
	m := &model{input: input, viewport: viewport.New(), width: 80, height: 24, assistant: -1}
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	if m.viewport.Width() != 48 || m.viewport.Height() != 13 || m.input.Width() != 46 {
		t.Fatal("terminal components did not resize")
	}
	m.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	m.Update(tea.KeyReleaseMsg{Code: 'h', Text: "h"})
	if m.input.Value() != "h" {
		t.Fatalf("key release duplicated input: %q", m.input.Value())
	}
	view := m.View()
	if !view.AltScreen || !strings.Contains(view.Content, "VEYRA") {
		t.Fatal("terminal view lost its content or alternate-screen setting")
	}
	_, quit := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if quit == nil {
		t.Fatal("Ctrl+C must quit")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("Ctrl+C returned an unexpected command")
	}
}

func TestV2ProgramStartsAndQuits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"state":"ready","capabilities":{"text_input":true}}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := &model{ctx: ctx, client: client{server.URL, server.Client()}, input: textinput.New(), viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(15)), width: 80, height: 24, assistant: -1}
	program := tea.NewProgram(m, tea.WithInput(strings.NewReader("\x03")), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithContext(ctx))
	m.send = program.Send
	if _, err := program.Run(); err != nil {
		t.Fatalf("terminal startup/quit failed: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("terminal did not quit on Ctrl+C before its deadline")
	}
}
