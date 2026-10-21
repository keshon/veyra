package llamacpp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keshon/veyra/internal/speech"
)

func TestStructuredSpeechStreamsSegmentsWithSchema(t *testing.T) {
	first := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		if !strings.Contains(string(payload["response_format"]), "spoken_reply") || !strings.Contains(string(payload["response_format"]), "whispering") {
			t.Error("missing constrained schema")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(s string) {
			b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": s}}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
			w.(http.Flusher).Flush()
		}
		for _, c := range `[{"cues":["whispering"],"text":"A secret."}` {
			send(string(c))
		}
		select {
		case <-first:
		case <-r.Context().Done():
			return
		}
		send(`,{"cues":[],"text":"Normal again."}]`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	var got []speech.Utterance
	err := New(server.URL).GenerateSpeech(context.Background(), nil, []string{"whispering"}, func(u speech.Utterance) error {
		got = append(got, u)
		if len(got) == 1 {
			close(first)
		}
		return nil
	})
	if err != nil || len(got) != 2 || got[0].Text != "A secret." || got[0].Cues[0].Name != "whispering" || len(got[1].Cues) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestStructuredSpeechRejectsUnsafeTextAndInvalidCues(t *testing.T) {
	for _, raw := range []string{`[{"cues":[],"text":"[clears throat softly]Hello"}]`, `[{"cues":["leans in"],"text":"Hello"}]`} {
		emitted := false
		parser := speechJSON{allowed: []string{"whispering"}, emit: func(speech.Utterance) error { emitted = true; return nil }}
		if err := parser.push(raw); err == nil || emitted {
			t.Fatalf("unsafe output accepted: %s", raw)
		}
	}
}

func TestStructuredSpeechEscapedStrings(t *testing.T) {
	var got speech.Utterance
	parser := speechJSON{emit: func(u speech.Utterance) error { got = u; return nil }}
	raw := `[{"cues":[],"text":"She said \"hello\". A brace } is text."}]`
	for _, b := range []byte(raw) {
		if err := parser.push(string(b)); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(got.Text, "A brace } is text.") {
		t.Fatal(got)
	}
}
