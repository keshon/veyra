package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keshon/veyra/internal/config"
	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/httpapi"
	"github.com/keshon/veyra/internal/language"
)

type runtimeFake struct{}

func (runtimeFake) Initialize(context.Context) error { return nil }
func (runtimeFake) Reset(context.Context) error      { return nil }
func (runtimeFake) Capabilities() conversation.Capabilities {
	return conversation.Capabilities{TextInput: true, Languages: []string{"en"}}
}
func (runtimeFake) Respond(_ context.Context, turn conversation.Turn, emit func(conversation.Event) error) error {
	return emit(conversation.Event{Kind: "text", Text: "reply"})
}
func testRuntime(t *testing.T) *runtime {
	t.Helper()
	s, err := conversation.New(runtimeFake{}, language.Preferences{Reply: "en", Fallback: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &runtime{session: s, handler: httpapi.Handler(s, t.TempDir(), 4096), close: func() {}}
}
func requestModels(m *runtimeManager, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, "http://127.0.0.1:8090"+path, strings.NewReader(body))
	m.ServeHTTP(w, r)
	return w
}
func waitSwitch(t *testing.T, m *runtimeManager) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		loading := m.loading
		m.mu.Unlock()
		if !loading {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("switch did not settle")
}
func TestModelSwitchFailureRetryAndAdmission(t *testing.T) {
	old := testRuntime(t)
	ctx := context.Background()
	if err := old.session.SetPromptOverride(ctx, "partner", "Keep it short."); err != nil {
		t.Fatal(err)
	}
	if err := old.session.Respond(ctx, conversation.Input{Text: "hello"}, func(conversation.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	replacement := testRuntime(t)
	closed := false
	fail := true
	block := make(chan struct{})
	old.close = func() { closed = true }
	m := &runtimeManager{ctx: ctx, current: old, catalog: modelCatalog{Switchable: true, Profiles: []modelChoice{{ID: "nano"}}, Models: []modelChoice{{ID: "model.gguf"}}}, check: func(config.Settings) error { return nil }}
	m.start = func(context.Context, config.Settings) (*runtime, error) {
		if !closed {
			t.Error("replacement started before old workers stopped")
		}
		<-block
		if fail {
			return nil, errors.New("out of memory")
		}
		return replacement, nil
	}
	body := `{"profile":"nano","model":"model.gguf"}`
	m.gate.RLock()
	if w := requestModels(m, "POST", "/api/models", body); w.Code != 409 {
		t.Fatal(w.Code)
	}
	m.gate.RUnlock()
	if w := requestModels(m, "POST", "/api/models", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := requestModels(m, "GET", "/api/status", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"loading"`) {
		t.Fatal(w.Body)
	}
	if w := requestModels(m, "POST", "/api/reset", ""); w.Code != 503 {
		t.Fatal(w.Code)
	}
	close(block)
	waitSwitch(t, m)
	if w := requestModels(m, "GET", "/api/status", ""); !strings.Contains(w.Body.String(), "out of memory") {
		t.Fatal(w.Body)
	}
	fail = false
	if w := requestModels(m, "POST", "/api/models", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	waitSwitch(t, m)
	if replacement.session.Status().Preferences.CustomPrompt != "Keep it short." {
		t.Fatal("prompt lost")
	}
	if w := requestModels(m, "GET", "/api/status", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"ready"`) {
		t.Fatal(w.Body)
	}
	// A cross-origin caller cannot restart local processes.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090/api/models", strings.NewReader(body))
	req.Header.Set("Origin", "https://evil.example")
	m.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestInitialLoadingAndRecoveryRemainAccessible(t *testing.T) {
	replacement := testRuntime(t)
	m := &runtimeManager{ctx: context.Background(), loading: true, settings: config.Settings{Reply: "en", Fallback: "en"}, catalog: modelCatalog{Switchable: true, Profiles: []modelChoice{{ID: "nano"}}, Models: []modelChoice{{ID: "model.gguf"}}}, check: func(config.Settings) error { return nil }, start: func(context.Context, config.Settings) (*runtime, error) { return replacement, nil }}
	if w := requestModels(m, "GET", "/api/status", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"loading"`) {
		t.Fatal(w.Code, w.Body)
	}
	if w := requestModels(m, "GET", "/", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "<html") {
		t.Fatal("UI unavailable during initial load", w.Code)
	}
	m.loading = false
	m.failure = "Startup failed"
	if w := requestModels(m, "GET", "/api/status", ""); !strings.Contains(w.Body.String(), "Startup failed") {
		t.Fatal(w.Body)
	}
	if w := requestModels(m, "POST", "/api/models", `{"profile":"nano","model":"model.gguf"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	waitSwitch(t, m)
	if w := requestModels(m, "GET", "/api/status", ""); !strings.Contains(w.Body.String(), `"state":"ready"`) {
		t.Fatal(w.Body)
	}
}
