package httpapi

import (
	"context"
	"encoding/json"
	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/language"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIListsAndResumesSavedContext(t *testing.T) {
	s, err := conversation.New(backend{}, language.Preferences{Reply: "en", Fallback: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.EnableArchive(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err = s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = s.Respond(context.Background(), conversation.Input{Text: "Remember Alice"}, func(conversation.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(Handler(s, t.TempDir(), 4096))
	defer server.Close()
	response, err := http.Get(server.URL + "/api/conversations")
	if err != nil {
		t.Fatal(err)
	}
	var items []conversation.SavedConversation
	err = json.NewDecoder(response.Body).Decode(&items)
	response.Body.Close()
	if err != nil || len(items) != 1 || len(items[0].Messages) != 0 {
		t.Fatal(items, err)
	}
	if err = s.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err = http.Post(server.URL+"/api/conversations/resume", "application/json", strings.NewReader(`{"id":"`+items[0].ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var saved conversation.SavedConversation
	if err = json.NewDecoder(response.Body).Decode(&saved); err != nil || response.StatusCode != 200 || len(saved.Messages) != 2 || saved.Messages[0].Text != "Remember Alice" {
		t.Fatal(response.StatusCode, saved, err)
	}
}
