package minicpm

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/language"
)

func TestCancelledNativeTurnRequiresSettlementBeforeRecovery(t *testing.T) {
	for _, settled := range []bool{false, true} {
		t.Run(fmt.Sprint(settled), func(t *testing.T) {
			out := t.TempDir()
			var decodes, breaks atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/stream/prefill":
					fmt.Fprint(w, `{"success":true}`)
				case "/v1/stream/decode":
					if decodes.Add(1) == 1 {
						fmt.Fprint(w, "data: {\"content\":\"Interrupted\"}\n\n")
						w.(http.Flusher).Flush()
						<-r.Context().Done()
						return
					}
					dir := filepath.Join(out, "round_001", "tts_wav")
					os.MkdirAll(dir, 0700)
					os.WriteFile(filepath.Join(dir, "wav_1000.wav"), wav(), 0600)
					os.WriteFile(filepath.Join(dir, "generation_done.flag"), nil, 0600)
					fmt.Fprint(w, "data: {\"content\":\"New reply\"}\n\ndata: [DONE]\n\n")
				case "/v1/stream/break":
					breaks.Add(1)
					fmt.Fprintf(w, `{"success":true,"settled":%t}`, settled)
				default:
					t.Errorf("unexpected reset or request: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			c := NewConfigured(out, server.URL, server.Client(), Config{SettledCancellation: true})
			c.count = 1
			turn := conversation.Turn{Input: conversation.Input{Audio: wav()}, Preferences: language.Preferences{Reply: "en"}}
			err := c.Respond(context.Background(), turn, func(e conversation.Event) error {
				if e.Kind == "text" {
					return context.Canceled
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || breaks.Load() != 1 {
				t.Fatal(err, breaks.Load())
			}
			if err := c.Recover(context.Background()); (err == nil) != settled {
				t.Fatal("unsafe recovery decision", err)
			}
			if c.failed == settled {
				t.Fatal("incorrect native failure state")
			}
			if settled {
				chunks := 0
				if err := c.Respond(context.Background(), turn, func(e conversation.Event) error {
					if e.Kind == "audio" {
						chunks++
					}
					return nil
				}); err != nil || chunks != 1 {
					t.Fatal(err, chunks)
				}
			}
		})
	}
}

func wav() []byte {
	b := make([]byte, 3244)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16000)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 3200)
	return b
}
func TestNativeInitializationAndStream(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "run", "out")
	dir := filepath.Join(out, "round_000", "tts_wav")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		json.NewDecoder(r.Body).Decode(&v)
		switch r.URL.Path {
		case "/health":
			w.Write([]byte(`{}`))
		case "/v1/stream/omni_init":
			if !strings.Contains(v["assistant_prompt"].(string), "Answer the user") {
				t.Error("missing instruction", v)
			}
			if v["n_predict"] != float64(160) {
				t.Error(v)
			}
			w.Write([]byte(`{"success":true}`))
		case "/v1/stream/prefill":
			if v["cnt"] != float64(1) {
				t.Error(v)
			}
			if _, err := os.ReadFile(v["audio_path_prefix"].(string)); err != nil {
				t.Error(err)
			}
			w.Write([]byte(`{"success":true}`))
		case "/v1/stream/decode":
			os.WriteFile(filepath.Join(dir, "wav_0.wav"), wav(), 0600)
			os.WriteFile(filepath.Join(dir, "generation_done.flag"), nil, 0600)
			w.Write([]byte("data: {\"content\":\"Hello\"}\n\ndata: [DONE]\n\n"))
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	c := New(root, out, server.URL, server.Client())
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	text := ""
	chunks := 0
	done := false
	err := c.Respond(context.Background(), conversation.Turn{Input: conversation.Input{Audio: wav()}, Preferences: language.Preferences{Reply: "en", Fallback: "en"}}, func(e conversation.Event) error {
		if e.Kind == "text" {
			text += e.Text
		}
		if e.Kind == "audio" {
			chunks++
		}
		if e.Kind == "done" {
			done = true
		}
		return nil
	})
	if err != nil || text != "Hello" || chunks != 1 || !done || c.count != 2 || c.round != 1 {
		t.Fatal(err, text, chunks, done, c.count, c.round)
	}
}
func TestNativeErrorsPropagate(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "missing model", 500) }))
	defer s.Close()
	c := New("", "", s.URL, s.Client())
	if err := c.call(context.Background(), "/v1/stream/omni_init", nil); err == nil || !strings.Contains(err.Error(), "missing model") {
		t.Fatal(err)
	}
}

func TestDrainedNativeTurnCanContinueWithoutReset(t *testing.T) {
	out := t.TempDir()
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		switch r.URL.Path {
		case "/v1/stream/prefill":
			count++
			if payload["cnt"] != float64(count) {
				t.Error("native counter lost", payload)
			}
			fmt.Fprint(w, `{"success":true}`)
		case "/v1/stream/decode":
			round := int(payload["round_idx"].(float64))
			dir := filepath.Join(out, fmt.Sprintf("round_%03d", round), "tts_wav")
			os.MkdirAll(dir, 0700)
			os.WriteFile(filepath.Join(dir, "wav_0.wav"), wav(), 0600)
			os.WriteFile(filepath.Join(dir, "generation_done.flag"), nil, 0600)
			fmt.Fprint(w, "data: {\"content\":\"Reply\"}\n\ndata: [DONE]\n\n")
		default:
			t.Error("unexpected native reset or request", r.URL.Path)
		}
	}))
	defer server.Close()
	c := New("", out, server.URL, server.Client())
	c.count = 1
	for i := 0; i < 2; i++ {
		err := c.Respond(context.Background(), conversation.Turn{Input: conversation.Input{Audio: wav()}, Preferences: language.Preferences{Reply: "en", Fallback: "en"}}, func(e conversation.Event) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	if c.failed || c.count != 3 || c.round != 2 {
		t.Fatal("draining invalidated native session")
	}
}
