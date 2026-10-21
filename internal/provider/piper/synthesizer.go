package piper

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/process"
)

type Voice struct {
	conversation.Voice
	Model   string
	Speaker *int
}
type Synthesizer struct {
	binary, directory string
	voices            []Voice
}

func New(binary, directory string, voices []Voice) *Synthesizer {
	copy := append([]Voice(nil), voices...)
	for i := range copy {
		if copy[i].Speaker != nil {
			n := *copy[i].Speaker
			copy[i].Speaker = &n
		}
	}
	return &Synthesizer{binary, directory, copy}
}
func (s *Synthesizer) Voices() []conversation.Voice {
	out := make([]conversation.Voice, 0, len(s.voices))
	for _, v := range s.voices {
		out = append(out, v.Voice)
	}
	return out
}
func (s *Synthesizer) selectVoice(language, id string) (Voice, error) {
	for _, v := range s.voices {
		if v.Language == language && (id == v.ID || id == "" && v.Default) {
			return v, nil
		}
	}
	return Voice{}, fmt.Errorf("no configured voice %q for language %q", id, language)
}
func (s *Synthesizer) Synthesize(ctx context.Context, text, language, id string) ([]byte, error) {
	voice, err := s.selectVoice(language, id)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(s.directory, "speech-")
	if err != nil {
		return nil, err
	}
	base, err := filepath.Abs(s.directory)
	if err != nil {
		return nil, err
	}
	target, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, fmt.Errorf("invalid speech directory")
	}
	defer os.RemoveAll(target)
	path := filepath.Join(dir, "reply.wav")
	args := []string{"--model", voice.Model, "--output_file", path}
	if voice.Speaker != nil {
		args = append(args, "--speaker", fmt.Sprint(*voice.Speaker))
	}
	cmd := exec.CommandContext(ctx, s.binary, args...)
	cmd.Stdin = strings.NewReader(text + "\n")
	var log bytes.Buffer
	cmd.Stdout = &log
	cmd.Stderr = &log
	err = process.Run(cmd)
	if err != nil {
		return nil, fmt.Errorf("Piper: %w: %s", err, log.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 44 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" || uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != uint64(len(data)) {
		return nil, fmt.Errorf("Piper returned invalid WAV")
	}
	return data, nil
}
