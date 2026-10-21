package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/language"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type duplexFake struct{ received chan conversation.DuplexInput }

func (*duplexFake) Initialize(context.Context) error { return nil }
func (*duplexFake) Reset(context.Context) error      { return nil }
func (*duplexFake) Respond(context.Context, conversation.Turn, func(conversation.Event) error) error {
	return nil
}
func (*duplexFake) Capabilities() conversation.Capabilities {
	return conversation.Capabilities{FullDuplex: true, Languages: []string{"en"}}
}
func (f *duplexFake) Duplex(ctx context.Context, in <-chan conversation.DuplexInput, emit func(conversation.Event) error) error {
	if err := emit(conversation.Event{Kind: "phase", Phase: "listening"}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case input := <-in:
			f.received <- input
		}
	}
}
func TestDuplexLeaseSequenceAndAdmission(t *testing.T) {
	f := &duplexFake{received: make(chan conversation.DuplexInput, 8)}
	session, err := conversation.New(f, language.Preferences{Reply: "en", Fallback: "en"})
	if err != nil {
		t.Fatal(err)
	}
	session.Initialize(context.Background())
	server := httptest.NewServer(Handler(session, t.TempDir(), 4096))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/duplex", nil)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	if !scanner.Scan() {
		t.Fatal("no ready event")
	}
	var event struct{ Token string }
	json.Unmarshal(scanner.Bytes(), &event)
	if event.Token == "" {
		t.Fatal("missing lease")
	}
	wav := make([]byte, 32044)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], 32036)
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 16000)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], 32000)
	post := func(path, token, seq string, body []byte) int {
		t.Helper()
		r, _ := http.NewRequestWithContext(ctx, "POST", server.URL+path, bytes.NewReader(body))
		r.Header.Set("X-Duplex-Token", token)
		r.Header.Set("X-Sequence", seq)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("/api/duplex/chunk", "wrong", "0", wav); code != 409 {
		t.Fatal(code)
	}
	if code := post("/api/duplex/chunk", event.Token, "1", wav); code != 409 {
		t.Fatal(code)
	}
	if code := post("/api/duplex/chunk", event.Token, "0", wav); code != 200 {
		t.Fatal(code)
	}
	select {
	case <-f.received:
	case <-ctx.Done():
		t.Fatal("audio not delivered")
	}
	if code := post("/api/duplex/chunk", event.Token, "0", wav); code != 409 {
		t.Fatal("duplicate accepted", code)
	}
	if code := post("/api/prompt", "", "", []byte(`{"id":"partner"}`)); code != 409 {
		t.Fatal("prompt changed while duplex owned KV", code)
	}
	if code := post("/api/duplex/stop", event.Token, "", nil); code != 200 {
		t.Fatal(code)
	}
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), `"done":true`) {
			break
		}
	}
	if session.Status().State != "ready" {
		t.Fatal(session.Status())
	}
}
