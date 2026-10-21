package terminal

import (
	"bytes"
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/keshon/veyra/internal/process"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type diagnosticMsg struct {
	label, text, path string
	err               error
}

// Recognition is explicit, CPU-only and never fed back into the native session.
func (m *model) heard(arg string) tea.Cmd {
	if m.diagnosticDir == "" {
		m.notice = "Restart with -voice-diagnostics to enable /heard."
		return nil
	}
	if arg != "" && arg != "raw" {
		m.notice = "Use /heard or /heard raw."
		return nil
	}
	if m.busy || m.recovering || m.diagnosticBusy {
		m.notice = "Wait for the response or current diagnostic to finish."
		return nil
	}
	wavPath, label := m.lastCapture, "submitted input"
	var raw []byte
	if arg == "raw" {
		if len(m.recentPCM) < 3200 {
			m.notice = "No microphone frames captured yet."
			return nil
		}
		raw = pcmWAV(append([]byte(nil), m.recentPCM...))
		wavPath = filepath.Join(m.diagnosticDir, fmt.Sprintf("raw-%d.wav", time.Now().UnixNano()))
		label = "last ten seconds of raw capture"
	}
	if wavPath == "" {
		m.notice = "No submitted recording yet; try /heard raw immediately after speaking."
		return nil
	}
	root := m.diagnosticRoot
	m.diagnosticBusy = true
	m.notice = "Transcribing saved microphone audio with CPU Whisper..."
	return func() tea.Msg {
		if raw != nil {
			if err := os.WriteFile(wavPath, raw, 0600); err != nil {
				return diagnosticMsg{label: label, path: wavPath, err: err}
			}
		}
		text, err := transcribeDiagnostic(m.ctx, root, wavPath)
		return diagnosticMsg{label: label, path: wavPath, text: text, err: err}
	}
}
func transcribeDiagnostic(parent context.Context, root, wavPath string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(root, "assets", "runtimes", "whisper", "Release", "whisper-cli.exe"), "-m", filepath.Join(root, "assets", "models", "whisper", "ggml-small-q5_1.bin"), "-f", wavPath, "-t", "2", "-ng", "-nt", "-np", "-l", "auto")
	var out, diagnostic bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &diagnostic
	if err := process.Run(cmd); err != nil {
		return "", fmt.Errorf("CPU Whisper: %w: %s", err, strings.TrimSpace(diagnostic.String()))
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		text = "[no transcript detected]"
	}
	return text, nil
}
