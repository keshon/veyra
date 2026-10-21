package terminal

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/keshon/veyra/internal/process"
	"github.com/keshon/veyra/internal/provider/vad"
)

const microphoneRate = 16000
const microphoneFrame = 320 // 20 ms, PCM16 mono

type liveCapture struct {
	root       string
	neural     bool
	ctx        context.Context
	cancel     context.CancelFunc
	microphone string
}
type microphoneMsg struct {
	capture *liveCapture
	pcm     []byte
}
type classifiedMicrophoneMsg struct {
	microphoneMsg
	speech bool
}
type captureFinishedMsg struct {
	capture *liveCapture
	err     error
}

func prepareLive(ctx context.Context, microphone string) (*liveCapture, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("live terminal capture requires Windows DirectShow")
	}
	if strings.TrimSpace(microphone) == "" {
		return nil, fmt.Errorf("select a microphone with /mic DEVICE")
	}
	ctx, cancel := context.WithCancel(ctx)
	return &liveCapture{ctx: ctx, cancel: cancel, microphone: microphone}, nil
}
func (c *liveCapture) run(emit func([]byte, *bool)) error {
	var classifier *vad.Client
	if c.neural {
		var err error
		classifier, err = vad.Start(c.ctx, c.root)
		if err != nil {
			return err
		}
		defer classifier.Close()
	}
	var vadErr error
	cmd := exec.CommandContext(c.ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "dshow", "-audio_buffer_size", "50", "-probesize", "32", "-analyzeduration", "0", "-i", "audio="+c.microphone, "-ac", "1", "-ar", "16000", "-f", "s16le", "-flush_packets", "1", "pipe:1")
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic

	cmd.Stdout = &microphoneWriter{emit: func(pcm []byte) {
		if vadErr != nil {
			return
		}
		if classifier == nil {
			emit(pcm, nil)
			return
		}
		speech, err := classifier.Speech(pcm)
		if err != nil {
			if c.ctx.Err() != nil {
				return
			}
			vadErr = err
			c.cancel()
			return
		}
		emit(pcm, &speech)
	}}
	err := process.Run(cmd)
	if vadErr != nil {
		return fmt.Errorf("speech detector stopped: %w", vadErr)
	}
	if c.ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("live microphone: %w: %s", err, strings.TrimSpace(diagnostic.String()))
	}
	return fmt.Errorf("microphone stream ended")
}

// exec waits for its stdout copy goroutine, so complete frames are delivered before shutdown.
type microphoneWriter struct {
	pending []byte
	emit    func([]byte)
}

func (w *microphoneWriter) Write(data []byte) (int, error) {
	n := len(data)
	w.pending = append(w.pending, data...)
	for len(w.pending) >= microphoneFrame*2 {
		frame := append([]byte(nil), w.pending[:microphoneFrame*2]...)
		w.pending = w.pending[microphoneFrame*2:]
		w.emit(frame)
	}
	return n, nil
}

// detector owns turn boundaries and pre-roll in the terminal update loop.
type detector struct {
	pre, parts                     []byte
	candidate, quiet, voiced       int
	candidateGap, candidateElapsed int
	active                         bool
	noise, level                   float64
	floor                          float64
	startSamples                   int
	endSamples, maxSeconds         int
}

func (d *detector) reset() {
	noise := d.noise
	*d = detector{noise: noise, floor: d.floor, startSamples: d.startSamples, endSamples: d.endSamples, maxSeconds: d.maxSeconds}
	if d.noise == 0 {
		d.noise = .003
	}
}
func (d *detector) push(pcm []byte) (bool, []byte) { return d.pushFrame(pcm, nil) }
func (d *detector) pushFrame(pcm []byte, classified *bool) (bool, []byte) {
	if len(pcm) == 0 || len(pcm)%2 != 0 {
		return false, nil
	}
	if d.noise == 0 {
		d.noise = .003
	}
	samples := len(pcm) / 2
	energy := 0.0
	for i := 0; i < len(pcm); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[i:]))) / 32768
		energy += v * v
	}
	rms := math.Sqrt(energy / float64(samples))
	d.level = rms
	threshold := d.threshold()
	if d.active {
		threshold = max(threshold*0.6, d.noise*2)
	} // Retain quieter syllables after confirmed onset.
	speech := rms > threshold
	if classified != nil {
		speech = *classified
	}
	if !d.active {
		if classified == nil && !speech && rms < d.threshold()*0.5 {
			d.noise = .99*d.noise + .01*min(rms, .01)
		}
		d.pre = append(d.pre, pcm...)
		preBytes := 25600
		if classified != nil {
			preBytes = 38400
		} // 1.2 seconds protects quiet words before neural activation.
		if len(d.pre) > preBytes {
			d.pre = append([]byte(nil), d.pre[len(d.pre)-preBytes:]...)
		}
		if speech {
			d.candidate += samples
			d.candidateGap = 0
		} else if d.candidate > 0 {
			d.candidateGap += samples
		}
		if d.candidate > 0 {
			d.candidateElapsed += samples
		}
		// Speech includes consonants and short gaps; do not require one long vowel.
		// Bound the evidence window to the retained pre-roll.
		if d.candidateGap >= microphoneRate*80/1000 || d.candidateElapsed > microphoneRate*800/1000 {
			d.candidate, d.candidateGap, d.candidateElapsed = 0, 0, 0
		}
		if d.candidate < d.onsetSamples() {
			return false, nil
		}
		d.active = true
		d.parts = d.pre
		d.pre = nil
		d.voiced = d.candidate
		return true, nil
	}
	d.parts = append(d.parts, pcm...)
	if speech {
		d.quiet = 0
		d.voiced += samples
	} else {
		d.quiet += samples
	}
	if d.quiet < d.endpointSamples() && len(d.parts) < microphoneRate*d.recordingSeconds()*2 {
		return false, nil
	}
	parts := d.parts
	valid := d.voiced >= d.onsetSamples() // Keep every utterance that was long enough to interrupt.
	d.reset()
	if !valid {
		return false, nil
	}
	return false, pcmWAV(parts)
}
func pcmWAV(pcm []byte) []byte {
	wav := make([]byte, 44+len(pcm))
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], microphoneRate)
	binary.LittleEndian.PutUint32(wav[28:], microphoneRate*2)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(pcm)))
	copy(wav[44:], pcm)
	return wav
}
func mergeUtterances(first, second []byte) ([]byte, error) {
	return mergeUtterancesLimit(first, second, 29)
}

