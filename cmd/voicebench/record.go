package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

//go:embed recording.html
var recordingPage string

func serveRecordingUI(root, address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return errors.New("record-ui must bind explicitly to 127.0.0.1")
	}
	directory := filepath.Join(root, "lab", "composed", "recordings")
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	token := make([]byte, 24)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	nonce := hex.EncodeToString(token)
	page := strings.ReplaceAll(recordingPage, "{{TOKEN}}", nonce)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, page)
	})
	mux.HandleFunc("POST /recordings", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Recording-Token") != nonce {
			http.Error(w, "invalid recording request", 403)
			return
		}
		language := r.Header.Get("X-Fixture-Language")
		if language != "en" && language != "ru" && language != "mixed" {
			http.Error(w, "select a fixture language", 400)
			return
		}
		text, err := url.QueryUnescape(r.Header.Get("X-Fixture-Text"))
		if err != nil || len(text) > 4096 {
			http.Error(w, "invalid reference text", 400)
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1_000_000))
		if err != nil {
			http.Error(w, "recording is too large", 413)
			return
		}
		if err := validRecording(data); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		suffix := make([]byte, 8)
		if _, err := rand.Read(suffix); err != nil {
			http.Error(w, "cannot create recording ID", 500)
			return
		}
		name := "human-" + language + "-" + hex.EncodeToString(suffix)
		path := filepath.Join(directory, name+".wav")
		if err := os.WriteFile(path, data, 0644); err != nil {
			http.Error(w, "cannot save recording", 500)
			return
		}
		metadata := map[string]string{"expected_language": language, "reference_text": text, "source": "user microphone", "created_at": time.Now().UTC().Format(time.RFC3339)}
		encoded, err := json.MarshalIndent(metadata, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(directory, name+".json"), encoded, 0644)
		}
		if err != nil {
			_ = os.Remove(path)
			http.Error(w, "cannot save recording metadata", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"path": path})
	})
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 35 * time.Second, WriteTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Println("Microphone fixture collector: http://" + address)
	fmt.Println("Recordings saved to " + directory)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func validRecording(data []byte) error {
	if len(data) < 44 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" || int(binary.LittleEndian.Uint32(data[4:8]))+8 != len(data) {
		return errors.New("send a complete PCM WAV")
	}
	format, audio := false, false
	for pos := 12; pos+8 <= len(data); {
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		begin := pos + 8
		if size > len(data)-begin {
			return errors.New("invalid WAV chunk")
		}
		if string(data[pos:pos+4]) == "fmt " && size >= 16 {
			f := data[begin : begin+size]
			format = binary.LittleEndian.Uint16(f) == 1 && binary.LittleEndian.Uint16(f[2:]) == 1 && binary.LittleEndian.Uint32(f[4:]) == 16000 && binary.LittleEndian.Uint16(f[14:]) == 16
		}
		if string(data[pos:pos+4]) == "data" {
			audio = size >= 3200 && size <= 960000 && size%2 == 0
		}
		pos = begin + size + size%2
	}
	if !format || !audio {
		return errors.New("expected mono 16 kHz PCM16 WAV, 0.1–30 seconds")
	}
	return nil
}
