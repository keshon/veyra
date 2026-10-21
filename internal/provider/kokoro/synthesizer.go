// Package kokoro owns a resident sherpa-onnx CPU speech worker.
package kokoro

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os/exec"
	"sync"

	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/process"
)

// kokoro owns one resident, serial CPU worker. Cancellation terminates that
// worker; the next turn starts a fresh worker instead of reading stale output.
type Synthesizer struct {
	voices                 []Voice
	mu                     sync.Mutex
	parent                 context.Context
	binary, library, model string
	log                    io.Writer
	cancel                 context.CancelFunc
	input                  io.WriteCloser
	output                 io.ReadCloser
	done                   chan error
}

func (k *Synthesizer) stop() {
	if k.cancel != nil {
		k.cancel()
		<-k.done
		k.input.Close()
		k.output.Close()
		k.cancel = nil
	}
}
func (k *Synthesizer) Close() { k.mu.Lock(); defer k.mu.Unlock(); k.stop() }
func (k *Synthesizer) start() error {
	if k.cancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(k.parent)
	cmd := exec.CommandContext(ctx, k.binary, k.library, k.model)
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		cancel()
		return err
	}
	cmd.Stderr = k.log
	k.input = input
	k.output = output
	k.cancel = cancel
	k.done = make(chan error, 1)
	go func() { k.done <- process.Run(cmd) }()
	return nil
}
func (k *Synthesizer) Synthesize(ctx context.Context, text, language, id string) ([]byte, error) {
	if language != "en" {
		return nil, fmt.Errorf("installed Kokoro bundle supports English only")
	}
	speaker, err := speaker(k.voices, id)
	if err != nil {
		return nil, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if k.cancel != nil {
		select {
		case <-k.done:
			k.cancel()
			k.input.Close()
			k.output.Close()
			k.cancel = nil
		default:
		}
	}
	fresh := k.cancel == nil
	if err := k.start(); err != nil {
		return nil, err
	}
	finished := make(chan struct{})
	cancelWorker := k.cancel
	go func() {
		select {
		case <-ctx.Done():
			select {
			case <-finished:
				return
			default:
				cancelWorker()
			}
		case <-finished:
		}
	}()
	defer close(finished)
	fail := func(err error) ([]byte, error) {
		k.stop()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if fresh {
		var magic uint32
		if err := binary.Read(k.output, binary.LittleEndian, &magic); err != nil {
			return fail(err)
		}
		if magic != 0x4b4f4b4f {
			return fail(fmt.Errorf("invalid Kokoro ready message"))
		}
	}
	if len(text) == 0 || len(text) > 8192 {
		return nil, fmt.Errorf("invalid phrase length")
	}
	if err := binary.Write(k.input, binary.LittleEndian, uint32(len(text))); err != nil {
		return fail(err)
	}
	if err := binary.Write(k.input, binary.LittleEndian, speaker); err != nil {
		return fail(err)
	}
	if _, err := io.WriteString(k.input, text); err != nil {
		return fail(err)
	}
	var header [2]uint32
	if err := binary.Read(k.output, binary.LittleEndian, &header); err != nil {
		return fail(err)
	}
	rate, n := header[0], header[1]
	if rate < 8000 || rate > 48000 || n == 0 || n > rate*60 {
		return fail(fmt.Errorf("invalid Kokoro audio header"))
	}
	samples := make([]byte, int(n)*4)
	if _, err := io.ReadFull(k.output, samples); err != nil {
		return fail(err)
	}
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	wav := make([]byte, 44+int(n)*2)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], rate)
	binary.LittleEndian.PutUint32(wav[28:], rate*2)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], n*2)
	for i := 0; i < int(n); i++ {
		f := math.Float32frombits(binary.LittleEndian.Uint32(samples[i*4:]))
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return fail(fmt.Errorf("nonfinite Kokoro sample"))
		}
		f = max(-1, min(1, f))
		binary.LittleEndian.PutUint16(wav[44+i*2:], uint16(int16(f*32767)))
	}
	return wav, nil
}

func New(ctx context.Context, binary, library, model string, voices []Voice, log io.Writer) *Synthesizer {
	return &Synthesizer{parent: ctx, binary: binary, library: library, model: model, voices: append([]Voice(nil), voices...), log: log}
}
func (k *Synthesizer) Voices() []conversation.Voice {
	voices := make([]conversation.Voice, len(k.voices))
	for i, v := range k.voices {
		voices[i] = v.Voice
	}
	return voices
}
