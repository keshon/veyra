package terminal

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/keshon/veyra/internal/config"
)

type editableSettings struct {
	Preferences    config.Preferences `json:"preferences"`
	ReloadSampling bool               `json:"reload_sampling"`
	Models         modelsCatalog      `json:"models"`
}
type cardChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type preferenceChoicesMsg struct {
	title   string
	choices []choice
	err     error
}

func (m *model) preferencesCommand(name, arg string) tea.Cmd {
	if m.busy || m.recording != nil || m.recovering || m.liveWanted {
		m.notice = "Stop the current operation before changing settings."
		return nil
	}
	if strings.HasPrefix(arg, "edit ") {
		m.input.SetValue(name + " " + strings.TrimPrefix(arg, "edit "))
		m.input.CursorEnd()
		m.notice = "Edit the value, then press Enter to apply."
		return m.input.Focus()
	}
	confirmed := strings.HasPrefix(arg, "--new ")
	arg = strings.TrimPrefix(arg, "--new ")
	return func() tea.Msg {
		response, err := m.client.request(m.ctx, "GET", "/api/settings", nil, "")
		if err != nil {
			return resultMsg{err}
		}
		var settings editableSettings
		err = json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&settings)
		response.Body.Close()
		if err != nil {
			return resultMsg{err}
		}
		p := settings.Preferences
		if name == "/character" {
			if strings.HasPrefix(arg, "import ") {
				path := strings.Trim(strings.TrimSpace(strings.TrimPrefix(arg, "import ")), "\"")
				file, err := os.Open(path)
				if err != nil {
					return resultMsg{err}
				}
				data, err := io.ReadAll(io.LimitReader(file, (32<<20)+1))
				file.Close()
				if err != nil {
					return resultMsg{err}
				}
				if len(data) > 32<<20 {
					return resultMsg{fmt.Errorf("card exceeds 32 MiB")}
				}
				response, err := m.client.request(m.ctx, "POST", "/api/characters/import", data, "application/octet-stream")
				if err != nil {
					return resultMsg{err}
				}
				var card cardChoice
				err = json.NewDecoder(response.Body).Decode(&card)
				response.Body.Close()
				if err != nil {
					return resultMsg{err}
				}
				return preferenceChoicesMsg{title: "Imported " + card.Name, choices: []choice{{value: "/character --new " + card.ID, label: "Use " + card.Name, description: "Starts a new conversation"}, {value: "/character", label: "Keep current character", description: "Imported card stays in the library"}}}
			}
			if !confirmed {
				response, err := m.client.request(m.ctx, "GET", "/api/characters", nil, "")
				if err != nil {
					return resultMsg{err}
				}
				var cards []cardChoice
				err = json.NewDecoder(response.Body).Decode(&cards)
				response.Body.Close()
				choices := []choice{}
				for _, card := range cards {
					id := card.ID
					if id == "" {
						id = "none"
					}
					choices = append(choices, choice{value: "/character --new " + id, label: card.Name, description: "Starts a new conversation"})
				}
				return preferenceChoicesMsg{"Character", choices, err}
			}
			p.Character = strings.TrimSpace(arg)
			if p.Character == "none" {
				p.Character = ""
			}
		} else {
			key, value, ok := strings.Cut(arg, " ")
			if !ok {
				g := p.Generation
				return preferenceChoicesMsg{title: "Generation - choose a value to edit", choices: []choice{
					{value: fmt.Sprintf("/generation edit temp %g", g.Temperature), label: fmt.Sprintf("Temperature: %g", g.Temperature)},
					{value: fmt.Sprintf("/generation edit top-p %g", g.TopP), label: fmt.Sprintf("Top-p: %g", g.TopP)},
					{value: fmt.Sprintf("/generation edit top-k %d", g.TopK), label: fmt.Sprintf("Top-k: %d", g.TopK)},
					{value: fmt.Sprintf("/generation edit repeat-penalty %g", g.RepeatPenalty), label: fmt.Sprintf("Repetition penalty: %g", g.RepeatPenalty)},
					{value: fmt.Sprintf("/generation edit max-tokens %d", g.MaxTokens), label: fmt.Sprintf("Maximum reply tokens: %d", g.MaxTokens)},
					{value: fmt.Sprintf("/generation edit ctx %d", g.Context), label: fmt.Sprintf("Context tokens: %d", g.Context)},
					{value: fmt.Sprintf("/generation edit seed %d", g.Seed), label: fmt.Sprintf("Seed: %d", g.Seed)},
				}}
			}
			number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				return resultMsg{err}
			}
			integer := func() (int, error) { return strconv.Atoi(strings.TrimSpace(value)) }
			switch key {
			case "temp":
				p.Generation.Temperature = number
			case "top-p":
				p.Generation.TopP = number
			case "repeat-penalty":
				p.Generation.RepeatPenalty = number
			case "ctx":
				p.Generation.Context, err = integer()
			case "max-tokens":
				p.Generation.MaxTokens, err = integer()
			case "top-k":
				p.Generation.TopK, err = integer()
			case "seed":
				p.Generation.Seed, err = integer()
			default:
				return resultMsg{fmt.Errorf("unknown generation setting %q", key)}
			}
			if err != nil {
				return resultMsg{err}
			}
			if err = p.Generation.Validate(); err != nil {
				return resultMsg{err}
			}
		}
		if name == "/generation" && settings.ReloadSampling && !confirmed {
			for _, profile := range settings.Models.Profiles {
				if profile.ID == settings.Models.Profile && profile.RequiresNew {
					return preferenceChoicesMsg{title: "Native settings require a new conversation", choices: []choice{{value: "/generation --new " + arg, label: "Apply and start new", description: "Reloads native backend; current native state cannot be transferred"}, {value: "/generation", label: "Cancel"}}}
				}
			}
		}
		err = m.client.action(m.ctx, "/api/settings", map[string]any{"preferences": p, "new_conversation": name == "/character" || confirmed})
		if name == "/character" || confirmed {
			return modelSwitchMsg{settings.Models.Profile, err}
		}
		return resultMsg{err}
	}
}
