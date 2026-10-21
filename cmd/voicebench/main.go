// voicebench is an experimental, file-driven composed-pipeline benchmark.
// It does not implement the assistant application or certify voice quality.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

type transcript struct {
	Result struct {
		Language string `json:"language"`
	} `json:"result"`
	Transcription []struct {
		Text string `json:"text"`
	} `json:"transcription"`
}

type chunk struct {
	Text             string  `json:"text"`
	Path             string  `json:"path"`
	ReadySeconds     float64 `json:"ready_seconds"`
	SynthesisSeconds float64 `json:"synthesis_seconds"`
	AudioSeconds     float64 `json:"audio_seconds"`
}

type report struct {
	Input             string  `json:"input"`
	DetectedLanguage  string  `json:"detected_language"`
	ReplyLanguage     string  `json:"reply_language"`
	Transcript        string  `json:"transcript"`
	Response          string  `json:"response"`
	ASRSeconds        float64 `json:"asr_seconds"`
	FirstTextSeconds  float64 `json:"first_text_seconds"`
	FirstAudioSeconds float64 `json:"first_audio_seconds"`
	GenerationSeconds float64 `json:"generation_seconds"`
	TotalSeconds      float64 `json:"total_seconds"`
	Chunks            []chunk `json:"chunks"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root := flag.String("root", ".", "project directory")
	input := flag.String("input", "", "mono 16 kHz PCM WAV input")
	out := flag.String("out", "lab/composed/results/turn", "new result directory")
	url := flag.String("llm", "http://127.0.0.1:19081", "local text generation server")
	reply := flag.String("reply-language", "auto", "auto, en or ru; auto follows this utterance")
	asrModel := flag.String("asr-model", "ggml-base.bin", "installed whisper model filename")
	threads := flag.Int("asr-threads", 4, "ASR CPU thread budget")
	asrURL := flag.String("asr-url", "", "persistent Whisper server; empty starts the CPU CLI")
	recordUI := flag.String("record-ui", "", "serve a local microphone fixture collector, e.g. 127.0.0.1:8092")
	flag.Parse()
	if *recordUI != "" {
		base, err := filepath.Abs(*root)
		if err != nil {
			return err
		}
		return serveRecordingUI(base, *recordUI)
	}
	if *input == "" {
		return errors.New("-input is required")
	}
	if *reply != "auto" && *reply != "en" && *reply != "ru" {
		return errors.New("unsupported reply-language")
	}
	base, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	assets := filepath.Join(base, "assets")
	inputPath, err := filepath.Abs(*input)
	if err != nil {
		return err
	}
	output, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	// Refuse reuse so stale audio or reports cannot masquerade as this run's result.
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("result directory must not exist: " + output)
	}
	if err := os.MkdirAll(output, 0755); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	started := time.Now()
	var tx transcript
	if *asrURL != "" {
		tx, err = transcribeHTTP(ctx, *asrURL, inputPath, output)
		if err != nil {
			return err
		}
	} else {
		asr := filepath.Join(assets, "runtimes", "whisper", "Release", "whisper-cli.exe")
		if _, err := os.Stat(asr); err != nil {
			asr, err = findExecutable(filepath.Join(assets, "runtimes", "whisper"), "whisper-cli.exe")
			if err != nil {
				return err
			}
		}
		asrOutput := filepath.Join(output, "transcription")
		cmd := exec.CommandContext(ctx, asr, "-m", filepath.Join(assets, "models", "whisper", *asrModel), "-f", inputPath, "-l", "auto", "-oj", "-of", asrOutput, "-t", fmt.Sprint(*threads), "-ng")
		hideProcess(cmd)
		logs, err := cmd.CombinedOutput()
		if writeErr := os.WriteFile(filepath.Join(output, "asr.log"), logs, 0644); writeErr != nil {
			return writeErr
		}
		if err != nil {
			return fmt.Errorf("ASR: %w; inspect %s", err, filepath.Join(output, "asr.log"))
		}
		data, err := os.ReadFile(asrOutput + ".json")
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &tx); err != nil {
			return err
		}
	}
	r := report{Input: inputPath, DetectedLanguage: tx.Result.Language, ASRSeconds: time.Since(started).Seconds()}
	for _, segment := range tx.Transcription {
		r.Transcript += segment.Text
	}
	r.Transcript = strings.TrimSpace(r.Transcript)
	if r.Transcript == "" {
		return errors.New("ASR returned no speech")
	}
	r.ReplyLanguage = *reply
	if r.ReplyLanguage == "auto" {
		r.ReplyLanguage = r.DetectedLanguage
	}
	voice := map[string]string{"en": "en_US-ljspeech-medium", "ru": "ru_RU-denis-medium"}[r.ReplyLanguage]
	if voice == "" {
		return fmt.Errorf("no tested voice for detected language %q", r.ReplyLanguage)
	}
	engine, err := findExecutable(filepath.Join(assets, "runtimes", "piper"), "piper.exe")
	if err != nil {
		return err
	}
	phrases := make(chan string, 2)
	type workerResult struct {
		chunks []chunk
		err    error
	}
	finished := make(chan workerResult, 1)
	go func() {
		result := workerResult{}
		for text := range phrases {
			if ctx.Err() != nil {
				result.err = ctx.Err()
				break
			}
			path := filepath.Join(output, fmt.Sprintf("speech-%03d.wav", len(result.chunks)+1))
			begin := time.Now()
			cmd := exec.CommandContext(ctx, engine, "--model", filepath.Join(assets, "models", "piper", voice+".onnx"), "--output_file", path)
			cmd.Stdin = strings.NewReader(text + "\n")
			hideProcess(cmd)
			log, err := cmd.CombinedOutput()
			if err != nil {
				result.err = fmt.Errorf("TTS: %w: %s", err, log)
				cancel()
				break
			}
			duration, err := wavDuration(path)
			if err != nil {
				result.err = err
				cancel()
				break
			}
			result.chunks = append(result.chunks, chunk{Text: text, Path: path, ReadySeconds: time.Since(started).Seconds(), SynthesisSeconds: time.Since(begin).Seconds(), AudioSeconds: duration})
		}
		finished <- result
	}()
	emit := func(text string) error {
		select {
		case phrases <- text:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var pending string
	generationStart := time.Now()
	err = generate(ctx, *url, r.Transcript, r.ReplyLanguage, func(text string) error {
		if r.FirstTextSeconds == 0 {
			r.FirstTextSeconds = time.Since(started).Seconds()
		}
		r.Response += text
		pending += text
		var ready []string
		ready, pending = splitPhrases(pending, false)
		for _, phrase := range ready {
			if err := emit(phrase); err != nil {
				return err
			}
		}
		return nil
	})
	r.GenerationSeconds = time.Since(generationStart).Seconds()
	if err == nil {
		ready, _ := splitPhrases(pending, true)
		for _, phrase := range ready {
			if e := emit(phrase); e != nil {
				err = e
				break
			}
		}
	} else {
		cancel()
	}
	close(phrases)
	worker := <-finished
	if worker.err != nil {
		return worker.err
	}
	if err != nil {
		return err
	}
	if len(worker.chunks) == 0 {
		return errors.New("model returned no speakable answer")
	}
	r.Chunks = worker.chunks
	r.FirstAudioSeconds = r.Chunks[0].ReadySeconds
	r.TotalSeconds = time.Since(started).Seconds()
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(output, "report.json"), b, 0644); err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func generate(ctx context.Context, url, text, language string, emit func(string) error) error {
	name := map[string]string{"en": "English", "ru": "Russian"}[language]
	payload := map[string]any{
		"messages": []map[string]string{
			{"role": "system", "content": "You are Veyra, a local voice assistant. Your name is Veyra. Answer in " + name + " using one or two short spoken sentences. No Markdown. Do not explain your reasoning. /no_think"},
			{"role": "user", "content": text + " /no_think"}},
		"stream": true, "max_tokens": 128, "temperature": 0.2,
		"chat_template_kwargs": map[string]any{"enable_thinking": false},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(url, "/")+"/v1/chat/completions", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("LLM: %s: %s", resp.Status, b)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 256*1024)
	complete := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if body == "[DONE]" {
			complete = true
			break
		}
		var event struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(body), &event); err != nil {
			return fmt.Errorf("invalid LLM event: %w", err)
		}
		if len(event.Error) > 0 && string(event.Error) != "null" {
			return fmt.Errorf("LLM stream error: %s", event.Error)
		}
		for _, choice := range event.Choices {
			if choice.Delta.Content != "" {
				if err := emit(choice.Delta.Content); err != nil {
					return err
				}
			}
			if choice.FinishReason != nil && *choice.FinishReason == "length" {
				return errors.New("LLM token limit reached; answer incomplete")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !complete {
		return errors.New("LLM stream ended without DONE")
	}
	return nil
}

// splitPhrases is deliberately limited to the short prose benchmark fixtures.
// Production phrase assembly will additionally handle abbreviations and code.
func splitPhrases(text string, flush bool) ([]string, string) {
	var ready []string
	start := 0
	for i, r := range text {
		width := utf8.RuneLen(r)
		next := i + width
		boundary := r == '\n' || ((r == '.' || r == '?' || r == '!') && next < len(text) && (text[next] == ' ' || text[next] == '\n'))
		if boundary {
			if p := strings.TrimSpace(text[start:next]); p != "" {
				ready = append(ready, p)
			}
			start = next
		}
	}
	remaining := text[start:]
	if flush {
		if p := strings.TrimSpace(remaining); p != "" {
			ready = append(ready, p)
		}
		return ready, ""
	}
	return ready, remaining
}

func findExecutable(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(d.Name(), name) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("missing %s under %s", name, root)
	}
	return found, nil
}

func wavDuration(path string) (float64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0, errors.New("invalid synthesized WAV")
	}
	var rate, size uint32
	for pos := 12; pos+8 <= len(b); {
		n := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		begin := pos + 8
		if n > len(b)-begin {
			return 0, errors.New("truncated synthesized WAV")
		}
		switch string(b[pos : pos+4]) {
		case "fmt ":
			if n >= 16 {
				rate = binary.LittleEndian.Uint32(b[begin+8 : begin+12])
			}
		case "data":
			size = uint32(n)
		}
		pos = begin + n + n%2
	}
	if rate == 0 || size == 0 {
		return 0, errors.New("empty synthesized WAV")
	}
	return float64(size) / float64(rate), nil
}
