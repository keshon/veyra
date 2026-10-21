package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/keshon/veyra/internal/config"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in acceptance uses installed assets and the GPU, never a downloaded model.
func TestNativeService(t *testing.T) {
	if os.Getenv("VEYRA_NATIVE_TEST") != "1" {
		t.Skip("set VEYRA_NATIVE_TEST=1 for installed GPU acceptance")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"kokoro", "minicpm-native"} {
		t.Run(profile, func(t *testing.T) {
			settings, err := config.Load([]string{"-data-dir", t.TempDir(), "-root", root, "-profile", profile, "-listen", "127.0.0.1:8096", "-model-port", "19095", "-asr-port", "19096", "-character", "profiles/characters/veyra.json", "-temp", "0.2"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			ended := make(chan error, 1)
			go func() { ended <- Run(ctx, settings) }()
			defer func() {
				cancel()
				select {
				case err := <-ended:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(10 * time.Second):
					t.Error("service shutdown exceeded 10 seconds")
					return
				}
				for _, port := range []int{8096, 19095, 19096} {
					listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
					if err != nil {
						t.Errorf("owned port %d remains occupied after shutdown: %v", port, err)
					} else {
						listener.Close()
					}
				}
			}()
			client := &http.Client{Timeout: 3 * time.Minute}
			base := "http://" + settings.Listen
			started := time.Now()
			for {
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				default:
				}
				req, _ := http.NewRequestWithContext(ctx, "GET", base+"/api/status", nil)
				resp, err := client.Do(req)
				if err == nil {
					var status struct {
						State, Detail string
						Setup         struct{ Character, Introduction string }
					}
					json.NewDecoder(resp.Body).Decode(&status)
					resp.Body.Close()
					if status.State == "error" {
						t.Fatal(status.Detail)
					}
					if status.State == "ready" {
						if status.Setup.Character != "Veyra" || status.Setup.Introduction == "" {
							t.Fatal("character metadata missing")
						}
						break
					}
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Logf("%s ready in %s", profile, time.Since(started))
			inputs := []string{"af_heart", "af_bella", "bm_george", "recorded-input"}
			if profile == "minicpm-native" {
				inputs = []string{"native-first", "native-second"}
			}
			for _, voice := range inputs {
				path, kind := "/api/text", "application/json"
				body := []byte(`{"text":"What is two plus three? Answer briefly."}`)
				if profile == "kokoro" && voice != "recorded-input" {
					p := []byte(fmt.Sprintf(`{"reply":"en","fallback":"en","voices":{"en":%q}}`, voice))
					req, _ := http.NewRequestWithContext(ctx, "POST", base+"/api/preferences", bytes.NewReader(p))
					req.Header.Set("Content-Type", "application/json")
					r, e := client.Do(req)
					if e != nil {
						t.Fatal(e)
					}
					b, _ := io.ReadAll(r.Body)
					r.Body.Close()
					if r.StatusCode != 200 {
						t.Fatalf("voice: %s", b)
					}
				} else {
					path = "/api/turn"
					kind = "audio/wav"
					body, err = os.ReadFile(filepath.Join(root, "lab/experiments/voice/results/composed-input.wav"))
					if err != nil {
						t.Fatal(err)
					}
				}
				req, _ := http.NewRequestWithContext(ctx, "POST", base+path, bytes.NewReader(body))
				req.Header.Set("Content-Type", kind)
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				if resp.StatusCode != http.StatusOK {
					message, _ := io.ReadAll(resp.Body)
					resp.Body.Close()
					t.Fatalf("turn HTTP %d: %s", resp.StatusCode, message)
				}
				decoder := json.NewDecoder(resp.Body)
				audioCount := 0
				done := false
				text := ""
				for {
					var e struct{ Kind, Text, Audio, Error string }
					err = decoder.Decode(&e)
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if e.Error != "" {
						t.Fatal(e.Error)
					}
					text += e.Text
					if e.Kind == "done" {
						done = true
					}
					if e.Audio != "" {
						audioCount++
						r, err := client.Get(base + e.Audio)
						if err != nil {
							t.Fatal(err)
						}
						wav, _ := io.ReadAll(r.Body)
						r.Body.Close()
						if len(wav) < 44 || string(wav[:4]) != "RIFF" {
							t.Fatal("invalid output WAV")
						}
					}
				}
				resp.Body.Close()
				if !done || audioCount == 0 || text == "" {
					t.Fatalf("incomplete %s: audio=%d done=%v text=%s", voice, audioCount, done, text)
				}
				t.Logf("%s / %s: %d WAV chunks; %s", profile, voice, audioCount, text)
			}
		})
	}
}
