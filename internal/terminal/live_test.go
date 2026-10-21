package terminal

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"

	tea "charm.land/bubbletea/v2"
	"github.com/keshon/veyra/internal/audio"
	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/process"
)

func pcmFrame(loud bool) []byte {
	frame := make([]byte, 640)
	if loud {
		for i := 0; i < len(frame); i += 2 {
			binary.LittleEndian.PutUint16(frame[i:], 2200)
		}
	}
	return frame
}
func TestClassifiedSpeechPreservesQuietOpeningAndNextTurn(t *testing.T) {
	d := detector{floor: .1, startSamples: 1600}
	for turn := 0; turn < 2; turn++ {
		var original, admitted []byte
		starts := 0
		for i := 0; i < 110; i++ {
			frame := pcmFrame(false)
			// The first words are below the old RMS floor and classification is delayed.
			if i < 45 {
				for j := 0; j < len(frame); j += 2 {
					binary.LittleEndian.PutUint16(frame[j:], uint16(100+i))
				}
			}
			original = append(original, frame...)
			speech := i >= 30 && i < 45
			onset, wav := d.pushFrame(frame, &speech)
			if onset {
				starts++
			}
			if wav != nil {
				admitted = wav
			}
		}
		if starts != 1 || len(admitted) < 44 {
			t.Fatalf("turn %d missing: starts=%d", turn, starts)
		}
		if !bytes.Contains(admitted[44:], original[:45*640]) {
			t.Fatal("quiet first words lost before classification")
		}
	}
}
func TestDetectorRetainsPausesAndRejectsNoise(t *testing.T) {
	var d detector
	starts := 0
	var turns [][]byte
	push := func(n int, loud bool) {
		for i := 0; i < n; i++ {
			start, wav := d.push(pcmFrame(loud))
			if start {
				starts++
			}
			if wav != nil {
				turns = append(turns, wav)
			}
		}
	}
	push(20, false)
	push(5, true)
	push(60, false)
	if starts != 0 || len(turns) != 0 {
		t.Fatal("short noise was admitted")
	}
	push(25, true)
	push(35, false)
	push(25, true)
	push(56, false)
	if starts != 1 || len(turns) != 1 {
		t.Fatalf("starts=%d turns=%d", starts, len(turns))
	}
	if err := audio.ValidateInput(turns[0]); err != nil {
		t.Fatal(err)
	}
	if len(turns[0]) < 16000*2*2 {
		t.Fatal("mid-sentence pause or words were lost")
	}
	push(25, true)
	push(56, false)
	if starts != 2 || len(turns) != 2 {
		t.Fatal("second utterance missing")
	}
}
func TestLiveInterruptionPoliciesAndPendingCorrection(t *testing.T) {
	for _, mode := range []string{"cancel", "drain"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			playCtx, stop := context.WithCancel(ctx)
			capture := &liveCapture{ctx: ctx, cancel: func() {}}
			m := &model{ctx: context.Background(), input: textinput.New(), viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(10)), width: 80, busy: true, turnCancel: cancel, player: &playback{ctx: playCtx, cancel: stop}, capture: capture, status: serviceStatus{State: "busy", Capabilities: conversation.Capabilities{Interruptions: true, InterruptionMode: mode}}, assistant: -1}
			for i := 0; i < 25; i++ {
				m.Update(microphoneMsg{capture, pcmFrame(true)})
			}
			if !m.interrupted || playCtx.Err() == nil {
				t.Fatal("speech did not stop playback")
			}
			if (ctx.Err() != nil) != (mode == "cancel") {
				t.Fatal("wrong native cancellation policy")
			}
			for i := 0; i < 56; i++ {
				m.Update(microphoneMsg{capture, pcmFrame(false)})
			}
			if len(m.pendingVoice) == 0 {
				t.Fatal("correction disappeared while old turn was active")
			}
			if err := audio.ValidateInput(m.pendingVoice); err != nil {
				t.Fatal(err)
			}
			// Recovery and native drain both keep the complete correction until it can be admitted.
			first := append([]byte(nil), m.pendingVoice...)
			second := pcmWAV(pcmFrame(true))
			merged, err := mergeUtterances(first, second)
			if err != nil {
				t.Fatal(err)
			}
			if len(merged) != len(first)+len(second)-44 {
				t.Fatal("merged correction lost samples")
			}
		})
	}
}
func TestStoppedCaptureCannotSendStaleUtterance(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := &liveCapture{ctx: ctx, cancel: cancel}
	m := &model{input: textinput.New(), capture: nil}
	for i := 0; i < 100; i++ {
		m.Update(microphoneMsg{old, pcmFrame(true)})
	}
	if m.detector.active || len(m.pendingVoice) > 0 {
		t.Fatal("stopped capture admitted input")
	}
}

