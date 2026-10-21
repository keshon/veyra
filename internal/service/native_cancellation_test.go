package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/keshon/veyra/internal/config"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in integration proves the API/TUI recovery path against the patched GPU
// runtime. It uses saved audio; it never opens the microphone.
func TestPatchedNativeCancellation(t *testing.T) {
	if os.Getenv("VEYRA_CANCELLATION_TEST") != "1" {
		t.Skip("set VEYRA_CANCELLATION_TEST=1 for patched GPU acceptance")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := config.Load([]string{"-data-dir", t.TempDir(), "-root", root, "-profile", "minicpm-native", "-profile-file", filepath.Join(root, "lab/experiments/voice/diagnostics/cancellation/profile.json"), "-listen", "127.0.0.1:8097", "-model-port", "19891", "-character", "profiles/characters/veyra.json", "-temp", "0.2", "-max-tokens", "160"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	ended := make(chan error, 1)
	go func() { ended <- Run(ctx, settings) }()
	defer func() {
		cancel()
		select {
		case err := <-ended:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(15 * time.Second):
			t.Error("owned service failed to shut down")
		}
	}()
	client := &http.Client{Timeout: 2 * time.Minute}
	base := "http://" + settings.Listen
	status := func() (string, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", base+"/api/status", nil)
		r, e := client.Do(req)
		if e != nil {
			return "", e
		}
		defer r.Body.Close()
		var s struct{ State, Detail string }
		if e = json.NewDecoder(r.Body).Decode(&s); e != nil {
			return "", e
		}
		if s.State == "error" {
			return s.State, fmt.Errorf("service: %s", s.Detail)
		}
		return s.State, nil
	}
	for {
		state, e := status()
		if state == "error" {
			t.Fatal(e)
		}
		if state == "ready" {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(100 * time.Millisecond)
	}
	audio, err := os.ReadFile(filepath.Join(root, "lab/experiments/voice/results/composed-input.wav"))
	if err != nil {
		t.Fatal(err)
	}
	action := func(path string) {
		req, _ := http.NewRequestWithContext(ctx, "POST", base+path, bytes.NewBufferString("{}"))
		req.Header.Set("Content-Type", "application/json")
		r, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		if r.StatusCode != 200 {
			t.Fatalf("%s: %d %s", path, r.StatusCode, b)
		}
	}
	for round := 0; round < 3; round++ {
		req, _ := http.NewRequestWithContext(ctx, "POST", base+"/api/turn", bytes.NewReader(audio))
		req.Header.Set("Content-Type", "audio/wav")
		r, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		if r.StatusCode != 200 {
			b, _ := io.ReadAll(r.Body)
			r.Body.Close()
			t.Fatalf("turn: %s", b)
		}
		decoder := json.NewDecoder(r.Body)
		chunks := 0
		text := ""
		done := false
		interrupted := false
		for {
			var event struct{ Kind, Text, Audio, Error string }
			e = decoder.Decode(&event)
			if e == io.EOF {
				break
			}
			if e != nil {
				r.Body.Close()
				t.Fatal(e)
			}
			if event.Error != "" && !interrupted {
				r.Body.Close()
				t.Fatal(event.Error)
			}
			text += event.Text
			if event.Kind == "done" {
				done = true
			}
			if event.Audio != "" {
				chunks++
				wav, e := client.Get(base + event.Audio)
				if e != nil {
					t.Fatal(e)
				}
				b, _ := io.ReadAll(wav.Body)
				wav.Body.Close()
				if len(b) < 44 || string(b[:4]) != "RIFF" {
					t.Fatal("invalid output WAV")
				}
				if round == 0 && !interrupted {
					interrupted = true
					action("/api/cancel")
				}
			}
		}
		r.Body.Close()
		if round == 0 {
			if !interrupted {
				t.Fatal("never reached speech interruption")
			}
			for {
				state, _ := status()
				if state != "" && state != "busy" && state != "loading" {
					break
				}
				if ctx.Err() != nil {
					t.Fatal(ctx.Err())
				}
				time.Sleep(50 * time.Millisecond)
			}
			action("/api/recover")
			t.Log("interrupted active speech; recovered without reset")
		} else {
			if !done || chunks == 0 || text == "" {
				t.Fatalf("incomplete follow-up: done=%v chunks=%d text=%q", done, chunks, text)
			}
			t.Logf("follow-up %d: %d WAVs; %s", round, chunks, text)
		}
	}
}
