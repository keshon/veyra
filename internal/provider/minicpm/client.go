package minicpm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/keshon/veyra/internal/conversation"
)

type Client struct {
	instruction                         string
	duplex                              bool
	output, engineURL                   string
	client                              *http.Client
	count, round                        int
	failed                              bool
	settledCancellation                 bool
	reference                           string
	modelDir, ttsDir, originalReference string
	capabilities                        conversation.Capabilities
}

func New(root, output, address string, client *http.Client) *Client {
	dir := filepath.Join(root, "assets", "models", "minicpm")
	return NewConfigured(output, address, client, Config{ModelDir: dir, TTSDir: filepath.Join(dir, "tts"), Reference: filepath.Join(dir, "reference.wav"), Capabilities: conversation.Capabilities{Languages: []string{"en"}, Profile: "minicpm-native", Listening: "MiniCPM-o 4.5 · Whisper-medium-based encoder", Thinking: "MiniCPM-o 4.5 · llama.cpp-omni", Speaking: "MiniCPM-o 4.5 · native speech / Token2Wav", ReferenceConditioning: true}})
}

type Config struct {
	Duplex              bool
	SettledCancellation bool
	ModelDir            string
	TTSDir              string
	Reference           string
	Capabilities        conversation.Capabilities
}

func NewConfigured(output, address string, client *http.Client, config Config) *Client {
	config.Capabilities.FullDuplex = config.Duplex
	return &Client{duplex: config.Duplex, settledCancellation: config.SettledCancellation, output: output, engineURL: address, client: client, reference: config.Reference, originalReference: config.Reference, modelDir: config.ModelDir, ttsDir: config.TTSDir, capabilities: config.Capabilities}
}
func (a *Client) Capabilities() conversation.Capabilities {
	return a.capabilities
}
func (a *Client) post(ctx context.Context, path string, payload any) (*http.Response, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", a.engineURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("engine %s: %s %s", path, resp.Status, string(b))
	}
	return resp, nil
}
func (a *Client) call(ctx context.Context, path string, payload any) error {
	resp, err := a.post(ctx, path, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var v map[string]any
	if err = json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return err
	}
	if v["success"] != true {
		return fmt.Errorf("engine rejected %s: %v", path, v)
	}
	return nil
}

// ConfigurePrompt runs before model initialization, while the session owns admission.
func (a *Client) ConfigurePrompt(instruction string) { a.instruction = instruction }
func (a *Client) SetPrompt(ctx context.Context, instruction string) error {
	previous := a.instruction
	a.instruction = instruction
	if err := a.Reset(ctx); err != nil {
		a.instruction = previous
		a.failed = true
		return err
	}
	return nil
}
func (a *Client) config() map[string]any {
	instruction := a.instruction
	if instruction == "" {
		instruction = conversation.DefaultPrompts()[0].Instruction
	}
	prefix := "<|im_start|>system\nClone the voice in the provided audio prompt.\n<|audio_start|>"
	suffix := "<|audio_end|>Please assist users while maintaining this voice style. " + instruction + " Reply in English using one or two short spoken sentences. No long monologues.<|im_end|>\n<|im_start|>user\n"
	if a.duplex {
		prefix = "<|im_start|>system\nStreaming Duplex Conversation! " + instruction + "\n<|audio_start|>"
		suffix = "<|audio_end|><|im_end|>\n"
	}
	return map[string]any{"media_type": 1, "duplex_mode": a.duplex, "voice_audio": a.reference, "voice_clone_prompt": prefix, "assistant_prompt": suffix, "listen_prob_scale": 1.0, "force_listen_count": 3, "max_new_speak_tokens_per_chunk": 20, "sliding_window_mode": "basic"}
}