func TestMicrophoneWriterPreservesSplitFrames(t *testing.T) {
	input := append(pcmFrame(true), pcmFrame(false)...)
	var result []byte
	writer := &microphoneWriter{emit: func(pcm []byte) { result = append(result, pcm...) }}
	for offset := 0; offset < len(input); {
		end := min(len(input), offset+113)
		if _, err := writer.Write(input[offset:end]); err != nil {
			t.Fatal(err)
		}
		offset = end
	}
	if !bytes.Equal(input, result) || len(writer.pending) != 0 {
		t.Fatal("split native PCM packets lost samples")
	}
}

func TestFFmpegSyntheticCapturePreservesFrames(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	samples := 0
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=16000:duration=0.4", "-ac", "1", "-ar", "16000", "-f", "s16le", "-flush_packets", "1", "pipe:1")
	cmd.Stdout = &microphoneWriter{emit: func(pcm []byte) { samples += len(pcm) / 2 }}
	if err := process.Run(cmd); err != nil {
		t.Fatal(err)
	}
	if samples != 6400 {
		t.Fatalf("native capture delivered %d samples, want 6400", samples)
	}
}

func TestQueuedNativeCorrectionIsSubmittedAfterDrain(t *testing.T) {
	expected := pcmWAV(append(pcmFrame(true), pcmFrame(false)...))
	var submitted []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/turn" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		submitted, _ = io.ReadAll(r.Body)
		fmt.Fprint(w, "{\"kind\":\"text\",\"text\":\"Correction received.\"}\n{\"kind\":\"done\"}\n")
	}))
	defer server.Close()
	m := &model{ctx: context.Background(), client: client{server.URL, server.Client()}, send: func(tea.Msg) {}, input: textinput.New(), viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(10)), width: 80, busy: true, interrupted: true, revision: 1, pendingVoice: expected, diagnosticDir: t.TempDir(), assistant: -1, status: serviceStatus{State: "busy", Capabilities: conversation.Capabilities{Interruptions: true, InterruptionMode: "drain"}}}
	_, cmd := m.Update(finishedMsg{revision: 1})
	if cmd == nil {
		t.Fatal("native correction not admitted after completion")
	}
	result := cmd().(finishedMsg)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !bytes.Equal(submitted, expected) {
		t.Fatal("queued native correction lost or changed audio")
	}
	saved, err := os.ReadFile(m.lastCapture)
	if err != nil || !bytes.Equal(saved, submitted) {
		t.Fatalf("diagnostic is not the submitted WAV: %v", err)
	}

	if len(m.pendingVoice) != 0 {
		t.Fatal("pending audio was resubmitted")
	}
}

