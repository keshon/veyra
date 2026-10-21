package vad

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This acceptance test uses the installed native runtime and local recordings.
func TestInstalledSilero(t *testing.T) {
	if os.Getenv("VEYRA_VAD_TEST") != "1" {
		t.Skip("set VEYRA_VAD_TEST=1 to test installed assets")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(root, "var", "voice-diagnostics", "session-*", "turn-*.wav"))
	if err != nil || len(files) == 0 {
		t.Fatal("saved microphone recordings required", err)
	}
	for _, file := range files {
		t.Run(filepath.Base(filepath.Dir(file))+"/"+filepath.Base(file), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			c, err := Start(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			wav, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if len(wav) < 44 || string(wav[:4]) != "RIFF" || string(wav[36:40]) != "data" {
				t.Fatal("expected canonical PCM WAV")
			}
			count, first := 0, -1
			started := time.Now()
			for offset := 44; offset < len(wav); offset += 640 {
				frame := make([]byte, 640)
				copy(frame, wav[offset:min(offset+640, len(wav))])
				speech, err := c.Speech(frame)
				if err != nil {
					t.Fatal(err)
				}
				if speech {
					count++
					if first < 0 {
						first = (offset - 44) / 640
					}
				}
			}
			if count == 0 {
				t.Fatal("no speech detected")
			}
			t.Logf("first classified speech %.2fs; speech frames %d; processing %s", float64(first)*.02, count, time.Since(started))
			// Silence must release the classifier for the next utterance.
			for i := 0; i < 100; i++ {
				speech, err := c.Speech(make([]byte, 640))
				if err != nil {
					t.Fatal(err)
				}
				if i == 99 && speech {
					t.Fatal("speech stuck after silence")
				}
			}
			// A second, quieter utterance must be heard by the same persistent process.
			quietCount := 0
			for offset := 44; offset < len(wav); offset += 640 {
				frame := make([]byte, 640)
				copy(frame, wav[offset:min(offset+640, len(wav))])
				for j := 0; j < len(frame); j += 2 {
					sample := int16(binary.LittleEndian.Uint16(frame[j:]))
					binary.LittleEndian.PutUint16(frame[j:], uint16(sample/4))
				}
				speech, err := c.Speech(frame)
				if err != nil {
					t.Fatal(err)
				}
				if speech {
					quietCount++
				}
			}
			if quietCount == 0 {
				t.Fatal("quieter second utterance missed")
			}
			t.Logf("quarter-amplitude second utterance: %d speech frames", quietCount)
			cancel()
			closed := make(chan struct{})
			go func() { c.Close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("VAD shutdown blocked")
			}
		})
	}
}

func TestInstalledSileroRejectsNonSpeech(t *testing.T) {
	if os.Getenv("VEYRA_VAD_TEST") != "1" {
		t.Skip("set VEYRA_VAD_TEST=1 to test installed assets")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Start(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Loud, brief broadband bursts separated by silence simulate handling noise.
	seed := uint32(42)
	for i := 0; i < 200; i++ {
		frame := make([]byte, 640)
		if i%50 < 5 {
			for j := 0; j < len(frame); j += 2 {
				seed = 1664525*seed + 1013904223
				binary.LittleEndian.PutUint16(frame[j:], uint16(int16(seed>>16)/3))
			}
		}
		speech, err := c.Speech(frame)
		if err != nil {
			t.Fatal(err)
		}
		if speech {
			t.Fatalf("handling noise classified as speech at frame %d", i)
		}
	}
}