// SetReference keeps uploaded samples inside this run; changing voice resets native KV state.
func (a *Client) SetReference(ctx context.Context, recording []byte) error {
	if a.failed {
		return errors.New("MiniCPM native state failed; restart before changing the reference")
	}
	previous := a.reference
	next := a.originalReference
	if len(recording) > 0 {
		file, err := os.CreateTemp(a.output, "voice-reference-*.wav")
		if err != nil {
			return err
		}
		next = file.Name()
		_, err = file.Write(recording)
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(next)
			return err
		}
	}
	a.reference = next
	if err := a.Reset(ctx); err != nil {
		a.reference = previous
		a.failed = true // Native prompt/KV may have changed even when its response failed.
		if len(recording) > 0 {
			os.Remove(next)
		}
		return fmt.Errorf("reference update failed; restart native MiniCPM: %w", err)
	}
	if filepath.Dir(previous) == a.output && strings.HasPrefix(filepath.Base(previous), "voice-reference-") {
		os.Remove(previous)
	}
	return nil
}
func (a *Client) Initialize(ctx context.Context) error {
	deadline := time.Now().Add(3 * time.Minute)
	for {
		req, _ := http.NewRequestWithContext(ctx, "GET", a.engineURL+"/health", nil)
		resp, err := a.client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			return errors.New("MiniCPM readiness timeout")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	p := a.config()
	p["use_tts"] = true
	p["n_predict"] = 160 // omni_init has its own cap; it overrides the CLI --predict value.
	p["model_dir"] = a.modelDir
	p["tts_bin_dir"] = a.ttsDir
	p["tts_gpu_layers"] = 100
	p["token2wav_device"] = "gpu:0"
	if a.settledCancellation {
		// The patched Vulkan Token2Wav path produces corrupted audio.
		// Keep its lab profile on the slower, verified CPU synthesis path.
		p["token2wav_device"] = "cpu"
	}
	p["output_dir"] = a.output
	if err := a.call(ctx, "/v1/stream/omni_init", p); err != nil {
		return err
	}
	if a.settledCancellation {
		if err := a.call(ctx, "/v1/stream/prefill", map[string]any{"audio_path_prefix": a.reference, "img_path_prefix": "", "cnt": 0}); err != nil {
			return err
		}
	}
	a.count = 1
	return nil
}

type audioFile struct {
	path string
	n    int
}

func wavFiles(dir string) []audioFile {
	var out []audioFile
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), "wav_") && strings.HasSuffix(d.Name(), ".wav") {
			n, e := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(d.Name(), "wav_"), ".wav"))
			if e == nil {
				out = append(out, audioFile{path, n})
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].n < out[j].n })
	return out
}

