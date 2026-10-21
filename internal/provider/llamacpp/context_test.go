package llamacpp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keshon/veyra/internal/conversation"
)

func tokenizerFixture(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apply-template":
			var p struct {
				Messages           []struct{ Role, Content string }
				ChatTemplateKwargs map[string]any `json:"chat_template_kwargs"`
			}
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				t.Error(err)
				return
			}
			if p.ChatTemplateKwargs["enable_thinking"] != false {
				t.Error("template settings differ from generation")
			}
			text := "BOS assistant-prefix"
			for _, m := range p.Messages {
				text += " " + m.Role + " " + m.Content
			}
			json.NewEncoder(w).Encode(map[string]string{"prompt": text})
		case "/tokenize":
			var p struct {
				Content      string
				AddSpecial   bool `json:"add_special"`
				ParseSpecial bool `json:"parse_special"`
			}
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				t.Error(err)
				return
			}
			if !p.AddSpecial || !p.ParseSpecial {
				t.Error("special tokens omitted")
			}
			json.NewEncoder(w).Encode(map[string]any{"tokens": make([]int, len(strings.Fields(p.Content)))})
		default:
			t.Error("unexpected generation during budgeting", r.URL.Path)
		}
	}))
}

func TestContextPreservesInstructionsAndWholeRecentExchanges(t *testing.T) {
	server := tokenizerFixture(t)
	defer server.Close()
	c := NewConfigured(server.URL, Sampling{ContextTokens: 100, MaxTokens: 20}) // 16 prompt tokens.
	messages := []conversation.Message{{Role: "system", Text: "Veyra character"}}
	for i := 0; i < 5; i++ {
		messages = append(messages, conversation.Message{Role: "user", Text: fmt.Sprintf("question%d", i)}, conversation.Message{Role: "assistant", Text: fmt.Sprintf("answer%d", i)})
	}
	messages = append(messages, conversation.Message{Role: "user", Text: "Привет сегодня"})
	got, usage, err := c.Prepare(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}
	if usage.PromptTokens+usage.Reserved > usage.Limit || usage.DroppedMessages != 6 {
		t.Fatal(usage)
	}
	if len(got) != 6 || got[0] != messages[0] || got[1].Text != "question3" || got[len(got)-1] != messages[len(messages)-1] {
		t.Fatal(got)
	}
	if messages[1].Text != "question0" {
		t.Fatal("caller history mutated")
	}
}

func TestContextRejectsOversizeCharacterAndInput(t *testing.T) {
	server := tokenizerFixture(t)
	defer server.Close()
	c := NewConfigured(server.URL, Sampling{ContextTokens: 100, MaxTokens: 20})
	for _, messages := range [][]conversation.Message{
		{{Role: "system", Text: strings.Repeat("character ", 30)}, {Role: "user", Text: "hi"}},
		{{Role: "system", Text: "Veyra"}, {Role: "user", Text: strings.Repeat("input ", 30)}},
	} {
		if _, _, err := c.Prepare(context.Background(), messages); err == nil || !strings.Contains(err.Error(), "Increase -ctx") {
			t.Fatal(err)
		}
	}
}

func TestContextCancellationAndUnsupportedRuntimeFail(t *testing.T) {
	messages := []conversation.Message{{Role: "system", Text: "Veyra"}, {Role: "user", Text: "hello"}}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	c := NewConfigured(server.URL, Sampling{ContextTokens: 4096, MaxTokens: 256})
	if _, _, err := c.Prepare(context.Background(), messages); err == nil {
		t.Fatal("missing tokenizer accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.Prepare(ctx, messages); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestPostHistoryProjectionKeepsHistoryAndDirectiveOrder(t *testing.T) {
	messages := []conversation.Message{{Role: "system", Text: "Base", PostHistory: "Stay in character."}, {Role: "user", Text: "Hello"}, {Role: "assistant", Text: "Welcome"}, {Role: "user", Text: "Latest question"}}
	wire := nativeMessages(messages)
	if len(wire) != 5 || wire[3]["content"] != "Latest question" || wire[4]["role"] != "system" || wire[4]["content"] != "Stay in character." {
		t.Fatal(wire)
	}
	if len(messages) != 4 || messages[3].Role != "user" {
		t.Fatal("prompt projection changed history")
	}
}

func TestPostHistoryIncludedInFinalTokenBudget(t *testing.T) {
	server := tokenizerFixture(t)
	defer server.Close()
	c := NewConfigured(server.URL, Sampling{ContextTokens: 100, MaxTokens: 20})
	messages := []conversation.Message{{Role: "system", Text: "Base"}, {Role: "user", Text: "Question"}}
	_, base, err := c.Prepare(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}
	messages[0].PostHistory = "One two three four."
	kept, withPost, err := c.Prepare(context.Background(), messages)
	if err != nil || withPost.PromptTokens != base.PromptTokens+5 || kept[0].PostHistory == "" {
		t.Fatal(base, withPost, err)
	}
	messages[0].PostHistory = strings.Repeat("directive ", 30)
	if _, _, err := c.Prepare(context.Background(), messages); err == nil {
		t.Fatal("post-history escaped token budget")
	}
}
