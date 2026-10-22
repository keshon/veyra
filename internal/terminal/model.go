package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/keshon/veyra/internal/conversation"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/keshon/veyra/internal/process"
	"github.com/keshon/veyra/internal/terminal/components/selection"
)

type statusMsg struct {
	status serviceStatus
	err    error
}
type resultMsg struct{ err error }
type eventMsg struct {
	revision int
	event    wireEvent
}
type finishedMsg struct {
	revision int
	err      error
}
type recordedMsg struct {
	recording *recording
	data      []byte
	err       error
}
type devicesMsg string
type pollMsg struct{}
type playbackMarkedMsg struct{ err error }

type model struct {
	newBackendProfile string
	picker            *picker
	commandSelection  selection.Model
	dismissed         string

	captureRoot, vadMode                       string
	ctx                                        context.Context
	client                                     client
	send                                       func(tea.Msg)
	input                                      textinput.Model
	viewport                                   viewport.Model
	width, height                              int
	status                                     serviceStatus
	notice                                     string
	turnWarning                                string
	playbackStopped                            bool
	playbackTurnID                             uint64
	history                                    []string
	introShown, busy                           bool
	revision                                   int
	turnCancel                                 context.CancelFunc
	player                                     *playback
	recording                                  *recording
	recordingCancel                            context.CancelFunc
	microphone                                 string
	assistant                                  int
	liveWanted                                 bool
	capture                                    *liveCapture
	detector                                   detector
	interrupted, recovering                    bool
	pendingVoice                               []byte
	cooldown                                   time.Time
	lastMicrophoneFrame                        time.Time
	diagnosticRoot, diagnosticDir, lastCapture string
	recentPCM                                  []byte
	diagnosticBusy                             bool
}

type Options struct {
	Root, DataDir, Microphone, VAD string
	Live, Diagnostics              bool
	MicThreshold                   float64
	MicStartMS                     int
	MicEndMS, MicMaxSeconds        int
}