func (a *Client) Respond(ctx context.Context, turn conversation.Turn, emit func(conversation.Event) error) error {
	if a.duplex {
		return errors.New("use continuous conversation when launched with -duplex")
	}
	completed := false
	defer func() {
		if !completed {
			a.failed = true
		}
	}()
	if len(turn.Input.Audio) == 0 {
		return errors.New("MiniCPM route requires audio input")
	}
	if turn.Preferences.Reply != "en" {
		return errors.New("this MiniCPM profile supports fixed English speech only")
	}
	b := turn.Input.Audio
	var turnError error
	var err error
	var stop context.CancelFunc
	send := func(e conversation.Event) {
		if turnError == nil {
			turnError = emit(e)
			if turnError != nil {
				if stop != nil {
					stop()
				}
			}
		}
	}
	file, err := os.CreateTemp(a.output, "input-*.wav")
	if err != nil {
		return err
	}
	input := file.Name()
	defer os.Remove(input)
	if err := file.Close(); err != nil {
		return err
	}
	if err = os.WriteFile(input, b, 0600); err != nil {
		return err
	}
	// Old runtimes cannot safely recover cancellation. The lab runtime explicitly
	// acknowledges joined workers before this session can accept another turn.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if a.settledCancellation {
		defer func() {
			if !completed && ctx.Err() != nil {
				cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
				defer stop()
				if a.settleCancellation(cleanup) == nil {
					completed = true
				}
			}
		}()
	}
	stop = cancel

	fail := func(err error) { turnError = err }
	started := time.Now()
	send(conversation.Event{Kind: "phase", Phase: "prefill"})
	if err = a.call(ctx, "/v1/stream/prefill", map[string]any{"audio_path_prefix": input, "img_path_prefix": "", "cnt": a.count}); err != nil {
		fail(err)
		return turnError
	}
	a.count++
	round := a.round
	a.round++
	dir := filepath.Join(a.output, fmt.Sprintf("round_%03d", round), "tts_wav")
	send(conversation.Event{Kind: "phase", Phase: "decode"})
	resp, err := a.post(ctx, "/v1/stream/decode", map[string]any{"stream": true, "round_idx": round, "debug_dir": a.output, "length_penalty": 1.0})
	if err != nil {
		fail(err)
		return turnError
	}
	defer resp.Body.Close()
	events := make(chan map[string]any, 32)
	decodeErr := make(chan error, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer close(events)
		s := bufio.NewScanner(resp.Body)
		s.Buffer(make([]byte, 4096), 1024*1024)
		done := false
		for s.Scan() {
			line := s.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				done = true
				break
			}
			var v map[string]any
			if e := json.Unmarshal([]byte(data), &v); e != nil {
				decodeErr <- e
				return
			}
			select {
			case events <- v:
			case <-ctx.Done():
				decodeErr <- ctx.Err()
				return
			}
		}
		err := s.Err()
		if err == nil && !done {
			err = errors.New("engine SSE ended without DONE")
		}
		decodeErr <- err
	}()
	defer func() { cancel(); _ = resp.Body.Close(); <-readerDone }()
	sent := map[string]bool{}
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	textDone := false
	var audioDeadline time.Time
	firstAudio := int64(-1)
	audioCount := 0
	flushAudio := func() {
		for _, f := range wavFiles(dir) {
			if sent[f.path] {
				continue
			}
			data, e := os.ReadFile(f.path)
			if e != nil || len(data) < 44 || string(data[:4]) != "RIFF" || int(binary.LittleEndian.Uint32(data[4:8]))+8 != len(data) {
				break
			}
			sent[f.path] = true
			audioCount++
			if firstAudio < 0 {
				firstAudio = time.Since(started).Milliseconds()
			}
			send(conversation.Event{Kind: "audio", Audio: data, Language: "en"})
		}
	}
	for {
		select {
		case v, ok := <-events:
			if !ok {
				events = nil
				textDone = true
				audioDeadline = time.Now().Add(90 * time.Second)
				if e := <-decodeErr; e != nil {
					fail(e)
					return turnError
				}
			} else if nativeError, ok := v["error"]; ok && nativeError != nil {
				fail(fmt.Errorf("MiniCPM stream error: %v", nativeError))
				return turnError
			} else if content, _ := v["content"].(string); content != "" {
				send(conversation.Event{Kind: "text", Text: content})
			}
		case <-ticker.C:
			flushAudio()
			if textDone {
				if _, e := os.Stat(filepath.Join(dir, "generation_done.flag")); e == nil {
					flushAudio()
					if audioCount == 0 {
						fail(errors.New("engine generated no speech"))
						return turnError
					}
					send(conversation.Event{Kind: "done"})
					completed = turnError == nil
					return turnError
				}
				if time.Now().After(audioDeadline) {
					fail(errors.New("TTS completion timeout"))
					return turnError
				}
			}
		case <-ctx.Done():
			fail(ctx.Err())
			return turnError
		}
	}
}

// settleCancellation requires the stronger acknowledgement supplied by the
// patched runtime. A legacy success response must never unlock recovery.
func (a *Client) settleCancellation(ctx context.Context) error {
	resp, err := a.post(ctx, "/v1/stream/break", map[string]any{"reason": "user_interrupt"})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var ack struct {
		Success bool `json:"success"`
		Settled bool `json:"settled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil {
		return err
	}
	if !ack.Success || !ack.Settled {
		return errors.New("MiniCPM cancellation did not acknowledge settled workers")
	}
	return nil
}

// Recover restores admission only after Respond has received a settlement ack.
// It deliberately does not reset native dialogue history.
func (a *Client) Recover(ctx context.Context) error {
	if !a.settledCancellation || a.failed {
		return errors.New("MiniCPM native state is not recoverable; restart the application")
	}
	return ctx.Err()
}

func (a *Client) Reset(ctx context.Context) error {
	if a.failed {
		return errors.New("MiniCPM turn failed or was canceled; restart the application to recover native state")
	}
	reset := func() error { return a.call(ctx, "/v1/stream/update_session_config", a.config()) }
	if a.settledCancellation {
		reset = func() error { return a.Initialize(ctx) }
	}
	if err := reset(); err != nil {
		return err
	}
	a.count = 1
	a.round = 0
	// Keep historical audio isolated by turn ID at the transport. Native rounds may be reused after reset.
	entries, err := os.ReadDir(a.output)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() && (strings.HasPrefix(e.Name(), "round_") || e.Name() == "tts_wav") {
			base, err := filepath.Abs(a.output)
			if err != nil {
				return err
			}
			target := filepath.Join(base, e.Name())
			rel, err := filepath.Rel(base, target)
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("invalid round path")
			}
			if err := os.RemoveAll(target); err != nil {
				return err
			}
		}
	}
	return nil
}
