package terminal

import (
	tea "charm.land/bubbletea/v2"
	"encoding/json"
	"io"
	"strings"
)

type modelEntry struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Unavailable  string `json:"unavailable"`
	Mode         string `json:"mode"`
	RequiresNew  bool   `json:"requires_new"`
	Experimental bool   `json:"experimental"`
}
type modelsCatalog struct {
	Profile    string       `json:"profile"`
	Model      string       `json:"model"`
	Switchable bool         `json:"switchable"`
	Profiles   []modelEntry `json:"profiles"`
	Models     []modelEntry `json:"models"`
}
type modelConfirmMsg struct{ profile string }
type modelSwitchMsg struct {
	profile string
	err     error
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
	confirmed := strings.HasPrefix(arg, "--new ")
	arg = strings.TrimPrefix(arg, "--new ")
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
		newConversation := false
		if command == "/backend" {
			for _, p := range catalog.Profiles {
				if p.ID == arg {
					newConversation = p.RequiresNew
					if p.Mode == "minicpm-native" {
						catalog.Model = ""
					}
				}
			}
			if newConversation && !confirmed {
				return modelConfirmMsg{arg}
			}
			catalog.Profile = arg
		} else {
			catalog.Model = arg
		}
		err = m.client.action(m.ctx, "/api/models", map[string]any{"profile": catalog.Profile, "model": catalog.Model, "new_conversation": newConversation && confirmed})
		if newConversation {
			return modelSwitchMsg{catalog.Profile, err}
		}
		return resultMsg{err}
	}
}
