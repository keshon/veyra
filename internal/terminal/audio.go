package terminal

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/keshon/veyra/internal/process"
)

type recording struct {
	command         *exec.Cmd
	input           io.WriteCloser
	directory, path string
	once            sync.Once
}

func prepareRecording(ctx context.Context, microphone string, limits ...int) (*recording, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("terminal capture currently requires Windows DirectShow")
	}
	if strings.TrimSpace(microphone) == "" {
		return nil, fmt.Errorf("set a microphone with /mic DEVICE or -microphone")
	}
	directory, err := os.MkdirTemp("", "veyra-recording-")
	if err != nil {
		return nil, err
	}
	seconds := 120
	if len(limits) > 0 && limits[0] > 0 {
		seconds = limits[0]
	}
	path := filepath.Join(directory, "input.wav")
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "dshow", "-i", "audio="+microphone, "-t", fmt.Sprint(seconds), "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", path)
	cmd.Stderr = io.Discard
	input, err := cmd.StdinPipe()
	if err != nil {
		os.RemoveAll(directory)
		return nil, err
	}
	return &recording{command: cmd, input: input, directory: directory, path: path}, nil
}
func (r *recording) stop() {
	r.once.Do(func() { _, _ = io.WriteString(r.input, "q\n"); _ = r.input.Close() })
}
func (r *recording) run() ([]byte, error) {
	defer os.RemoveAll(r.directory)
	defer r.input.Close()
	if err := process.Run(r.command); err != nil {
		return nil, fmt.Errorf("microphone %q: %w (check /devices)", r.command.Args[9], err)
	}
	return os.ReadFile(r.path)
}

type playback struct {
	ctx    context.Context
	cancel context.CancelFunc
	queue  chan []byte
	done   chan error
}

func newPlayback(ctx context.Context) *playback {
	ctx, cancel := context.WithCancel(ctx)
	p := &playback{ctx: ctx, cancel: cancel, queue: make(chan []byte, 2), done: make(chan error, 1)}
	go func() { p.done <- p.run() }()
	return p
}

// One raw PCM stream keeps the audio device open across generated WAV chunks.
// Format is validated locally; stream probing must not wait for future chunks.
func (p *playback) run() error {
	var first []byte
	select {
	case <-p.ctx.Done():
		return nil
	case wav, ok := <-p.queue:
		if !ok {
			return nil
		}
		first = wav
	}
	format, pcm, err := decodePlaybackWAV(first)
	if err != nil {
		return err
	}
	fadePlaybackStart(pcm, format)
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(p.ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffplay", "-nodisp", "-autoexit", "-loglevel", "error", "-probesize", "32", "-analyzeduration", "0", "-f", "s16le", "-ar", fmt.Sprint(format.rate), "-ac", fmt.Sprint(format.channels), "-i", "pipe:0")
	cmd.Stdin = reader
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	done := make(chan error, 1)
	go func() {
		err := process.Run(cmd)
		reader.Close() // Unblock writes if the player fails before consuming PCM.
		done <- err
	}()
	joined := false
	defer func() {
		cancel()
		writer.Close()
		if !joined {
			<-done
		}
	}()
	for {
		if _, err = writer.Write(pcm); err != nil {
			if p.ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("speaker stream: %w", err)
		}
		select {
		case <-p.ctx.Done():
			return nil
		case wav, ok := <-p.queue:
			if !ok {
				// FFplay auto-exits when decoded frames are consumed, before the
				// device necessarily drains. Trailing silence protects real samples.
				if _, err = writer.Write(make([]byte, int(format.rate*format.channels*2)*400/1000)); err != nil {
					if p.ctx.Err() != nil {
						return nil
					}
					return fmt.Errorf("speaker drain: %w", err)
				}
				writer.Close()
				err = <-done
				joined = true
				if p.ctx.Err() != nil {
					return nil
				}
				if err != nil {
					return fmt.Errorf("speaker playback: %w", err)
				}
				return nil
			}
			var next playbackFormat
			next, pcm, err = decodePlaybackWAV(wav)
			if err != nil {
				return err
			}
			if next != format {
				return fmt.Errorf("speech format changed during playback")
			}
		}
	}
}

type playbackFormat struct{ rate, channels uint32 }

// WAVs may contain metadata chunks; only sample bytes belong in the PCM stream.
func decodePlaybackWAV(wav []byte) (playbackFormat, []byte, error) {
	var format playbackFormat
	if len(wav) < 12 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || uint64(binary.LittleEndian.Uint32(wav[4:]))+8 != uint64(len(wav)) {
		return format, nil, fmt.Errorf("invalid playback WAV")
	}
	var pcm []byte
	for pos := 12; pos+8 <= len(wav); {
		size := int(binary.LittleEndian.Uint32(wav[pos+4:]))
		start := pos + 8
		if size > len(wav)-start {
			return format, nil, fmt.Errorf("truncated playback WAV")
		}
		chunk := wav[start : start+size]
		switch string(wav[pos : pos+4]) {
		case "fmt ":
			if size < 16 || binary.LittleEndian.Uint16(chunk) != 1 || binary.LittleEndian.Uint16(chunk[14:]) != 16 {
				return format, nil, fmt.Errorf("playback requires PCM16 WAV")
			}
			format = playbackFormat{binary.LittleEndian.Uint32(chunk[4:]), uint32(binary.LittleEndian.Uint16(chunk[2:]))}
		case "data":
			pcm = append(pcm, chunk...)
		}
		pos = start + size + size%2
	}
	if format.rate < 8000 || format.rate > 192000 || format.channels < 1 || format.channels > 2 || len(pcm) == 0 || len(pcm)%int(format.channels*2) != 0 {
		return format, nil, fmt.Errorf("invalid playback PCM format")
	}
	return format, pcm, nil
}
func (p *playback) enqueue(ctx context.Context, audio []byte) error {
	select {
	case <-p.ctx.Done():
		return nil
	default:
	}
	select {
	case <-p.ctx.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case err := <-p.done:
		p.done <- err
		if err == nil {
			return fmt.Errorf("playback worker exited")
		}
		return err
	case p.queue <- audio:
		return nil
	}
}

// Only the turn producer closes the queue, after it stops publishing audio.
func (p *playback) finish() error { close(p.queue); return <-p.done }
func (c client) audio(ctx context.Context, path string) ([]byte, error) {
	if !strings.HasPrefix(path, "/audio/turn-") || strings.Contains(path, "..") {
		return nil, fmt.Errorf("invalid service audio path")
	}
	resp, err := c.request(ctx, "GET", path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	audio, err := io.ReadAll(io.LimitReader(resp.Body, 6000001))
	if err != nil {
		return nil, err
	}
	if len(audio) > 6000000 {
		return nil, fmt.Errorf("audio exceeds playback limit")
	}
	return audio, nil
}

// Ramp the first 10 ms only: avoid a discontinuity when opening the audio device.
func fadePlaybackStart(pcm []byte, format playbackFormat) {
	frames := min(int(format.rate)/100, len(pcm)/(int(format.channels)*2))
	if frames < 2 {
		return
	}
	for frame := 0; frame < frames; frame++ {
		for channel := 0; channel < int(format.channels); channel++ {
			offset := (frame*int(format.channels) + channel) * 2
			sample := int16(binary.LittleEndian.Uint16(pcm[offset:]))
			binary.LittleEndian.PutUint16(pcm[offset:], uint16(int16(int(sample)*frame/(frames-1))))
		}
	}
}
