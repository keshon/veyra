package llamacpp

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/process"
)

// This opt-in protocol check loads installed models on CPU, without generation,
// microphone capture, recognition, speech synthesis or GPU allocation.
func TestInstalledContextTokenizer(t *testing.T) {
	if os.Getenv("VEYRA_CONTEXT_TEST") != "1" {
		t.Skip("set VEYRA_CONTEXT_TEST=1 for CPU-only installed tokenizer check")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Qwen3-1.7B-Q8_0.gguf", "Chronos-Gold-12B-1.0.i1-Q4_K_M.gguf"} {
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			runtime := filepath.Join(root, "assets/runtimes/llamacpp")
			child, err := process.Start(ctx, filepath.Join(runtime, "llama-server.exe"), runtime, []string{"--host", "127.0.0.1", "--port", strconv.Itoa(port), "-m", filepath.Join(root, "assets/models/qwen", name), "-c", "512", "-ngl", "0", "-t", "2", "--parallel", "1", "--jinja", "--no-warmup"}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			defer child.Stop()
			address := "http://127.0.0.1:" + strconv.Itoa(port)
			if err := child.Ready(ctx, address); err != nil {
				t.Fatal(err)
			}
			c := NewConfigured(address, Sampling{ContextTokens: 512, MaxTokens: 64})
			messages := []conversation.Message{{Role: "system", Text: "You are Veyra. Keep your answers brief."}}
			for i := 0; i < 8; i++ {
				messages = append(messages, conversation.Message{Role: "user", Text: strings.Repeat("Remember this earlier conversation. ", 25)}, conversation.Message{Role: "assistant", Text: strings.Repeat("I understand the earlier message. ", 25)})
			}
			messages = append(messages, conversation.Message{Role: "user", Text: "Hello. What is your name?"})
			kept, usage, err := c.Prepare(ctx, messages)
			if err != nil {
				t.Fatal(err)
			}
			if usage.PromptTokens+usage.Reserved > usage.Limit || usage.DroppedMessages == 0 || len(kept)%2 != 0 {
				t.Fatal(usage, len(kept))
			}
			n, err := c.countPrompt(ctx, kept)
			if err != nil || n != usage.PromptTokens {
				t.Fatal("reported token count mismatch", n, usage, err)
			}
			t.Logf("%s: %d prompt + %d reserved / %d; discarded %d exchanges", name, n, usage.Reserved, usage.Limit, usage.DroppedMessages/2)
		})
	}
}

func TestInstalledCompaction(t *testing.T) {
	if os.Getenv("VEYRA_COMPACTION_TEST") != "1" {
		t.Skip("set VEYRA_COMPACTION_TEST=1 for bounded CPU summary check")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	runtime := filepath.Join(root, "assets/runtimes/llamacpp")
	child, err := process.Start(ctx, filepath.Join(runtime, "llama-server.exe"), runtime, []string{"--host", "127.0.0.1", "--port", strconv.Itoa(port), "-m", filepath.Join(root, "assets/models/qwen/Qwen3-1.7B-Q8_0.gguf"), "-c", "2048", "-ngl", "0", "-t", "2", "--parallel", "1", "--jinja", "--no-warmup", "--reasoning-budget", "0"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Stop()
	address := "http://127.0.0.1:" + strconv.Itoa(port)
	if err := child.Ready(ctx, address); err != nil {
		t.Fatal(err)
	}
	c := NewConfigured(address, Sampling{ContextTokens: 2048, MaxTokens: 128})
	note, err := c.Compact(ctx, []conversation.Message{{Role: "user", Text: "My name is Megabyte. I enjoy the cartoon ReBoot. Please give short answers."}, {Role: "assistant", Text: "Hello Megabyte. We can discuss ReBoot."}, {Role: "user", Text: "We agreed to discuss the characters tomorrow."}, {Role: "assistant", Text: "Tomorrow we will discuss ReBoot characters."}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(note), "megabyte") || !strings.Contains(strings.ToLower(note), "reboot") {
		t.Fatal("explicit facts absent", note)
	}
	t.Log("Generated carry-over summary:", note)
}