func Run(ctx context.Context, address string, options Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	input := textinput.New()
	input.Placeholder = "Type a message or /help"
	input.CharLimit = 4000
	input.Focus()
	m := &model{ctx: ctx, client: client{address: address, http: &http.Client{Timeout: 3 * time.Minute}}, input: input, viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(15)), width: 80, height: 24, microphone: options.Microphone, assistant: -1, liveWanted: options.Live, notice: "Connecting to local service..."}
	m.captureRoot, m.vadMode = options.Root, options.VAD
	if options.Diagnostics {
		m.diagnosticRoot = options.Root
		if options.DataDir == "" {
			return fmt.Errorf("user data directory is required for microphone diagnostics")
		}
		base := filepath.Join(options.DataDir, "voice-diagnostics")
		if err := os.MkdirAll(base, 0700); err != nil {
			return err
		}
		dir, err := os.MkdirTemp(base, "session-")
		if err != nil {
			return err
		}
		m.diagnosticDir = dir
	}
	m.detector.endSamples = options.MicEndMS * microphoneRate / 1000
	m.detector.maxSeconds = options.MicMaxSeconds
	m.detector.floor = options.MicThreshold
	m.detector.startSamples = options.MicStartMS * microphoneRate / 1000
	if options.VAD == "silero" {
		m.detector.startSamples = 1600
	}
	program := tea.NewProgram(m, tea.WithContext(ctx))
	m.send = program.Send
	_, err := program.Run()
	cancel()
	if m.capture != nil {
		m.capture.cancel()
	}
	if m.recordingCancel != nil {
		m.recordingCancel()
	}
	if m.turnCancel != nil {
		m.turnCancel()
	}
	if m.player != nil {
		m.player.cancel()
	}
	return err
}
func (m *model) Init() tea.Cmd { return tea.Batch(textinput.Blink, m.poll(), pollTick()) }
func (m *model) poll() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 2*time.Second)
		defer cancel()
		s, e := m.client.status(ctx)
		return statusMsg{s, e}
	}
}
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' || r == 127 {
			return -1
		}
		return r
	}, ansi.Strip(s))
}
func (m *model) redraw() {
	m.viewport.SetContent(lipgloss.NewStyle().Width(max(1, m.width-2)).Render(strings.Join(m.history, "\n\n")))
	m.viewport.GotoBottom()
}
func (m *model) add(text string) {
	m.history = append(m.history, clean(text))
	if len(m.history) > 200 {
		cut := len(m.history) - 200
		m.history = m.history[cut:]
		if m.assistant >= 0 {
			m.assistant -= cut
			if m.assistant < 0 {
				m.assistant = -1
			}
		}
	}
	m.redraw()
}
func (m *model) begin(text string, audio []byte) tea.Cmd {
	if m.recovering {
		m.notice = "Recovering interrupted response; keep speaking."
		return nil
	}
	if m.busy {
		m.notice = "Response active. Ctrl+O stops speech; Esc cancels."
		return nil
	}
	if m.recording != nil {
		m.notice = "Finish or discard the recording first."
		return nil
	}
	if m.status.State != "ready" {
		m.notice = "Service is " + m.status.State + ": " + m.status.Detail
		return nil
	}
	if text != "" && !m.status.Capabilities.TextInput {
		m.notice = "Native MiniCPM requires audio. Ctrl+R records; Kokoro accepts text."
		return nil
	}
	if text != "" {
		user := m.status.Setup.User
		if user == "" {
			user = "You"
		}
		m.add(user + ": " + text)
	}
	m.busy = true
	m.turnWarning = ""
	m.interrupted = false
	m.revision++
	rev := m.revision
	ctx, cancel := context.WithCancel(m.ctx)
	m.turnCancel = cancel
	m.playbackStopped = false
	m.playbackTurnID = 0
	m.player = newPlayback(ctx)
	player := m.player
	m.assistant = -1
	m.notice = "Generating..."
	capturePath := ""
	if len(audio) > 0 && m.diagnosticDir != "" {
		capturePath = filepath.Join(m.diagnosticDir, fmt.Sprintf("turn-%d.wav", rev))
		m.lastCapture = capturePath
	}
	return func() tea.Msg {
		if capturePath != "" {
			if err := os.WriteFile(capturePath, audio, 0600); err != nil {
				m.send(diagnosticMsg{label: "Capture save", err: err})
			}
		}
		err := m.client.turn(ctx, text, audio, func(e wireEvent) error {
			m.send(eventMsg{rev, e})
			if e.Audio != "" {
				b, err := m.client.audio(ctx, e.Audio)
				if err != nil {
					return err
				}
				return player.enqueue(ctx, b)
			}
			return nil
		})
		if err != nil {
			player.cancel()
		}
		pe := player.finish()
		cancel()
		if err == nil {
			err = pe
		}
		return finishedMsg{rev, err}
	}
}
func (m *model) record() tea.Cmd {
	if m.capture != nil && !m.busy {
		m.stopLive()
	}
	if m.recording != nil {
		r := m.recording
		m.notice = "Finishing recording..."
		return func() tea.Msg { r.stop(); return nil }
	}
	if m.busy || m.status.State != "ready" {
		m.notice = "Wait for the response, or cancel it before recording."
		return nil
	}
	if strings.TrimSpace(m.microphone) == "" {
		m.notice = "Choose a microphone, then Ctrl+R to record."
		return m.command("/devices")
	}
	ctx, cancel := context.WithCancel(m.ctx)
	r, err := prepareRecording(ctx, m.microphone, m.detector.recordingSeconds())
	if err != nil {
		cancel()
		m.notice = err.Error()
		return nil
	}
	m.recording = r
	m.recordingCancel = cancel
	m.notice = fmt.Sprintf("Recording: Ctrl+R sends, Esc discards. Maximum %d seconds.", m.detector.recordingSeconds())
	return func() tea.Msg { b, err := r.run(); return recordedMsg{r, b, err} }
}
func (m *model) command(text string) tea.Cmd {
	name, arg, _ := strings.Cut(text, " ")
	arg = strings.TrimSpace(arg)
	switch name {
	case "/generation", "/character":
		return m.preferencesCommand(name, arg)
	case "/talk":
		return m.record()
	case "/stop":
		return m.stopResponse()
	case "/backend", "/model":
		return m.modelCommand(name, arg)
	case "/prompt":
		return m.promptCommand(arg)
	case "/resume":
		if arg == "" {
			return m.sessionsCommand("/sessions", "")
		}
		return m.sessionsCommand(name, arg)
	case "/sessions", "/new", "/compact":
		return m.sessionsCommand(name, arg)
	case "/heard":
		return m.heard(arg)
	case "/live":
		return m.toggleLive(arg)
	case "/quit":
		return tea.Quit
	case "/help":
		var help []string
		for _, item := range commands {
			help = append(help, item.label+"  "+item.description)
		}
		m.add(strings.Join(help, "\n"))
		m.notice = "Type / to search commands. ↑↓ choose · Enter run · Tab complete · Esc close."
	case "/intro":
		m.add(m.status.Setup.Introduction)
	case "/settings":
		return m.settingsPicker()
	case "/mic":
		if m.recording != nil {
			m.notice = "Finish recording first."
		} else if arg == "" {
			return m.command("/devices")
		} else {
			m.microphone = arg
			m.notice = "Microphone: " + arg
			if m.capture != nil {
				m.capture.cancel()
				m.capture = nil
				m.detector.reset()
				return m.listen()
			}
		}
	case "/devices":
		ctx := m.ctx
		return func() tea.Msg {
			cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-list_devices", "true", "-f", "dshow", "-i", "dummy")
			var output bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			_ = process.Run(cmd)
			return devicesMsg(audioDevices(output.String()))
		}
	case "/voice":
		if m.busy || m.recording != nil || m.recovering || m.liveWanted {
			m.notice = "Stop recording, hands-free mode or the response before changing voices."
			return nil
		}
		if arg == "" {
			return m.voicePicker()
		}

		prefs := m.status.Preferences.Clone()
		if prefs.Voices == nil {
			prefs.Voices = map[string]string{}
		}
		prefs.Voices["en"] = arg
		return func() tea.Msg { return resultMsg{m.client.action(m.ctx, "/api/preferences", prefs)} }
	case "/reset":
		if m.busy || m.recording != nil || m.recovering {
			m.notice = "Cancel the response or recording first."
			return nil
		}
		return func() tea.Msg { return resultMsg{m.client.action(m.ctx, "/api/reset", map[string]any{})} }
	default:
		m.notice = "Unknown command. Use /help."
	}
	return nil
}
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case sessionsMsg:
		return m, m.showSessions(msg)
	case resumedMsg:
		m.recovering = false
		if msg.err != nil {
			m.notice = msg.err.Error()
			return m, m.poll()
		}
		m.history = nil
		m.assistant = -1
		m.interrupted = false
		m.turnWarning = ""
		m.status.Context = conversation.ContextUsage{}
		if msg.saved.Summary != "" {
			m.add("Carry-over summary (generated; may omit details): " + msg.saved.Summary)
		}
		m.status.State = "ready"
		if msg.saved.ID != "" {
			m.status.Preferences = msg.saved.Preferences.Clone()
		}
		for _, message := range msg.saved.Messages {
			label := "You"
			if message.Role == "assistant" {
				label = "Veyra"
			}
			text := message.Text
			if message.Interrupted {
				text += "\n[Interrupted; generated text may not have been heard in full.]"
			}
			m.add(label + ": " + text)
		}
		m.redraw()
		m.notice = "Conversation ready. Ctrl+L enables hands-free listening."
		return m, m.poll()
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.SetWidth(max(1, msg.Width-2))
		m.viewport.SetHeight(max(1, msg.Height-7))
		m.input.SetWidth(max(1, msg.Width-4))
		if m.picker != nil {
			m.picker.query.SetWidth(max(1, msg.Width-6))
		}
		m.redraw()
	case diagnosticMsg:
		m.diagnosticBusy = false
		if msg.err != nil {
			m.notice = "Diagnostic: " + msg.err.Error()
		} else {
			m.add("Whisper diagnostic (" + msg.label + "): " + msg.text + "\nWAV: " + msg.path)
		}
		return m, nil
	case statusMsg:
		if msg.err != nil {
			m.notice = "Connecting: " + msg.err.Error()
		} else {
			oldState := m.status.State
			m.status = msg.status
			if m.newBackendProfile != "" {
				if m.status.State == "error" {
					m.newBackendProfile = ""
				} else if m.status.State == "ready" && m.status.Capabilities.Profile == m.newBackendProfile {
					m.history = nil
					m.assistant = -1
					m.newBackendProfile = ""
					m.redraw()
				}
			}
			if !m.busy && !m.detector.active && len(m.pendingVoice) == 0 && m.recording == nil && oldState != m.status.State {
				if m.status.State == "ready" {
					m.notice = "Ready."
				} else if m.status.State == "loading" {
					m.notice = "Loading models..."
				} else if m.status.State == "error" {
					m.notice = m.status.Detail
					m.add("Service error: " + m.status.Detail)
				}
			}
			if !m.introShown {
				m.introShown = true
				for _, warning := range m.status.Setup.Warnings {
					m.add("Character import: " + warning)
				}
				if m.status.Setup.Introduction != "" {
					m.add(m.status.Setup.Character + ": " + m.status.Setup.Introduction)
				}
			}
		}
		if msg.err == nil {
			if cmd := m.submitPendingVoice(); cmd != nil {
				return m, cmd
			}
		}
		if m.liveWanted && m.capture == nil && !m.busy && !m.recovering && m.status.State == "ready" {
			return m, m.listen()
		}
		return m, nil
	case pollMsg:
		return m, tea.Batch(m.poll(), pollTick())
	case resultMsg:
		if msg.err != nil {
			m.notice = msg.err.Error()
		} else {
			m.notice = "Updated."
		}
		return m, m.poll()
	case modelSwitchMsg:
		if msg.err != nil {
			m.notice = msg.err.Error()
		} else {
			m.newBackendProfile = msg.profile
			m.notice = "Loading backend; a new conversation will start when ready."
		}
		return m, m.poll()
	case modelConfirmMsg:
		return m, m.openPicker("Native backend · starts a new conversation", []choice{{"/settings", "Cancel", "Keep the current setup"}, {"/backend --new " + msg.profile, "Start new conversation", "Saved conversations remain available"}})
	case modelsMsg:
		if msg.err != nil {
			m.notice = msg.err.Error()
			return m, nil
		}
		if !msg.catalog.Switchable {
			m.notice = "Native audio backends require restarting Veyra."
			return m, nil
		}
		var items []choice
		entries := msg.catalog.Profiles
		if msg.command == "/model" {
			entries = msg.catalog.Models
		}
		for _, entry := range entries {
			if entry.Unavailable == "" {
				description := ""
				if entry.Experimental {
					description = "Experimental"
				}
				if entry.RequiresNew {
					description += " · starts a new conversation"
				}
				items = append(items, choice{msg.command + " " + entry.ID, entry.Name, description})
			}
		}
		return m, m.openPicker("Models", items)
	case devicesMsg:
		return m, m.chooseMicrophone(string(msg))
	case promptChangedMsg:
		if msg.err != nil {
			m.notice = msg.err.Error()
		} else {
			m.history = nil
			m.assistant = -1
			m.redraw()
			m.notice = "System prompt changed; new conversation started."
		}
		return m, m.poll()
	case recordedMsg:
		if msg.recording != m.recording {
			return m, nil
		}
		m.recording = nil
		if m.recordingCancel != nil {
			m.recordingCancel()
			m.recordingCancel = nil
		}
		if msg.err != nil {
			m.notice = msg.err.Error()
			return m, nil
		}
		return m, m.begin("", msg.data)
	case classifiedMicrophoneMsg:
		return m, microphoneCommand(m, msg.microphoneMsg, &msg.speech)
	case microphoneMsg:
		return m, microphoneCommand(m, msg, nil)
	case captureFinishedMsg:
		if msg.capture != m.capture {
			return m, nil
		}
		m.capture = nil
		m.liveWanted = false
		m.detector.reset()
		if msg.err != nil {
			m.notice = msg.err.Error()
		} else {
			m.notice = "Hands-free microphone stopped."
		}
		return m, nil
	case recoveredMsg:
		m.recovering = false
		m.interrupted = false
		if msg.err != nil {
			m.pendingVoice = nil
			m.stopLive()
			m.notice = "Recovery failed: " + msg.err.Error() + ". /reset or restart is required."
			return m, m.poll()
		}
		m.status.State = "ready"
		m.notice = "Listening after interruption."
		if cmd := m.submitPendingVoice(); cmd != nil {
			return m, cmd
		}
		return m, m.poll()
	case preferenceChoicesMsg:
		if msg.err != nil {
			m.notice = msg.err.Error()
			return m, nil
		}
		return m, m.openPicker(msg.title, msg.choices)
	case playbackMarkedMsg:
		m.busy = false
		if msg.err != nil {
			m.notice = "Could not mark interrupted speech: " + msg.err.Error()
		}
		if cmd := m.submitPendingVoice(); cmd != nil {
			return m, cmd
		}
		return m, m.poll()
	case eventMsg:
		if msg.revision == m.revision && msg.event.TurnID != 0 {
			m.playbackTurnID = msg.event.TurnID
		}
		if msg.revision != m.revision || m.interrupted {
			return m, nil
		}
		if msg.event.Kind == "transcript" {
			m.add("Heard: " + msg.event.Transcript)
		}
		if msg.event.Kind == "text" {
			if m.assistant < 0 {
				m.add("Veyra: ")
				m.assistant = len(m.history) - 1
			}
			m.history[m.assistant] += clean(msg.event.Text)
			m.redraw()
		}
		if msg.event.Kind == "warning" {
			// Compaction notices remain in the transcript after playback starts.
			m.add("Notice: " + msg.event.Text)
			m.turnWarning = clean(msg.event.Text)
			m.notice = m.turnWarning
		}
		if msg.event.Kind == "audio" {
			m.notice = "Speaking..."
		}
		if msg.event.Kind == "context" {
			m.status.Context = msg.event.Context
		}
		if msg.event.Kind == "phase" && msg.event.Phase == "compact" {
			m.notice = "Compacting older conversation..."
		}
	case finishedMsg:
		if msg.revision != m.revision {
			return m, nil
		}
		m.busy = false
		m.player = nil
		m.turnCancel = nil
		m.cooldown = time.Now().Add(350 * time.Millisecond)

		if msg.err == nil && m.playbackStopped && m.playbackTurnID != 0 {
			m.playbackStopped = false
			id := m.playbackTurnID
			mark := func() tea.Msg {
				return playbackMarkedMsg{m.client.action(m.ctx, "/api/interrupted", map[string]any{"turn_id": id})}
			}
			m.status.State = "ready"
			m.notice = "Speech stopped."
			m.busy = true
			return m, mark
		}
		if m.interrupted && m.status.Capabilities.InterruptionMode == "drain" && msg.err == nil {
			m.interrupted = false
			m.status.State = "ready"
			m.notice = "Native turn finished; listening for your correction."
			if cmd := m.submitPendingVoice(); cmd != nil {
				return m, cmd
			}
			return m, m.poll()
		}
		if m.status.Capabilities.InterruptionMode != "drain" && m.status.Capabilities.Interruptions && (m.interrupted || errors.Is(msg.err, context.Canceled) || m.liveWanted && msg.err != nil && strings.Contains(msg.err.Error(), "ASR returned no speech")) {
			m.recovering = true
			m.notice = "Recovering; microphone remains active."
			return m, m.recoverTurn()
		}
		if msg.err != nil {
			m.notice = "Stopped: " + msg.err.Error() + ". /reset may be required."
			if m.status.Capabilities.InterruptionMode == "drain" {
				m.stopLive()
				m.notice = "Native response failed/canceled; restart Veyra."
			}
		} else {
			m.status.State = "ready"
			m.notice = "Ready."
			if m.turnWarning != "" {
				m.notice = m.turnWarning
			}
			if m.detector.active {
				m.notice = "Listening - continue your thought..."
			}
			if cmd := m.submitPendingVoice(); cmd != nil {
				return m, cmd
			}
		}
		return m, m.poll()
	case tea.KeyPressMsg:
		if handled, cmd := m.commandKey(msg); handled {
			return m, cmd
		}
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "ctrl+l":
			return m, m.toggleLive("")
		case "ctrl+r":
			return m, m.record()
		case "ctrl+o":
			if m.player != nil {
				m.playbackStopped = true
			}
			if m.player != nil {
				m.player.cancel()
			}
			m.notice = "Speech stopped; generation continues."
			return m, nil
		case "esc":
			return m, m.stopResponse()
		case "pgup", "pgdown":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			if strings.HasPrefix(text, "/") {
				m.input.SetValue("")
				return m, m.command(text)
			}
			cmd := m.begin(text, nil)
			if cmd != nil {
				m.input.SetValue("")
			}
			return m, cmd
		}
	}
	var cmd tea.Cmd
	if m.picker != nil {
		m.picker.query, cmd = m.picker.query.Update(msg)
		return m, cmd
	}
	before := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	if before != m.input.Value() {
		m.commandSelection = selection.Model{}
		m.dismissed = ""
	}
	return m, cmd
}
func (m *model) View() tea.View {
	width := max(1, m.width-2)
	line := func(text string) string {
		return ansi.Truncate(clean(strings.ReplaceAll(text, "\n", " ")), width, "…")
	}
	state := m.status.State
	if m.recording != nil || m.capture != nil {
		state = "Listening"
	}
	if m.busy {
		state = "Thinking / speaking"
	}
	mic := m.microphone
	if mic == "" {
		mic = "not selected · /mic"
	}
	mode := "Push-to-talk"
	if m.liveWanted {
		mode = "Hands-free"
	}
	middle := m.viewport.View()
	if picker := m.pickerView(); picker != "" {
		middle = picker
	}
	title := lipgloss.NewStyle().Bold(true).Render(line("VEYRA  ·  " + state + "  ·  " + m.status.Capabilities.Profile))
	view := tea.NewView(title + "\n" + line(mode+" · Mic: "+mic) + "\n\n" + middle + "\n\n" + m.input.View() + "\n" + line(m.notice) + "\n" + line("Ctrl+R Talk · Ctrl+L Live · Esc Stop · / Commands · Ctrl+C Quit"))
	view.AltScreen = true
	return view
}

