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

func TestCompactionRequiresCompleteBoundedJSON(t *testing.T) {
	for _, output := range []string{`{"summary":"User is Alice; prefers concise replies. The earlier answer was interrupted."}`, `not JSON`, `{"summary":""}`} {
		t.Run(output, func(t *testing.T) {
			tokenizer := tokenizerFixture(t)
			defer tokenizer.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" {
					tokenizer.Config.Handler.ServeHTTP(w, r)
					return
				}
				var p struct {
					Messages    []struct{ Role, Content string }
					MaxTokens   int `json:"max_tokens"`
					Temperature float64
				}
				if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
					t.Error(err)
				}
				if p.MaxTokens != 256 || p.Temperature != 0 || !strings.Contains(p.Messages[1].Content, "previous_summary") {
					t.Error("summary call lacks bounds or prior note", p)
				}
				body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": output}}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", body)
			}))
			defer server.Close()
			c := NewConfigured(server.URL, Sampling{ContextTokens: 2048, MaxTokens: 128})
			note, err := c.Compact(context.Background(), []conversation.Message{{Role: "user", Text: "My name is Alice."}, {Role: "assistant", Text: "Hello Alice.", Interrupted: true}}, "User prefers concise replies.")
			if strings.Contains(output, "User is Alice") {
				if err != nil || !strings.Contains(note, "Alice") {
					t.Fatal(note, err)
				}
			} else if err == nil {
				t.Fatal("invalid summary accepted", note)
			}
		})
	}
}