func mergeUtterancesLimit(first, second []byte, seconds int) ([]byte, error) {
	if len(first) == 0 {
		return second, nil
	}
	if len(first) < 44 || len(second) < 44 {
		return nil, fmt.Errorf("invalid pending voice input")
	}
	if len(first)+len(second)-88 > microphoneRate*seconds*2 {
		return nil, fmt.Errorf("pending speech exceeded %d seconds; earlier audio kept, latest segment not added", seconds)
	}
	pcm := append(append([]byte(nil), first[44:]...), second[44:]...)
	return pcmWAV(pcm), nil
}

type recoveredMsg struct{ err error }

func (m *model) stopLive() {
	m.liveWanted = false
	if m.capture != nil {
		m.capture.cancel()
		m.capture = nil
	}
	m.detector.reset()
	m.pendingVoice = nil
}
func (m *model) toggleLive(arg string) tea.Cmd {
	switch arg {
	case "", "on", "off":
	default:
		m.notice = "Use /live on or /live off."
		return nil
	}
	if arg == "off" || arg == "" && m.liveWanted {
		m.stopLive()
		m.notice = "Hands-free off."
		return nil
	}
	if m.recording != nil {
		m.notice = "Finish or discard the recording first."
		return nil
	}
	m.liveWanted = true
	return m.listen()
}
func (m *model) listen() tea.Cmd {
	if m.capture != nil {
		return nil
	}
	if m.status.State != "ready" || m.recovering || m.busy {
		m.notice = "Hands-free will start when the service is ready."
		return nil
	}
	if strings.TrimSpace(m.microphone) == "" {
		m.liveWanted = false
		m.notice = "Choose a microphone, then Ctrl+L for hands-free."
		return m.command("/devices")
	}
	c, err := prepareLive(m.ctx, m.microphone)
	if err != nil {
		m.liveWanted = false
		m.notice = err.Error()
		return nil
	}
	c.root, c.neural = m.captureRoot, m.vadMode == "silero"
	m.capture = c
	m.lastMicrophoneFrame = time.Time{}
	m.detector.reset()
	m.notice = "Opening microphone; wait for audio frames before speaking. Use headphones."
	return func() tea.Msg {
		err := c.run(func(pcm []byte, speech *bool) {
			if speech == nil {
				m.send(microphoneMsg{c, pcm})
			} else {
				m.send(classifiedMicrophoneMsg{microphoneMsg{c, pcm}, *speech})
			}
		})
		return captureFinishedMsg{c, err}
	}
}
func (m *model) recoverTurn() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()
		timer := time.NewTicker(50 * time.Millisecond)
		defer timer.Stop()
		for {
			status, err := m.client.status(ctx)
			if err == nil && status.State != "busy" && status.State != "loading" {
				return recoveredMsg{m.client.action(ctx, "/api/recover", map[string]any{})}
			}
			select {
			case <-ctx.Done():
				return recoveredMsg{ctx.Err()}
			case <-timer.C:
			}
		}
	}
}

// Admission is retried on both turn completion and service readiness: a status
// poll can report the old busy state after the turn has already finished.
func (m *model) submitPendingVoice() tea.Cmd {
	if len(m.pendingVoice) == 0 || m.busy || m.recovering || m.recording != nil || m.detector.active || m.status.State != "ready" {
		return nil
	}
	cmd := m.begin("", m.pendingVoice)
	if cmd != nil {
		m.pendingVoice = nil
	}
	return cmd
}

func (m *model) microphoneStatus() string {
	if m.capture == nil {
		return ""
	}
	if m.lastMicrophoneFrame.IsZero() {
		return " | Mic: opening; wait before speaking"
	}
	if time.Since(m.lastMicrophoneFrame) > 2*time.Second {
		return " | Mic: no audio frames; Ctrl+L off/on to reconnect"
	}
	state := "listening"
	if m.detector.active {
		state = fmt.Sprintf("speech detected %.1fs / %ds", float64(len(m.detector.parts))/(microphoneRate*2), m.detector.recordingSeconds())
	} else if len(m.pendingVoice) > 0 {
		state = "speech queued"
	}
	if m.vadMode == "silero" {
		return " | Mic: " + state + " | Silero VAD CPU"
	}
	return fmt.Sprintf(" | Mic: %s | level %.3f / trigger %.3f", state, m.detector.level, m.detector.threshold())
}

func (d *detector) threshold() float64 {
	floor := d.floor
	if floor == 0 {
		floor = .012
	}
	return max(floor, d.noise*3.5)
}
func (d *detector) onsetSamples() int {
	if d.startSamples == 0 {
		return 3200
	}
	return d.startSamples
}

func (d *detector) endpointSamples() int {
	if d.endSamples > 0 {
		return d.endSamples
	}
	return 17600
}
func (d *detector) recordingSeconds() int {
	if d.maxSeconds > 0 {
		return d.maxSeconds
	}
	return 120
}
