package config

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func writeGGUFFixture(t *testing.T, metadata map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model.gguf")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	write := func(v any) {
		if err := binary.Write(f, binary.LittleEndian, v); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = f.WriteString("GGUF")
	write(uint32(3))
	write(uint64(1))
	write(uint64(len(metadata)))
	text := func(s string) { write(uint64(len(s))); _, _ = f.WriteString(s) }
	for k, v := range metadata {
		text(k)
		write(uint32(8))
		text(v)
	}
	return path
}
func TestChatModelRejectsVoiceComponents(t *testing.T) {
	for _, name := range []string{"conversation", "Tts", "audio", "projector", "cache"} {
		t.Run(name, func(t *testing.T) {
			metadata := map[string]string{"general.architecture": "llama", "general.type": "model", "general.name": name, "tokenizer.ggml.model": "gpt2", "tokenizer.chat_template": "{{messages}}"}
			if name == "audio" {
				metadata["general.architecture"] = "whisper"
				delete(metadata, "tokenizer.ggml.model")
			}
			if name == "projector" {
				metadata["general.architecture"] = "projector"
			}
			if name == "cache" {
				delete(metadata, "general.architecture")
				delete(metadata, "tokenizer.chat_template")
			}
			err := CheckChatModel(writeGGUFFixture(t, metadata))
			if (err == nil) != (name == "conversation") {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "broken.gguf")
	_ = os.WriteFile(path, []byte("GGUF"), 0600)
	if CheckChatModel(path) == nil {
		t.Fatal("truncated model accepted")
	}
}
