package terminal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeardRequiresExplicitDiagnostics(t *testing.T) {
	m := &model{}
	if cmd := m.heard(""); cmd != nil || !strings.Contains(m.notice, "-voice-diagnostics") {
		t.Fatal("diagnostics enabled without opt-in")
	}
	m.diagnosticDir = t.TempDir()
	if cmd := m.heard("raw"); cmd != nil {
		t.Fatal("raw diagnostic admitted without microphone audio")
	}
	m.lastCapture = "recorded.wav"
	m.busy = true
	if cmd := m.heard(""); cmd != nil {
		t.Fatal("diagnostic competed with active turn")
	}
}
func TestCPUWhisperDiagnosticAcceptance(t *testing.T) {
	if os.Getenv("VEYRA_DIAGNOSTIC_TEST") != "1" {
		t.Skip("installed CPU recognition acceptance is opt-in")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	text, err := transcribeDiagnostic(context.Background(), root, filepath.Join(root, "lab/composed/results/20261002-111413/input-1-en-16k.wav"))
	if err != nil || text == "[no transcript detected]" {
		t.Fatalf("%q: %v", text, err)
	}
	t.Log(text)
}
