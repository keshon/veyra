package terminal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/keshon/veyra/internal/conversation"
)

func TestTextStreamAndIncompleteStream(t *testing.T) {
	for _, complete := range []bool{true, false} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/text" {
				t.Errorf("path %s", r.URL.Path)
			}
			fmt.Fprintln(w, `{"kind":"text","text":"Hello."}`)
			if complete {
				fmt.Fprintln(w, `{"kind":"done"}`)
			}
		}))
		c := client{server.URL, server.Client()}
		text := ""
		err := c.turn(context.Background(), "Hi", nil, func(e wireEvent) error { text += e.Text; return nil })
		server.Close()
		if text != "Hello." || (err == nil) != complete {
			t.Fatal(text, err)
		}
	}
}
func TestTerminalPreservesRejectedInputAndIgnoresStaleEvents(t *testing.T) {
	input := textinput.New()
	input.SetValue("Keep this question")
	m := &model{input: input, viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(10)), width: 80, busy: true, revision: 2, assistant: -1, status: serviceStatus{State: "ready", Capabilities: conversation.Capabilities{TextInput: true}}}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || m.input.Value() != "Keep this question" {
		t.Fatal("rejected input lost")
	}
	m.Update(eventMsg{1, wireEvent{Kind: "text", Text: "stale"}})
	if len(m.history) != 0 {
		t.Fatal("stale output accepted")
	}
	m.add("visible\x1b[2J\x00")
	if strings.Contains(m.history[0], "\x1b") || strings.Contains(m.history[0], "\x00") {
		t.Fatal("terminal controls retained")
	}
}
func TestStopSpeechDoesNotCancelGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	playCtx, stop := context.WithCancel(ctx)
	m := &model{input: textinput.New(), player: &playback{ctx: playCtx, cancel: stop}, turnCancel: cancel, busy: true}
	m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if playCtx.Err() == nil || ctx.Err() != nil || !m.busy {
		t.Fatal("stop speech altered generation")
	}
}
