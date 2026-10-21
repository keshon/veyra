package conversation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/keshon/veyra/internal/language"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SavedConversation struct {
	PostHistory string               `json:"post_history,omitempty"`
	Summary     string               `json:"summary,omitempty"`
	Instruction string               `json:"instruction"`
	Version     int                  `json:"version"`
	ID          string               `json:"id"`
	Title       string               `json:"title"`
	Updated     time.Time            `json:"updated"`
	Profile     string               `json:"profile"`
	Preferences language.Preferences `json:"preferences"`
	Messages    []Message            `json:"messages,omitempty"`
	Language    string               `json:"language,omitempty"`
}

// EnableArchive enables automatic snapshots for text-capable profiles.
// Native audio sessions cannot reconstruct their model state from text history.
func (s *Session) EnableArchive(directory string) error {
	if !s.Capabilities().TextInput {
		return nil
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return ErrBusy
	}
	s.archiveDir = directory
	return nil
}
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
func readSaved(directory, id string) (SavedConversation, error) {
	var saved SavedConversation
	if !validID(id) {
		return saved, fmt.Errorf("invalid conversation ID")
	}
	f, err := os.Open(filepath.Join(directory, id+".json"))
	if err != nil {
		return saved, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 4*1024*1024))
	if err = decoder.Decode(&saved); err != nil {
		return saved, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return saved, fmt.Errorf("saved conversation must contain one JSON object")
	}
	if saved.Version != 1 || saved.ID != id || len(saved.Messages)%2 != 0 {
		return saved, fmt.Errorf("invalid saved conversation")
	}
	size := 0
	for i, m := range saved.Messages {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if m.Role != role {
			return saved, fmt.Errorf("invalid saved exchange")
		}
		size += len(m.Text)
	}
	if size > 256*1024 || len(saved.Messages) > 256 {
		return saved, fmt.Errorf("saved context exceeds storage limit")
	}
	if len(saved.Summary) > 4096 {
		return saved, fmt.Errorf("saved summary exceeds limit")
	}
	return saved, nil
}
func (s *Session) Saved() ([]SavedConversation, error) {
	s.mu.Lock()
	directory := s.archiveDir
	s.mu.Unlock()
	if directory == "" {
		return nil, fmt.Errorf("saved conversations are unavailable for this profile")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	items := make([]SavedConversation, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		saved, err := readSaved(directory, strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		saved.Messages = nil
		items = append(items, saved)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Updated.After(items[j].Updated) })
	return items, nil
}

// saveLocked atomically replaces retained context. WAVs and runtime state are
// not stored. This is a resumable context snapshot, not a full transcript archive.
func (s *Session) saveLocked() error {
	if s.archiveDir == "" || len(s.history) == 0 {
		return nil
	}
	if s.savedID == "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return err
		}
		s.savedID = hex.EncodeToString(id[:])
		title := []rune(s.history[0].Text)
		if len(title) > 80 {
			title = title[:80]
		}
		s.savedTitle = string(title)
	}
	saved := SavedConversation{Version: 1, ID: s.savedID, Title: s.savedTitle, Updated: time.Now().UTC(), Profile: s.backend.Capabilities().Profile, Preferences: s.preferences.Clone(), Messages: s.history, Language: s.previousLanguage, Instruction: s.preferences.Instruction, PostHistory: s.preferences.PostHistory, Summary: s.summary}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.archiveDir, ".save-*.tmp")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(temp, filepath.Join(s.archiveDir, s.savedID+".json"))
}
func (s *Session) Resume(ctx context.Context, id string) (SavedConversation, error) {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return SavedConversation{}, ErrBusy
	}
	if s.failure != nil {
		err := s.failure
		s.mu.Unlock()
		return SavedConversation{}, err
	}
	if s.archiveDir == "" {
		s.mu.Unlock()
		return SavedConversation{}, fmt.Errorf("saved conversations are unavailable for this profile")
	}
	saved, err := readSaved(s.archiveDir, id)
	if err == nil && (saved.Profile != s.backend.Capabilities().Profile || saved.Instruction != s.preferences.Instruction || saved.PostHistory != s.preferences.PostHistory) {
		err = fmt.Errorf("launch the saved profile and character/system prompt before resuming")
	}
	if err == nil {
		err = validatePreferences(saved.Preferences, s.backend.Capabilities())
	}
	if err != nil {
		s.mu.Unlock()
		return SavedConversation{}, err
	}
	s.busy = true
	s.state, s.detail = "loading", "resuming saved conversation"
	s.mu.Unlock()
	err = s.backend.Reset(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = false
	if s.failure != nil {
		return SavedConversation{}, s.failure
	}
	if err != nil {
		s.state = "error"
		s.detail = err.Error()
		return SavedConversation{}, err
	}
	s.history = append([]Message(nil), saved.Messages...)
	s.summary = saved.Summary
	s.preferences = saved.Preferences.Clone()
	s.preferences.Instruction = saved.Instruction
	s.preferences.PostHistory = saved.PostHistory
	s.previousLanguage = saved.Language
	s.savedID, s.savedTitle = saved.ID, saved.Title
	s.contextUsage = ContextUsage{}
	s.state, s.detail = "ready", "resumed conversation"
	return saved, nil
}
