package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/language"
)

type backend struct{}

func (backend) Initialize(context.Context) error { return nil }
func (backend) Reset(context.Context) error      { return nil }
func (backend) Capabilities() conversation.Capabilities {
	return conversation.Capabilities{Languages: []string{"en", "ru"}, AutomaticLanguage: true, TextInput: true}
}
func (backend) Respond(c context.Context, t conversation.Turn, e func(conversation.Event) error) error {
	for _, ev := range []conversation.Event{{Kind: "text", Text: "hello"}, {Kind: "audio", Audio: []byte("audio")}, {Kind: "done"}} {
		if err := e(ev); err != nil {
			return err
		}
	}
	return nil
}
func TestAPIStreamsUniqueAudioAndPreservesPreferences(t *testing.T) {
	s, _ := conversation.New(backend{}, language.Preferences{Reply: "auto", Fallback: "en"})
	s.Initialize(context.Background())
	server := httptest.NewServer(Handler(s, t.TempDir(), 2048))
	defer server.Close()
	post := func(path, body string) string {
		t.Helper()
		r, err := http.Post(server.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		if r.StatusCode != 200 {
			t.Fatal(r.StatusCode, string(b))
		}
		return string(b)
	}
	post("/api/preferences", `{"reply":"ru","fallback":"en"}`)
	first := post("/api/text", `{"text":"hello"}`)
	post("/api/reset", "")
	second := post("/api/text", `{"text":"hello"}`)
	getAudio := func(body string) string {
		for _, line := range strings.Split(body, "\n") {
			var v map[string]any
			if json.Unmarshal([]byte(line), &v) == nil && v["audio"] != nil {
				return v["audio"].(string)
			}
		}
		t.Fatal("missing audio", body)
		return ""
	}
	a, b := getAudio(first), getAudio(second)
	if a == b {
		t.Fatal("reset reused audio URL")
	}
	r, err := http.Get(server.URL + a)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	data, _ := io.ReadAll(r.Body)
	if string(data) != "audio" {
		t.Fatal(string(data))
	}
	if s.Status().Preferences.Reply != "ru" {
		t.Fatal(s.Status())
	}
	if !strings.Contains(second, `"done":true`) {
		t.Fatal(second)
	}
}
func TestRejectCrossOriginAndBadInput(t *testing.T) {
	s, _ := conversation.New(backend{}, language.Preferences{Reply: "auto", Fallback: "en"})
	s.Initialize(context.Background())
	h := Handler(s, t.TempDir(), 2048)
	for _, tc := range []struct {
		host, origin, body string
		code               int
	}{{"127.0.0.1:8090", "https://hostile.example", `{"text":"hi"}`, 403}, {"hostile.example:8090", "", `{"text":"hi"}`, 403}, {"127.0.0.1:8090", "", `{"text":"hi","extra":1}`, 400}, {"127.0.0.1:8090", "", `{"text":"hi"} {}`, 400}, {"127.0.0.1:8090", "", `{"text":""}`, 400}} {
		r := httptest.NewRequest("POST", "http://"+tc.host+"/api/text", strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatal(tc, w.Code)
		}
	}
}
