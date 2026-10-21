package terminal

import (
	tea "charm.land/bubbletea/v2"
	"encoding/json"
	"io"
)

type modelEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Unavailable string `json:"unavailable"`
}
type modelsCatalog struct {
	Profile    string       `json:"profile"`
	Model      string       `json:"model"`
	Switchable bool         `json:"switchable"`
	Profiles   []modelEntry `json:"profiles"`
	Models     []modelEntry `json:"models"`
}
type modelsMsg struct {
	command string
	catalog modelsCatalog
	err     error
}

func (m *model) modelCommand(command, arg string) tea.Cmd {
	if m.busy || m.recording != nil || m.recovering || m.liveWanted {
		m.notice = "Stop recording, hands-free mode or the response before changing models."
		return nil
	}
	return func() tea.Msg {
		var catalog modelsCatalog
		response, err := m.client.request(m.ctx, "GET", "/api/models", nil, "")
		if err == nil {
			defer response.Body.Close()
			err = json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&catalog)
		}
		if err != nil || arg == "" {
			return modelsMsg{command, catalog, err}
		}
		if command == "/backend" {
			catalog.Profile = arg
		} else {
			catalog.Model = arg
		}
		return resultMsg{m.client.action(m.ctx, "/api/models", map[string]string{"profile": catalog.Profile, "model": catalog.Model})}
	}
}
