package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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
	session *conversation.Session
	handler http.Handler
	close   func()
}

func loadRuntime(ctx context.Context, settings config.Settings) (*runtime, error) {
	ctx, cancel := context.WithCancel(ctx)
	ready := make(chan *runtime)
	done := make(chan error, 1)
	go func() {
		done <- serveRuntime(ctx, settings, func(r *runtime) {
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

type modelChoice struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Unavailable string `json:"unavailable,omitempty"`
}
type modelCatalog struct {
	Profile    string        `json:"profile"`
	Model      string        `json:"model"`
	Profiles   []modelChoice `json:"profiles"`
	Models     []modelChoice `json:"models"`
	Switchable bool          `json:"switchable"`
}

// modelCatalogFor lists installed manifests and GGUF files, including nested
// model directories. Paths come from the installation or current explicit model.
func modelCatalogFor(s config.Settings) modelCatalog {
	c := modelCatalog{Profile: s.Profile, Model: s.ThinkingModel, Profiles: []modelChoice{}, Models: []modelChoice{}}
	current, err := s.LoadProfile()
	if err != nil {
		return c
	}
	c.Switchable = current.Mode == "composed"
	c.Model = current.Thinking.Model
	entries, _ := filepath.Glob(filepath.Join(s.Root, "profiles", "*.json"))
	if s.ProfileFile == "" {
		entries = append([]string{filepath.Join(s.Root, "profiles", s.Profile+".json")}, entries...)
	}
	seenProfiles := map[string]bool{}
	for _, path := range entries {
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		candidate := s
		candidate.Profile, candidate.ProfileFile = id, ""
		p, err := candidate.LoadProfile()
		if err != nil || p.Mode != "composed" {
			continue
		}
		key := fmt.Sprintf("%s|%s|%s|%s|%s|%s", p.Listening.Model, p.Speaking.Provider, p.Speaking.Model, p.Speaking.Variant, p.Speaking.Voices, p.Speaking.Binary)
		if seenProfiles[key] {
			continue
		}
		seenProfiles[key] = true
		item := modelChoice{ID: id, Name: strings.Split(p.Speaking.Name, " · ")[0]}
		if err := Check(candidate); err != nil {
			item.Unavailable = err.Error()
		}
		c.Profiles = append(c.Profiles, item)
	}
	seen := map[string]bool{}
	add := func(path string) {
		path = filepath.Clean(path)
		if !seen[path] {
			seen[path] = true
			c.Models = append(c.Models, modelChoice{ID: path, Name: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))})
		}
	}
	add(c.Model)
	roots := []string{filepath.Join(s.Root, "assets", "models"), filepath.Dir(c.Model)}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".gguf") {
				add(path)
			}
			return nil
		})
	}
	return c
}

type runtimeManager struct {
	ctx      context.Context
	gate     sync.RWMutex // Includes complete streamed turns, not just admission.
	mu       sync.Mutex
	current  *runtime
	settings config.Settings
	loading  bool
	failure  string
	catalog  modelCatalog
	start    func(context.Context, config.Settings) (*runtime, error)
	check    func(config.Settings) error
}

func Run(ctx context.Context, settings config.Settings) error {
	listener, err := net.Listen("tcp", settings.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	m := &runtimeManager{ctx: ctx, settings: settings, catalog: modelCatalogFor(settings), start: loadRuntime, check: Check, loading: true}
	m.gate.Lock()
	go func() {
		defer m.gate.Unlock()
		current, err := m.start(ctx, settings)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.loading = false
		if err != nil {
			m.failure = fmt.Sprintf("Could not load models: %v. Choose a setup and retry.", err)
			return
		}
		m.current = current
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
	if r.URL.Path == "/api/models" {
		if r.Method == "GET" {
			m.mu.Lock()
			c := m.catalog
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
	m.mu.Unlock()
	if current == nil {
		if r.Method == "GET" && r.URL.Path == "/api/status" {
			state, detail := "loading", "Loading models"
			if !loading {
				state, detail = "error", failure
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"state": state, "detail": detail, "preferences": map[string]string{"reply": settings.Reply, "fallback": settings.Fallback}, "capabilities": conversation.Capabilities{Languages: []string{}, Voices: []conversation.Voice{}}, "prompts": []conversation.Prompt{}})
		} else if r.Method == "GET" && !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/audio/") {
			web.Handler().ServeHTTP(w, r)
		} else {
			http.Error(w, "models are unavailable; choose a setup and retry", 503)
		}
		return
	}
	if r.URL.Path == "/api/status" && r.Method == "GET" && (loading || failure != "") {
		status := current.session.Status()
		state, detail := "loading", "Loading models; conversation kept"
		if !loading {
			state, detail = "error", failure
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"state": state, "detail": detail, "preferences": status.Preferences, "capabilities": current.session.Capabilities(), "prompts": current.session.Prompts()})
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
		Profile string `json:"profile"`
		Model   string `json:"model"`
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
	m.mu.Unlock()
	validProfile, validModel := false, false
	for _, p := range c.Profiles {
		if p.ID == input.Profile && p.Unavailable == "" {
			validProfile = true
		}
	}
	for _, p := range c.Models {
		if p.ID == input.Model {
			validModel = true
		}
	}
	if !c.Switchable || !validProfile || !validModel {
		m.gate.Unlock()
		http.Error(w, "choose an installed composed profile and model", 400)
		return
	}
	next.Profile, next.ProfileFile, next.ThinkingModel, next.Voice = input.Profile, "", input.Model, ""
	if p, e := next.LoadProfile(); e == nil && (p.Speaking.Provider == "chatterbox" || p.Speaking.Provider == "kokoro") {
		next.Reply, next.Fallback = "en", "en"
	}
	if err := m.check(next); err != nil {
		m.gate.Unlock()
		http.Error(w, err.Error(), 400)
		return
	}
	m.mu.Lock()
	m.loading = true
	m.failure = ""
	m.mu.Unlock()
	go func() {
		defer m.gate.Unlock()
		if old != nil {
			old.close()
		} // Release GPU memory and ports before starting replacements.
		replacement, err := m.start(m.ctx, next)
		if err == nil && old != nil {
			err = old.session.CopyContextTo(replacement.session)
		}
		if err != nil && replacement != nil {
			replacement.close()
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.loading = false
		if err != nil {
			m.failure = fmt.Sprintf("Could not load models: %v. Choose a setup and retry; conversation kept.", err)
			return
		}
		m.current, m.settings = replacement, next
		m.catalog.Profile, m.catalog.Model = input.Profile, input.Model
	}()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("{}"))
}
