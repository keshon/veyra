package terminal

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/keshon/veyra/internal/terminal/components/selection"
)

type choice struct{ value, label, description string }

// Commands and help share one vocabulary. Provider actions remain in command().
var commands = []choice{
	{"/generation", "/generation", "Inspect or change generation settings"},
	{"/character", "/character", "Choose a character; /character import <PNG or JSON path>"},
	{"/talk", "/talk", "Record a message; run again to send"},
	{"/stop", "/stop", "Stop the response or discard a recording"},
	{"/live", "/live", "Toggle hands-free conversation"},
	{"/mic", "/mic", "Choose a microphone"},
	{"/voice", "/voice", "Choose a speech voice"},
	{"/prompt", "/prompt", "Choose a system prompt; starts a new conversation"},
	{"/backend", "/backend", "Choose an installed voice backend; keeps conversation"},
	{"/model", "/model", "Choose a generation model; keeps conversation"},
	{"/settings", "/settings", "Voice settings and current model details"},
	{"/sessions", "/sessions", "Find and resume a saved conversation"},
	{"/new", "/new", "Start a new conversation"},
	{"/compact", "/compact", "Summarize older conversation turns"},
	{"/intro", "/intro", "Show the character introduction"},
	{"/heard", "/heard", "Inspect recorded audio (diagnostics must be enabled)"},
	{"/help", "/help", "Show commands and keyboard shortcuts"},
	{"/quit", "/quit", "Exit Veyra"},
}

type picker struct {
	title     string
	items     []choice
	query     textinput.Model
	selection selection.Model
}

func filterChoices(items []choice, query string) []choice {
	query = strings.ToLower(strings.TrimSpace(query))
	var matches []choice
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.label+" "+item.description), query) {
			matches = append(matches, item)
		}
	}
	return matches
}

func (m *model) openPicker(title string, items []choice) tea.Cmd {
	if len(items) == 0 {
		m.notice = "No choices available for this backend."
		return nil
	}
	query := textinput.New()
	query.Placeholder = "Search…"
	query.CharLimit = 120
	query.SetWidth(max(1, m.width-6))
	focus := query.Focus()
	m.input.Blur()
	m.picker = &picker{title: title, items: items, query: query}
	return focus
}

func (m *model) completions() []choice {
	text := m.input.Value()
	if !strings.HasPrefix(text, "/") || strings.ContainsAny(text, " \t\n") || text == m.dismissed {
		return nil
	}
	return filterChoices(commands, strings.TrimPrefix(text, "/"))
}

// Pickers consume keys before the composer, so Enter and Esc have one owner.
func (m *model) commandKey(key tea.KeyPressMsg) (bool, tea.Cmd) {
	if key.String() == "ctrl+c" {
		return false, nil
	}
	items := m.completions()
	selected := &m.commandSelection
	if m.picker != nil {
		items = filterChoices(m.picker.items, m.picker.query.Value())
		selected = &m.picker.selection
	}
	if m.picker == nil && len(items) == 0 {
		return false, nil
	}
	selected.Clamp(len(items), max(1, m.height-13))
	if selected.Update(key, len(items), max(1, m.height-13)) {
		return true, nil
	}
	switch key.String() {
	case "esc":
		m.picker = nil
		m.dismissed = m.input.Value()
		return true, m.input.Focus()
	case "enter", "tab":
		if len(items) == 0 {
			return true, nil
		}
		item := items[selected.Cursor]
		if m.picker == nil && key.String() == "tab" {
			m.input.SetValue(item.value + " ")
			m.input.CursorEnd()
			return true, nil
		}
		if m.picker == nil {
			m.input.SetValue("")
		}
		m.picker = nil
		focus := m.input.Focus()
		return true, tea.Batch(focus, m.command(item.value))
	default:
		if m.picker != nil {
			var cmd tea.Cmd
			previous := m.picker.query.Value()
			m.picker.query, cmd = m.picker.query.Update(key)
			if previous != m.picker.query.Value() {
				m.picker.selection = selection.Model{}
			}
			return true, cmd
		}
	}
	return false, nil
}

