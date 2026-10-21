package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/keshon/veyra/internal/audio"
	"github.com/keshon/veyra/internal/conversation"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

type duplexConnection struct {
	token  string
	chunks chan conversation.DuplexInput
	cancel context.CancelFunc
	next   uint64
}

func registerDuplex(mux *http.ServeMux, s *conversation.Session, output string) {
	var mu sync.Mutex
	var active *duplexConnection
	mux.HandleFunc("POST /api/duplex", func(w http.ResponseWriter, r *http.Request) {
		if !s.Capabilities().FullDuplex {
			http.Error(w, "native continuous streaming is experimental", 400)
			return
		}
		tokenBytes := make([]byte, 16)
		if _, err := rand.Read(tokenBytes); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		connection := &duplexConnection{token: hex.EncodeToString(tokenBytes), chunks: make(chan conversation.DuplexInput, 3), cancel: cancel}
		mu.Lock()
		if active != nil {
			mu.Unlock()
			http.Error(w, "duplex connection already active", 409)
			return
		}
		active = connection
		mu.Unlock()
		defer func() {
			mu.Lock()
			if active == connection {
				active = nil
			}
			mu.Unlock()
		}()
		streamed := false
		encoder := json.NewEncoder(w)
		send := func(value any) error {
			if !streamed {
				w.Header().Set("Content-Type", "application/x-ndjson")
				w.Header().Set("Cache-Control", "no-store")
				streamed = true
			}
			if err := encoder.Encode(value); err != nil {
				return err
			}
			return http.NewResponseController(w).Flush()
		}
		first := true
		err := s.Duplex(ctx, connection.chunks, func(e conversation.Event) error {
			if first {
				first = false
				if err := send(map[string]any{"kind": "ready", "token": connection.token}); err != nil {
					return err
				}
			}
			value := map[string]any{"kind": e.Kind, "phase": e.Phase, "text": e.Text, "sequence": e.Sequence, "turn_id": e.TurnID}
			if e.Kind == "audio" {
				name := fmt.Sprintf("duplex-%d-%d.wav", e.TurnID, e.Sequence)
				if err := os.WriteFile(filepath.Join(output, name), e.Audio, 0600); err != nil {
					return err
				}
				value["audio"] = "/audio/" + name
			}
			return send(value)
		})
		if err != nil {
			if streamed {
				_ = send(map[string]any{"error": err.Error()})
			} else {
				writeError(w, err)
			}
		} else {
			_ = send(map[string]any{"done": true})
		}
	})
	mux.HandleFunc("POST /api/duplex/chunk", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32100))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := audio.ValidateInput(b); err != nil || len(b) != 32044 {
			http.Error(w, "expected exactly one second of mono 16 kHz PCM16 WAV", 400)
			return
		}
		seq, err := strconv.ParseUint(r.Header.Get("X-Sequence"), 10, 64)
		if err != nil {
			http.Error(w, "invalid audio sequence", 400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if active == nil || active.token != r.Header.Get("X-Duplex-Token") {
			http.Error(w, "duplex connection expired", 409)
			return
		}
		if seq != active.next {
			http.Error(w, "audio sequence mismatch", 409)
			return
		}
		select {
		case active.chunks <- conversation.DuplexInput{Audio: b}:
			active.next++
			w.Write([]byte("{}"))
		default:
			active.cancel()
			http.Error(w, "model cannot keep up with live audio; conversation stopped", 429)
		}
	})
	mux.HandleFunc("POST /api/duplex/stop", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if active != nil && active.token == r.Header.Get("X-Duplex-Token") {
			active.cancel()
		}
		w.Write([]byte("{}"))
	})
}
