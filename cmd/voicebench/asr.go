package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func transcribeHTTP(ctx context.Context, baseURL, input, output string) (transcript, error) {
	var tx transcript
	file, err := os.Open(input)
	if err != nil {
		return tx, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return tx, err
	}
	if info.Size() > 1_000_000 {
		return tx, fmt.Errorf("input exceeds benchmark audio limit")
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for name, value := range map[string]string{
		"language": "auto", "response_format": "verbose_json", "no_language_probabilities": "true",
		"no_timestamps": "true", "no_context": "true", "translate": "false", "beam_size": "5", "best_of": "5",
	} {
		if err := form.WriteField(name, value); err != nil {
			return tx, err
		}
	}
	part, err := form.CreateFormFile("file", "input.wav")
	if err != nil {
		return tx, err
	}
	if _, err := io.Copy(part, io.LimitReader(file, 1_000_001)); err != nil {
		return tx, err
	}
	if body.Len() > 1_010_000 {
		return tx, fmt.Errorf("input exceeds benchmark audio limit")
	}
	if err := form.Close(); err != nil {
		return tx, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(baseURL, "/")+"/inference", &body)
	if err != nil {
		return tx, err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	client := http.Client{Timeout: 2 * time.Minute}
	response, err := client.Do(req)
	if err != nil {
		return tx, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 256*1024))
	if err != nil {
		return tx, err
	}
	if response.StatusCode != http.StatusOK {
		return tx, fmt.Errorf("ASR server: %s: %s", response.Status, raw)
	}
	var native struct {
		Language string `json:"language"`
		Text     string `json:"text"`
	}
	if err := json.Unmarshal(raw, &native); err != nil {
		return tx, fmt.Errorf("invalid ASR response: %w", err)
	}
	if strings.TrimSpace(native.Text) == "" {
		return tx, fmt.Errorf("ASR returned no speech")
	}
	tx.Result.Language = map[string]string{"english": "en", "russian": "ru", "en": "en", "ru": "ru"}[strings.ToLower(native.Language)]
	if tx.Result.Language == "" {
		return tx, fmt.Errorf("unsupported ASR language %q", native.Language)
	}
	tx.Transcription = append(tx.Transcription, struct {
		Text string `json:"text"`
	}{Text: native.Text})
	if err := os.WriteFile(filepath.Join(output, "asr-response.json"), raw, 0644); err != nil {
		return tx, err
	}
	return tx, nil
}
