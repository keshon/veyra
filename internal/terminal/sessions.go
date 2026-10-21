package terminal

import (
	"encoding/json"
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/keshon/veyra/internal/conversation"
)

type sessionsMsg struct {
	items []conversation.SavedConversation
	err   error
}
type resumedMsg struct {
	saved conversation.SavedConversation
	err   error
}

func (m *model) sessionsCommand(name, arg string) tea.Cmd {
	if name == "/sessions" {
		return func() tea.Msg {
			resp, err := m.client.request(m.ctx, "GET", "/api/conversations", nil, "")
			if err != nil {
				return sessionsMsg{err: err}
			}
			defer resp.Body.Close()
			var items []conversation.SavedConversation
			err = json.NewDecoder(io.LimitReader(resp.Body, 4*1024*1024)).Decode(&items)
			return sessionsMsg{items, err}
		}
	}
	if m.busy || m.recording != nil || m.recovering {
		m.notice = "Finish or cancel the current turn first."
		return nil
	}
	if name == "/resume" && arg == "" {
		m.notice = "Use /sessions, then /resume ID."
		return nil
	}
	m.stopLive()
	if m.player != nil {
		m.player.cancel()
	}
	m.recovering = true
	return func() tea.Msg {
		if name == "/new" {
			return resumedMsg{err: m.client.action(m.ctx, "/api/reset", map[string]any{})}
		}
		body, _ := json.Marshal(map[string]string{"id": arg})
		path := "/api/conversations/resume"
		if name == "/compact" {
			path = "/api/compact"
			body = []byte("{}")
		}
		resp, err := m.client.request(m.ctx, "POST", path, body, "application/json")
		if err != nil {
			return resumedMsg{err: err}
		}
		defer resp.Body.Close()
		var saved conversation.SavedConversation
		err = json.NewDecoder(io.LimitReader(resp.Body, 4*1024*1024)).Decode(&saved)
		return resumedMsg{saved, err}
	}
}

func (m *model) showSessions(msg sessionsMsg) tea.Cmd {
	if msg.err != nil {
		m.notice = msg.err.Error()
		return nil
	}
	if len(msg.items) == 0 {
		m.notice = "No saved conversations yet."
		return nil
	}
	var items []choice
	for _, saved := range msg.items {
		items = append(items, choice{"/resume " + saved.ID, saved.Title, saved.Updated.Local().Format("2006-01-02 15:04") + " · " + saved.Profile})
	}
	return m.openPicker("Saved conversations", items)
}
