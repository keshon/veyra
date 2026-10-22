// Package httpapi adapts the local HTTP interface to a conversation session.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/keshon/veyra/internal/audio"
	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/language"
	"github.com/keshon/veyra/internal/web"
)

type Metadata struct {
	User         string         `json:"user,omitempty"`
	Warnings     []string       `json:"warnings,omitempty"`
	Character    string         `json:"character,omitempty"`
	Introduction string         `json:"introduction,omitempty"`
	Generation   map[string]any `json:"generation,omitempty"`
}

func Handler(s *conversation.Session, output string, contextSize int) http.Handler {
	return HandlerConfigured(s, output, contextSize, Metadata{})
}
func HandlerConfigured(s *conversation.Session, output string, contextSize int, metadata Metadata) http.Handler {
	mux := http.NewServeMux()
	registerDuplex(mux, s, output)
	mux.Handle("GET /", web.Handler())
	mux.HandleFunc("POST /api/compact", func(w http.ResponseWriter, r *http.Request) {
		saved, err := s.Compact(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(saved)
	})
	mux.HandleFunc("GET /api/conversations", func(w http.ResponseWriter, r *http.Request) {
		items, err := s.Saved()
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	})
	mux.HandleFunc("POST /api/conversations/resume", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ID string `json:"id"`
		}
		if err := decode(w, r, &input); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		saved, err := s.Resume(r.Context(), input.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(saved)
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		status := s.Status()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"state": status.State, "has_history": status.HasHistory, "detail": status.Detail, "context": contextSize, "context_usage": status.Context, "preferences": status.Preferences, "active_turn": status.ActiveTurn, "reference": status.Reference, "capabilities": s.Capabilities(), "prompts": s.Prompts(), "setup": metadata})
	})
	mux.HandleFunc("POST /api/preferences", func(w http.ResponseWriter, r *http.Request) {
		var p language.Preferences
		if err := decode(w, r, &p); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := s.UpdatePreferences(p); err != nil {
			writeError(w, err)
			return
		}
		w.Write([]byte("{}"))
	})
	mux.HandleFunc("POST /api/prompt", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			ID     string `json:"id"`
			Custom string `json:"custom"`
		}
		if err := decode(w, r, &p); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := s.SetPromptOverride(r.Context(), p.ID, p.Custom); err != nil {
			writeError(w, err)
			return
		}
		w.Write([]byte("{}"))
	})
	mux.HandleFunc("POST /api/interrupted", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			TurnID uint64 `json:"turn_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "invalid turn", 400)
			return
		}
		if err := s.MarkInterrupted(input.TurnID); err != nil {
			writeError(w, err)
			return
		}
		w.Write([]byte("{}"))
	})
	mux.HandleFunc("POST /api/cancel", func(w http.ResponseWriter, r *http.Request) { s.Cancel(); w.Write([]byte("{}")) })
	mux.HandleFunc("POST /api/voice-reference", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1000000))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := s.SetReference(r.Context(), data); err != nil {
			writeError(w, err)
			return
		}
		w.Write([]byte("{}"))
	})
	mux.HandleFunc("POST /api/recover", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Recover(r.Context()); err != nil {
			writeError(w, err)
			return
		}
		w.Write([]byte("{}"))
	})
	mux.HandleFunc("POST /api/reset", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Reset(r.Context()); err != nil {
			writeError(w, err)
			return
		}
		w.Write([]byte("{}"))
	})
	respond := func(w http.ResponseWriter, r *http.Request, input conversation.Input) {
		started := time.Now()
		chunks := 0
		first := int64(0)
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
		err := s.Respond(r.Context(), input, func(ev conversation.Event) error {
			value := map[string]any{"turn_id": ev.TurnID, "sequence": ev.Sequence, "kind": ev.Kind}
			if ev.Language != "" {
				value["language"] = ev.Language
			}
			switch ev.Kind {
			case "context":
				value["context_usage"] = ev.Context
			case "phase":
				value["phase"] = ev.Phase
			case "transcript":
				value["transcript"] = ev.Text
			case "text", "warning":
				value["text"] = ev.Text
			case "audio":
				name := fmt.Sprintf("turn-%d-%d.wav", ev.TurnID, ev.Sequence)
				if err := os.WriteFile(filepath.Join(output, name), ev.Audio, 0600); err != nil {
					return err
				}
				chunks++
				if chunks == 1 {
					first = time.Since(started).Milliseconds()
				}
				value["audio"] = "/audio/" + name
			case "done":
				value["done"] = true
				value["first_audio_ms"] = first
				value["elapsed_ms"] = time.Since(started).Milliseconds()
				value["audio_chunks"] = chunks
			}
			return send(value)
		})
		if err != nil {
			if streamed {
				_ = send(map[string]any{"error": err.Error()})
			} else {
				writeError(w, err)
			}
		}
	}
	mux.HandleFunc("POST /api/turn", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, audio.MaxInputBytes))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		respond(w, r, conversation.Input{Audio: data})
	})
	mux.HandleFunc("POST /api/regenerate", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			TurnID uint64 `json:"turn_id"`
		}
		if err := decode(w, r, &input); err != nil || input.TurnID == 0 {
			http.Error(w, "provide a valid turn_id", 400)
			return
		}
		respond(w, r, conversation.Input{Regenerate: input.TurnID})
	})
	mux.HandleFunc("POST /api/text", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Text string `json:"text"`
		}
		if err := decode(w, r, &input); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		respond(w, r, conversation.Input{Text: input.Text})
	})
	mux.Handle("GET /audio/", http.StripPrefix("/audio/", http.FileServer(http.Dir(output))))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		ip := net.ParseIP(host)
		if err != nil || !(host == "localhost" || ip != nil && ip.IsLoopback()) {
			http.Error(w, "loopback host required", 403)
			return
		}
		if r.Method == "POST" {
			origin := r.Header.Get("Origin")
			if origin != "" && origin != "http://"+r.Host || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				http.Error(w, "cross-origin request", 403)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected a single JSON object")
	}
	return nil
}
func writeError(w http.ResponseWriter, err error) {
	code := 400
	if errors.Is(err, conversation.ErrBusy) {
		code = 409
	}
	if errors.Is(err, conversation.ErrNotReady) {
		code = 503
	}
	http.Error(w, err.Error(), code)
}