func audioDevices(output string) string {
	var names []string
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "(audio)") {
			continue
		}
		start := strings.Index(line, "\"")
		end := strings.LastIndex(line, "\"")
		if start >= 0 && end > start {
			names = append(names, line[start+1:end])
		}
	}
	if len(names) == 0 {
		return "No DirectShow audio devices found. Check ffmpeg installation and microphone access."
	}
	return "Microphones (select with /mic DEVICE):\n" + strings.Join(names, "\n")
}

func pollTick() tea.Cmd { return tea.Tick(time.Second, func(time.Time) tea.Msg { return pollMsg{} }) }

func microphoneCommand(m *model, msg microphoneMsg, speech *bool) tea.Cmd {
	if msg.capture != m.capture {
		return nil
	}
	if !m.status.Capabilities.Interruptions && (m.busy || time.Now().Before(m.cooldown)) {
		m.detector.reset()
		return nil
	}
	if m.status.State == "error" && !m.recovering && !m.busy {
		m.detector.reset()
		return nil
	}
	if m.diagnosticDir != "" {
		m.recentPCM = append(m.recentPCM, msg.pcm...)
		if len(m.recentPCM) > microphoneRate*2*10 {
			m.recentPCM = m.recentPCM[len(m.recentPCM)-microphoneRate*2*10:]
		}
	}
	firstFrame := m.lastMicrophoneFrame.IsZero()
	m.lastMicrophoneFrame = time.Now()
	if firstFrame && !m.busy {
		m.notice = "Microphone receiving audio; listening."
	}
	onset, wav := m.detector.pushFrame(msg.pcm, speech)
	if onset {
		if m.busy && m.status.Capabilities.Interruptions {
			m.interrupted = true
			if m.player != nil {
				m.player.cancel()
			}
			if m.turnCancel != nil && m.status.Capabilities.InterruptionMode != "drain" {
				m.turnCancel()
			}
		}
		m.notice = "Listening - continue your thought..."
		if m.interrupted && m.status.Capabilities.InterruptionMode == "drain" {
			m.notice = "Listening - old native turn is finishing silently."
		}
	}
	if len(wav) > 0 {
		combined, err := mergeUtterancesLimit(m.pendingVoice, wav, m.detector.recordingSeconds())
		if err != nil {
			m.notice = err.Error()
			return nil
		}
		m.pendingVoice = combined
		if cmd := m.submitPendingVoice(); cmd != nil {
			return cmd
		}
		m.notice = "Speech recorded; waiting for the current turn to finish."
		return nil
	}

	return nil
}