// A status request started before turn completion can arrive after it.
// Audio ending in that window must be submitted when the next ready status arrives.
func TestPendingSpeechResumesOnReadyStatus(t *testing.T) {
	for _, mode := range []string{"cancel", "drain"} {
		t.Run(mode, func(t *testing.T) {
			expected := pcmWAV(pcmFrame(true))
			submitted := make(chan []byte, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				submitted <- b
				fmt.Fprint(w, "{\"kind\":\"done\"}\n")
			}))
			defer server.Close()
			caps := conversation.Capabilities{Interruptions: true, InterruptionMode: mode}
			m := &model{ctx: context.Background(), client: client{server.URL, server.Client()}, send: func(tea.Msg) {}, input: textinput.New(), viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(10)), width: 80, assistant: -1, pendingVoice: expected, status: serviceStatus{State: "busy", Capabilities: caps}}
			// Continued speech must prevent an early submission, even when ready.
			m.detector.active = true
			_, cmd := m.Update(statusMsg{status: serviceStatus{State: "ready", Capabilities: caps}})
			if cmd != nil || m.busy || len(m.pendingVoice) == 0 {
				t.Fatal("submitted before the speaker finished")
			}
			m.detector.active = false
			_, cmd = m.Update(statusMsg{status: serviceStatus{State: "ready", Capabilities: caps}})
			if cmd == nil || !m.busy {
				t.Fatal("ready status stranded pending speech")
			}
			result := cmd().(finishedMsg)
			if result.err != nil {
				t.Fatal(result.err)
			}
			if !bytes.Equal(<-submitted, expected) {
				t.Fatal("submitted audio changed")
			}
			m.Update(result)
			m.Update(statusMsg{status: serviceStatus{State: "ready", Capabilities: caps}})
			if len(submitted) != 0 || len(m.pendingVoice) != 0 {
				t.Fatal("speech submitted twice")
			}
		})
	}
}

func TestShortInterruptionIsNotDiscardedAfterOnset(t *testing.T) {
	var d detector
	for i := 0; i < 10; i++ {
		d.push(pcmFrame(true))
	}
	if !d.active {
		t.Fatal("200 ms speech did not trigger an interruption")
	}
	var wav []byte
	for i := 0; i < 55; i++ {
		_, wav = d.push(pcmFrame(false))
	}
	if len(wav) == 0 {
		t.Fatal("accepted interruption was silently discarded")
	}
	if err := audio.ValidateInput(wav); err != nil {
		t.Fatal(err)
	}
}

func TestNativeDrainFinishingBeforeCorrectionEnds(t *testing.T) {
	submitted := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		submitted <- b
		fmt.Fprint(w, "{\"kind\":\"done\"}\n")
	}))
	defer server.Close()
	caps := conversation.Capabilities{Interruptions: true, InterruptionMode: "drain"}
	capture := &liveCapture{cancel: func() {}}
	m := &model{ctx: context.Background(), client: client{server.URL, server.Client()}, send: func(tea.Msg) {}, input: textinput.New(), viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(10)), width: 80, assistant: -1, revision: 1, busy: true, capture: capture, status: serviceStatus{State: "busy", Capabilities: caps}}
	for i := 0; i < 25; i++ {
		m.Update(microphoneMsg{capture, pcmFrame(true)})
	}
	m.Update(finishedMsg{revision: 1})
	// A poll in flight returns the state from before the completed turn.
	m.Update(statusMsg{status: serviceStatus{State: "busy", Capabilities: caps}})
	for i := 0; i < 55; i++ {
		_, cmd := m.Update(microphoneMsg{capture, pcmFrame(false)})
		if cmd != nil {
			t.Fatal("submitted while service reported busy")
		}
	}
	if len(m.pendingVoice) == 0 || m.detector.active {
		t.Fatal("correction was not queued at its endpoint")
	}
	expected := append([]byte(nil), m.pendingVoice...)
	_, cmd := m.Update(statusMsg{status: serviceStatus{State: "ready", Capabilities: caps}})
	if cmd == nil {
		t.Fatal("correction stranded after native drain")
	}
	result := cmd().(finishedMsg)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !bytes.Equal(<-submitted, expected) {
		t.Fatal("correction audio changed")
	}
}

func TestPendingSpeechWaitsForPlaybackAndRecovery(t *testing.T) {
	for _, block := range []string{"playback", "recovery", "recording"} {
		t.Run(block, func(t *testing.T) {
			m := &model{input: textinput.New(), pendingVoice: pcmWAV(pcmFrame(true)), status: serviceStatus{State: "ready"}}
			switch block {
			case "playback":
				m.busy = true
			case "recovery":
				m.recovering = true
			case "recording":
				m.recording = &recording{}
			}
			_, cmd := m.Update(statusMsg{status: serviceStatus{State: "ready"}})
			if cmd != nil || len(m.pendingVoice) == 0 {
				t.Fatal("pending speech lost or admitted before safe handoff")
			}
		})
	}
}

