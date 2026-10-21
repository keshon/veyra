package minicpm

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/keshon/veyra/internal/conversation"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Duplex follows the native TDM protocol: one second of prefill, then one
// bounded decode decision. Capture and Token2Wav playback remain independent.
func (a *Client) Duplex(ctx context.Context, input <-chan conversation.DuplexInput, emit func(conversation.Event) error) (result error) {
	if !a.duplex || a.failed {
		return errors.New("native duplex is unavailable; restart with -duplex")
	}
	// Never abandon a native operation on browser disconnect. Complete its bounded
	// cycle, then reset under admission before making the model available again.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := a.call(cleanup, "/v1/stream/break", map[string]any{"reason": "duplex_stopped"}); err != nil {
			a.failed = true
			result = err
			return
		}
		if err := a.Reset(cleanup); err != nil {
			a.failed = true
			result = err
		}
	}()
	if err := emit(conversation.Event{Kind: "phase", Phase: "listening"}); err != nil {
		return nil
	}
	sent := map[string][32]byte{}
	flush := func() error {
		for _, file := range wavFiles(filepath.Join(a.output, "tts_wav")) {
			data, err := os.ReadFile(file.path)
			if err != nil || len(data) < 44 || string(data[:4]) != "RIFF" || int(binary.LittleEndian.Uint32(data[4:8]))+8 != len(data) {
				continue
			}
			hash := sha256.Sum256(data)
			if previous, ok := sent[file.path]; ok && previous == hash {
				continue
			}
			sent[file.path] = hash
			if err := emit(conversation.Event{Kind: "audio", Audio: data, Language: "en"}); err != nil {
				return err
			}
		}
		return nil
	}
	// Poll async TTS even during silence / a blocked model cycle.
	audioErr := make(chan error, 1)
	audioCtx, stopAudio := context.WithCancel(ctx)
	audioDone := make(chan struct{})
	go func() {
		defer close(audioDone)
		ticker := time.NewTicker(60 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-audioCtx.Done():
				return
			case <-ticker.C:
				if err := flush(); err != nil {
					select {
					case audioErr <- err:
					default:
					}
					return
				}
			}
		}
	}()
	defer func() { stopAudio(); <-audioDone }()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-audioErr:
			if ctx.Err() != nil {
				return nil
			}
			return err
		case chunk, ok := <-input:
			if !ok {
				return nil
			}
			cycle, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := func() error {
				if chunk.Interrupt {
					return a.call(cycle, "/v1/stream/break", map[string]any{"reason": "user_interrupt"})
				}
				file, err := os.CreateTemp(a.output, "duplex-input-*.wav")
				if err != nil {
					return err
				}
				name := file.Name()
				defer os.Remove(name)
				_, err = file.Write(chunk.Audio)
				closeErr := file.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
				if err := a.call(cycle, "/v1/stream/prefill", map[string]any{"audio_path_prefix": name, "cnt": a.count}); err != nil {
					return err
				}
				a.count++
				response, err := a.post(cycle, "/v1/stream/decode", map[string]any{"stream": true, "debug_dir": a.output})
				if err != nil {
					return err
				}
				defer response.Body.Close()
				scanner := bufio.NewScanner(response.Body)
				scanner.Buffer(make([]byte, 4096), 1<<20)
				done := false
				for scanner.Scan() {
					line := scanner.Text()
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					data := strings.TrimPrefix(line, "data: ")
					if data == "[DONE]" {
						done = true
						break
					}
					var event struct {
						Content  string `json:"content"`
						IsListen bool   `json:"is_listen"`
						Error    any    `json:"error"`
					}
					if err := json.Unmarshal([]byte(data), &event); err != nil {
						return err
					}
					if event.Error != nil {
						return fmt.Errorf("native duplex: %v", event.Error)
					}
					if ctx.Err() != nil {
						continue
					}
					if event.Content != "" {
						if err := emit(conversation.Event{Kind: "text", Text: event.Content}); err != nil {
							return err
						}
					}
					if event.IsListen {
						if err := emit(conversation.Event{Kind: "phase", Phase: "listening"}); err != nil {
							return err
						}
					}
				}
				if scanner.Err() != nil {
					return scanner.Err()
				}
				if !done {
					return errors.New("native duplex stream ended without DONE")
				}
				return nil
			}()
			cancel()
			if err != nil {
				return err
			}
		}
	}
}