func (m *model) pickerView() string {
	items, title := m.completions(), "Commands"
	selected := &m.commandSelection
	query := ""
	if m.picker != nil {
		items, title = filterChoices(m.picker.items, m.picker.query.Value()), m.picker.title
		selected, query = &m.picker.selection, m.picker.query.View()
	} else if len(items) == 0 {
		return ""
	}
	width, height := max(1, m.width-2), max(1, m.height-7)
	capacity := max(1, height-4)
	selected.Clamp(len(items), capacity)
	start, end := selected.Offset, min(selected.Offset+capacity, len(items))
	rows := []string{lipgloss.NewStyle().Bold(true).Render(title), query}
	if len(items) == 0 {
		rows = append(rows, "No matching choices.")
	}
	for i := start; i < end; i++ {
		row := "  " + items[i].label
		if items[i].description != "" {
			row += "  " + items[i].description
		}
		row = clean(row)
		if i == selected.Cursor {
			row = "> " + strings.TrimPrefix(row, "  ")
			row = lipgloss.NewStyle().Reverse(true).Render(ansi.Truncate(clean(row), width, "…"))
		}
		rows = append(rows, ansi.Truncate(row, width, "…"))
	}
	rows = append(rows, "↑↓ select · Enter choose · Tab complete · Esc close")
	for len(rows) < height {
		rows = append(rows, "")
	}
	for i := range rows {
		rows[i] = ansi.Truncate(rows[i], width, "…")
	}
	return strings.Join(rows[:min(len(rows), height)], "\n")
}

func (m *model) chooseMicrophone(output string) tea.Cmd {
	var items []choice
	for _, name := range strings.Split(strings.TrimPrefix(output, "Microphones (select with /mic DEVICE):\n"), "\n") {
		if strings.HasPrefix(name, "No DirectShow") {
			m.notice = name
			return nil
		}
		if name != "" {
			items = append(items, choice{"/mic " + name, name, ""})
		}
	}
	return m.openPicker("Microphone", items)
}

func (m *model) voicePicker() tea.Cmd {
	var items []choice
	for _, voice := range m.status.Capabilities.Voices {
		items = append(items, choice{"/voice " + voice.ID, voice.Name, voice.Language})
	}
	return m.openPicker("Speech voice", items)
}

func (m *model) promptCommand(id string) tea.Cmd {
	if m.busy || m.recording != nil || m.recovering || m.liveWanted {
		m.notice = "Stop recording, hands-free mode or the response before changing prompts."
		return nil
	}
	if id == "" {
		var items []choice
		for _, prompt := range m.status.Prompts {
			items = append(items, choice{"/prompt " + prompt.ID, prompt.Name, ""})
		}
		return m.openPicker("System prompt · starts a new conversation", items)
	}
	custom := ""
	if strings.HasPrefix(id, "custom ") {
		custom = strings.TrimSpace(strings.TrimPrefix(id, "custom "))
		id = m.status.Preferences.PromptID
	} else if id == "clear" {
		id = m.status.Preferences.PromptID
	}
	return func() tea.Msg {
		return promptChangedMsg{m.client.action(m.ctx, "/api/prompt", map[string]string{"id": id, "custom": custom})}
	}
}

type promptChangedMsg struct{ err error }

func (m *model) stopResponse() tea.Cmd {
	m.detector.reset()
	m.pendingVoice = nil
	if m.recording != nil {
		m.recordingCancel()
		m.recording = nil
		m.notice = "Recording discarded."
		return nil
	}
	if m.player != nil {
		m.player.cancel()
	}
	if m.turnCancel != nil {
		m.turnCancel()
		m.interrupted = true
	}
	if !m.busy {
		m.notice = "Speech stopped."
		return nil
	}
	m.notice = "Stopping response…"
	return func() tea.Msg { return resultMsg{m.client.action(m.ctx, "/api/cancel", map[string]any{})} }
}

func (m *model) settingsPicker() tea.Cmd {
	m.add(fmt.Sprintf("Recognition: %s\nGeneration: %s\nSpeech: %s", m.status.Capabilities.Listening, m.status.Capabilities.Thinking, m.status.Capabilities.Speaking))
	return m.openPicker("Settings", []choice{{"/generation", "Generation settings", "Context, reply length and sampling"}, {"/character", "Character", "Import or choose a card"}, {"/backend", "Voice backend", "Keeps conversation"}, {"/model", "Generation model", "Keeps conversation"}, {"/mic", "Microphone", m.microphone}, {"/voice", "Speech voice", ""}, {"/prompt", "System prompt", "Starts a new conversation"}, {"/help", "Keyboard and commands", ""}})
}