func TestConfiguredNoiseGatePreservesQuietBeginning(t *testing.T) {
	d := detector{floor: .02, startSamples: 4800}
	quiet := make([]byte, 640)
	for i := 0; i < len(quiet); i += 2 {
		binary.LittleEndian.PutUint16(quiet[i:], 400)
	}
	// 400 ms below the floor never starts a turn, but remains in pre-roll.
	for i := 0; i < 20; i++ {
		onset, wav := d.push(quiet)
		if onset || wav != nil {
			t.Fatal("below-floor noise triggered")
		}
	}
	for i := 0; i < 5; i++ {
		if onset, _ := d.push(pcmFrame(true)); onset {
			t.Fatal("100 ms movement burst triggered")
		}
	}
	d.push(pcmFrame(false))
	// A quiet first syllable followed by confirmed speech must not be cut away.
	for i := 0; i < 10; i++ {
		d.push(quiet)
	}
	for i := 0; i < 15; i++ {
		d.push(pcmFrame(true))
	}
	if !d.active {
		t.Fatal("sustained speech was not detected")
	}
	var wav []byte
	for i := 0; i < 55; i++ {
		_, wav = d.push(pcmFrame(false))
	}
	if len(wav) == 0 || !bytes.Contains(wav[44:], bytes.Repeat(quiet, 10)) {
		t.Fatal("quiet leading syllable lost")
	}
	if d.floor != .02 || d.startSamples != 4800 {
		t.Fatal("reset lost gate configuration")
	}
}

func TestSpeechOnsetAllowsSyllableGaps(t *testing.T) {
	d := detector{floor: .025, startSamples: 5600}
	onsets := 0
	for group := 0; group < 4; group++ {
		for i := 0; i < 5; i++ {
			onset, _ := d.push(pcmFrame(true))
			if onset {
				onsets++
			}
		}
		for i := 0; i < 2; i++ {
			d.push(pcmFrame(false))
		}
	}
	if onsets != 1 || !d.active {
		t.Fatal("ordinary syllable gaps prevented activation")
	}
}

func TestQuietSpeechDoesNotRaiseNoiseThreshold(t *testing.T) {
	d := detector{floor: .025, startSamples: 5600}
	quietVoice := make([]byte, 640)
	for i := 0; i < len(quietVoice); i += 2 {
		binary.LittleEndian.PutUint16(quietVoice[i:], 600)
	}
	for i := 0; i < 500; i++ {
		d.push(quietVoice)
	}
	if d.threshold() > .025 {
		t.Fatalf("quiet voice raised trigger to %.3f", d.threshold())
	}
}

func TestLongSpeechRetainsOpeningAndDoesNotSplitAt29Seconds(t *testing.T) {
	d := detector{maxSeconds: 120, endSamples: microphoneRate * 2}
	speech := true
	var first []byte
	for i := 0; i < microphoneRate*40/microphoneFrame; i++ {
		_, wav := d.pushFrame(pcmFrame(true), &speech)
		if len(wav) > 0 {
			t.Fatal("long utterance split early")
		}
		if i == 0 {
			first = pcmFrame(true)
		}
	}
	speech = false
	var wav []byte
	for i := 0; i < 100; i++ {
		_, got := d.pushFrame(pcmFrame(false), &speech)
		if len(got) > 0 {
			wav = got
		}
	}
	if len(wav) < 44+microphoneRate*40*2 || !bytes.Equal(wav[44:44+len(first)], first) {
		t.Fatal("opening audio missing")
	}
	if err := audio.ValidateInput(wav); err != nil {
		t.Fatal(err)
	}
}
func TestPendingOverflowKeepsEarlierAudio(t *testing.T) {
	earlier := pcmWAV(make([]byte, microphoneRate*28*2))
	later := pcmWAV(make([]byte, microphoneRate*2*2))
	m := &model{capture: &liveCapture{}, pendingVoice: earlier, detector: detector{maxSeconds: 29}}
	m.detector.active = true
	m.detector.parts = later[44:]
	m.detector.voiced = 3200
	m.detector.quiet = 17600
	microphoneCommand(m, microphoneMsg{capture: m.capture, pcm: pcmFrame(false)}, nil)
	if !bytes.Equal(m.pendingVoice, earlier) {
		t.Fatal("earlier queued audio discarded")
	}
}
