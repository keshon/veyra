package llamacpp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/keshon/veyra/internal/conversation"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRejectIncompleteGeneration(t *testing.T) {
	for _, body := range []string{"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", "data: invalid\n\n", "data: {\"error\":\"broken\"}\n\n", "data: {\"choices\":[{\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n"} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		err := New(s.URL).Generate(context.Background(), nil, func(string) error { return nil })
		s.Close()
		if err == nil {
			t.Fatal("incomplete stream accepted", body)
		}
	}
}

func TestConfiguredSamplingReachesModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		for key, want := range map[string]float64{"temperature": 0, "top_p": 0.75, "repeat_penalty": 1.2, "max_tokens": 320} {
			if payload[key] != want {
				t.Errorf("%s=%v, want %v", key, payload[key], want)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	err := NewConfigured(server.URL, Sampling{Temperature: 0, TopP: 0.75, RepeatPenalty: 1.2, MaxTokens: 320}).Generate(context.Background(), nil, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
}

func TestInterruptionMetadataIsNotAssistantDialogue(t *testing.T) {
	messages := []conversation.Message{{Role: "system", Text: "You are Veyra."}, {Role: "user", Text: "Tell a story."}, {Role: "assistant", Text: "Once upon a time", Interrupted: true}, {Role: "user", Text: "Wait."}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role    string
				Content string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload.Messages) != len(messages) {
			t.Fatal("messages lost")
		}
		for i, m := range payload.Messages {
			if strings.Contains(m.Content, "[Previous answer was interrupted]") {
				t.Error("synthetic assistant marker leaked")
			}
			if i > 0 && m.Content != messages[i].Text {
				t.Error("dialogue was modified")
			}
		}
		if !strings.Contains(payload.Messages[0].Content, "may not have been heard") {
			t.Error("system lacks interruption context")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	if err := New(server.URL).Generate(context.Background(), messages, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestPostHistoryReachesGenerationAfterUserInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []map[string]string `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload.Messages) != 3 || payload.Messages[1]["content"] != "Question" || payload.Messages[2]["content"] != "Final directive" {
			t.Error(payload.Messages)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	messages := []conversation.Message{{Role: "system", Text: "Base", PostHistory: "Final directive"}, {Role: "user", Text: "Question"}}
	if err := New(server.URL).Generate(context.Background(), messages, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
