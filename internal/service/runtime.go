package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/keshon/veyra/internal/config"
	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/web"
)

type runtime struct {
	apply   func(config.Settings)
	session *conversation.Session
	handler http.Handler
	close   func()
}

func loadRuntimeProgress(ctx context.Context, settings config.Settings, report func(string, string)) (*runtime, error) {
	ctx, cancel := context.WithCancel(ctx)
	ready := make(chan *runtime)
	done := make(chan error, 1)
	go func() {
		done <- serveRuntime(ctx, settings, report, func(r *runtime) {
			select {
			case ready <- r:
			case <-ctx.Done():
			}
		})
	}()
	select {
	case r := <-ready:
		var once sync.Once
		r.close = func() { once.Do(func() { cancel(); <-done }) }
		if ctx.Err() != nil {
			r.close()
			return nil, ctx.Err()
		}
		return r, nil
	case err := <-done:
		cancel()
		return nil, err
	case <-ctx.Done():
		cancel()
		<-done
		return nil, ctx.Err()
	}
}

type runtimeManager struct {
	ctx                         context.Context
	gate                        sync.RWMutex // Includes complete streamed turns, not just admission.
	mu                          sync.Mutex
	current                     *runtime
	settings                    config.Settings
	loading                     bool
	failure                     string
	catalog                     modelCatalog
	start                       func(context.Context, config.Settings) (*runtime, error)
	check                       func(config.Settings) error
	stage, logDir, errorDetails string
	started, stageStarted       time.Time
	loadCancel                  context.CancelFunc
}

func Run(ctx context.Context, settings config.Settings) error {
	listener, err := net.Listen("tcp", settings.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	m := &runtimeManager{ctx: ctx, settings: settings, catalog: modelCatalogFor(settings), check: checkModelSetup, loading: true, started: time.Now(), stageStarted: time.Now(), stage: "Checking installation"}
	m.start = func(ctx context.Context, s config.Settings) (*runtime, error) {
		return loadRuntimeProgress(ctx, s, m.progress)
	}
	loadCtx, loadCancel := context.WithCancel(ctx)
	m.loadCancel = loadCancel
	m.gate.Lock()
	go func() {
		defer m.gate.Unlock()
		current, err := m.start(loadCtx, settings)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.loading = false
		if err == nil && loadCtx.Err() != nil {
			err = loadCtx.Err()
			current.close()
		}
		if err != nil {
			loadCancel()
			m.failLocked(err)
			return
		}
		closeRuntime := current.close
		current.close = func() { loadCancel(); closeRuntime() }
		m.current = current
		_, port, _ := net.SplitHostPort(listener.Addr().String())
		log.Printf("Open Veyra: http://localhost:%s/", port)
	}()
	defer func() {
		m.gate.Lock()
		defer m.gate.Unlock()
		if m.current != nil {
			m.current.close()
		}
	}()
	server := &http.Server{Handler: m, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(stop)
	}()
	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (m *runtimeManager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Apply the same loopback and origin policy to model management as session APIs.
	host, _, err := net.SplitHostPort(r.Host)
	ip := net.ParseIP(host)
	if err != nil || !(host == "localhost" || ip != nil && ip.IsLoopback()) {
		http.Error(w, "loopback host required", 403)
		return
	}
	origin := r.Header.Get("Origin")
	if r.Method == "POST" && (origin != "" && origin != "http://"+r.Host || r.Header.Get("Sec-Fetch-Site") == "cross-site") {
		http.Error(w, "cross-origin request", 403)
		return
	}
	if r.URL.Path == "/api/settings" || r.URL.Path == "/api/characters" || r.URL.Path == "/api/characters/import" {
		m.preferencesHTTP(w, r)
		return
	}
	if r.URL.Path == "/api/models/cancel" && r.Method == "POST" {
		m.mu.Lock()
		if m.loading && m.loadCancel != nil {
			m.loadCancel()
		}
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
		return
	}
	if r.URL.Path == "/api/models" {
		if r.Method == "GET" {
			m.mu.Lock()
			c := m.catalog
			c.CanRestore = m.current != nil && (m.failure != "" || m.current.session.Status().State == "error")
			m.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(c)
			return
		}
		if r.Method == "POST" {
			m.change(w, r)
			return
		}
		http.Error(w, "method not allowed", 405)
		return
	}
	m.mu.Lock()
	current, loading, failure, settings := m.current, m.loading, m.failure, m.settings
	stage := m.stage
	if stage == "" {
		stage = "Loading models"
	}
	startup := map[string]any{"stage": m.stage, "started_at": m.started, "stage_started_at": m.stageStarted, "log_dir": m.logDir, "can_cancel": loading && m.loadCancel != nil}
	errorDetails := m.errorDetails
	m.mu.Unlock()
	if current != nil && failure == "" && current.session.Status().State == "error" {
		failure = "The voice session stopped. Reload this setup or choose another backend."
		errorDetails = current.session.Status().Detail
	}
	if current == nil {
		if r.Method == "GET" && r.URL.Path == "/api/status" {
			state, detail := "loading", stage
			if !loading {
				state, detail = "error", failure
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"state": state, "detail": detail, "startup": startup, "error_details": errorDetails, "preferences": map[string]string{"reply": settings.Reply, "fallback": settings.Fallback}, "capabilities": conversation.Capabilities{Languages: []string{}, Voices: []conversation.Voice{}}, "prompts": []conversation.Prompt{}})
		} else if r.Method == "GET" && !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/audio/") {
			web.Handler().ServeHTTP(w, r)
		} else {
			http.Error(w, "models are unavailable; choose a setup and retry", 503)
		}
		return
	}
	if r.URL.Path == "/api/status" && r.Method == "GET" && (loading || failure != "") {
		status := current.session.Status()
		state, detail := "loading", stage
		if !loading {
			state, detail = "error", failure
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"state": state, "detail": detail, "startup": startup, "error_details": errorDetails, "preferences": status.Preferences, "capabilities": current.session.Capabilities(), "prompts": current.session.Prompts(), "has_history": status.HasHistory})
		return
	}
	if r.Method == "GET" {
		current.handler.ServeHTTP(w, r)
		return
	}
	if !m.gate.TryRLock() {
		http.Error(w, "models are loading", 503)
		return
	}
	defer m.gate.RUnlock()
	m.mu.Lock()
	current, failure = m.current, m.failure
	m.mu.Unlock()
	if failure != "" && r.Method == "POST" {
		http.Error(w, failure, 503)
		return
	}
	current.handler.ServeHTTP(w, r)
}

func (m *runtimeManager) change(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Profile         string              `json:"profile"`
		Model           string              `json:"model"`
		NewConversation bool                `json:"new_conversation"`
		Preferences     *config.Preferences `json:"preferences,omitempty"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	d.DisallowUnknownFields()
	if err := d.Decode(&input); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if d.Decode(new(any)) != io.EOF {
		http.Error(w, "expected one JSON object", 400)
		return
	}
	if !m.gate.TryLock() {
		http.Error(w, "finish the current operation before changing models", 409)
		return
	}
	m.mu.Lock()
	c := m.catalog
	next := m.settings
	old := m.current
	failed := m.failure != ""
	m.mu.Unlock()
	validProfile, validModel := false, false
	selected := modelChoice{}
	for _, p := range c.Profiles {
		if p.ID == input.Profile && p.Unavailable == "" {
			validProfile = true
			selected = p
		}
	}
	for _, p := range c.Models {
		if p.ID == input.Model {
			validModel = true
		}
	}
	if selected.Mode == "minicpm-native" {
		validModel = true
	}
	sameSetup := input.Profile == c.Profile && input.Model == c.Model
	if input.Preferences != nil && sameSetup {
		if profile, err := next.LoadProfile(); err == nil {
			validProfile, validModel = true, true
			selected.Mode = profile.Mode
			selected.RequiresNew = profile.Mode == "minicpm-native"
		}
	}
	if (!c.Switchable && !(input.Preferences != nil && sameSetup)) || !validProfile || !validModel {
		m.gate.Unlock()
		http.Error(w, "Choose an installed backend and a compatible text model.", 400)
		return
	}
	preferencesChanged := false
	if input.Preferences != nil {
		p := input.Preferences
		if err := p.Generation.Validate(); err != nil {
			m.gate.Unlock()
			http.Error(w, err.Error(), 400)
			return
		}
		if strings.TrimSpace(p.User) == "" || len(p.User) > 100 {
			m.gate.Unlock()
			http.Error(w, "user name must contain 1–100 bytes", 400)
			return
		}
		preferencesChanged = next.Preferences() != *p
		characterChanged := next.Character != p.Character || next.User != p.User
		if characterChanged && !input.NewConversation {
			m.gate.Unlock()
			http.Error(w, "Changing character or user requires a new conversation.", 409)
			return
		}
		selected.RequiresNew = selected.RequiresNew || characterChanged
		if p.Character != "" && p.Character != next.Character {
			base := filepath.Join(next.DataDir, "characters")
			rel, err := filepath.Rel(base, p.Character)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
				m.gate.Unlock()
				http.Error(w, "Import the character card first.", 400)
				return
			}
		}
		next.SetGeneration(p.Generation)
		next.Character = p.Character
		next.User = p.User
		if _, err := Metadata(next); err != nil {
			m.gate.Unlock()
			http.Error(w, err.Error(), 400)
			return
		}
	}
	if old != nil && old.session.Status().State == "ready" && !failed && !preferencesChanged && !selected.RequiresNew && input.Profile == c.Profile && input.Model == c.Model {
		m.gate.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
		return
	}
	if selected.RequiresNew && !input.NewConversation {
		m.gate.Unlock()
		http.Error(w, "Switching native voice backends requires an explicit new conversation.", 409)
		return
	}
	if input.Preferences == nil || !sameSetup {
		next.Profile, next.ProfileFile, next.ThinkingModel, next.Voice = input.Profile, "", input.Model, ""
	}
	if selected.Mode == "minicpm-native" {
		next.ThinkingModel = ""
	}

	if p, e := next.LoadProfile(); e == nil && (p.Speaking.Provider == "chatterbox" || p.Speaking.Provider == "kokoro") {
		next.Reply, next.Fallback = "en", "en"
	}
	if err := m.check(next); err != nil {
		m.gate.Unlock()
		http.Error(w, err.Error(), 400)
		return
	}
	if input.Preferences != nil && old != nil && old.apply != nil && old.session.Status().State == "ready" && next.Context == m.settings.Context && !selected.RequiresNew && input.Profile == c.Profile && input.Model == c.Model && !failed {
		if err := next.SavePreferences(); err != nil {
			m.gate.Unlock()
			http.Error(w, err.Error(), 500)
			return
		}
		old.apply(next)
		m.mu.Lock()
		m.settings = next
		m.mu.Unlock()
		m.gate.Unlock()
		w.Write([]byte("{}"))
		return
	}
	m.mu.Lock()
	m.loading = true
	m.failure, m.errorDetails = "", ""
	m.stage = "Checking installation"
	m.started = time.Now()
	m.stageStarted = m.started
	loadCtx, loadCancel := context.WithCancel(m.ctx)
	m.loadCancel = loadCancel
	m.mu.Unlock()
	go func() {
		defer m.gate.Unlock()
		if old != nil {
			old.close()
		} // Release GPU memory and ports before starting replacements.
		replacement, err := m.start(loadCtx, next)
		if err != nil {
			loadCancel()
		} else {
			closeRuntime := replacement.close
			replacement.close = func() { loadCancel(); closeRuntime() }
		}
		if err == nil && old != nil && !selected.RequiresNew {
			err = old.session.CopyContextTo(replacement.session)
		}
		if err != nil && replacement != nil {
			replacement.close()
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.loading = false
		if err == nil && loadCtx.Err() != nil {
			err = loadCtx.Err()
			replacement.close()
		}
		if err != nil {
			m.failLocked(err)
			return
		}
		if input.Preferences != nil {
			if err := next.SavePreferences(); err != nil {
				m.errorDetails = "Settings applied but could not be saved: " + err.Error()
				log.Print(m.errorDetails)
			}
		}
		m.current, m.settings = replacement, next
		m.catalog.Profile, m.catalog.Model = input.Profile, input.Model
		if p, e := next.LoadProfile(); e == nil {
			m.catalog.Model = p.Thinking.Model
		}
		for i := range m.catalog.Profiles {
			m.catalog.Profiles[i].RequiresNew = selected.Mode == "minicpm-native" || m.catalog.Profiles[i].Mode == "minicpm-native"
		}
	}()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("{}"))
}

func checkModelSetup(s config.Settings) error {
	p, err := s.LoadProfile()
	if err != nil {
		return err
	}
	if p.Mode == "composed" {
		if err = config.CheckChatModel(p.Thinking.Model); err != nil {
			return fmt.Errorf("Choose a text chat model: %w", err)
		}
	}
	return Check(s)
}
func (m *runtimeManager) progress(stage, dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stage, m.logDir = stage, dir
	m.stageStarted = time.Now()
}
func (m *runtimeManager) failLocked(err error) {
	defer func() { log.Printf("Startup failed during %s: %v", m.stage, err) }()
	m.errorDetails = err.Error()
	m.failure = "The model runtime could not start. Choose another setup or retry."
	if strings.Contains(strings.ToLower(err.Error()), "out of memory") {
		m.failure = "Not enough memory to load this setup. Choose a smaller model or another backend."
	}
	if strings.Contains(err.Error(), "deadline exceeded") {
		m.failure = "Model loading took too long. Try another backend or a smaller model."
	}
	if err == context.Canceled || strings.Contains(err.Error(), "context canceled") {
		m.failure = "Model loading canceled. Choose a setup to continue."
	}
}
