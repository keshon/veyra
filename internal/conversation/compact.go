package conversation

import (
	"context"
	"fmt"
)

type historyCompactor interface {
	CompactHistory(context.Context, Turn) (string, error)
}

// Compact manually summarizes older exchanges while retaining the latest four.
// On failure the existing history and summary remain available unchanged.
func (s *Session) Compact(ctx context.Context) (SavedConversation, error) {
	worker, ok := s.backend.(historyCompactor)
	if !ok {
		return SavedConversation{}, fmt.Errorf("this profile does not support conversation compaction")
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return SavedConversation{}, ErrBusy
	}
	if s.state != "ready" {
		s.mu.Unlock()
		return SavedConversation{}, ErrNotReady
	}
	cut := len(s.history) - 8
	if cut <= 0 {
		s.mu.Unlock()
		return SavedConversation{}, fmt.Errorf("nothing to compact; the latest four exchanges are always retained")
	}
	turn := Turn{Summary: s.summary, History: append([]Message(nil), s.history[:cut]...), Preferences: s.preferences.Clone()}
	compactCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.cancel = cancel
	s.busy = true
	s.state, s.detail = "loading", "compacting conversation"
	s.mu.Unlock()
	note, err := worker.CompactHistory(compactCtx, turn)
	if err == nil {
		err = compactCtx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = false
	s.cancel = nil
	if s.failure != nil {
		return SavedConversation{}, s.failure
	}
	s.state, s.detail = "ready", "conversation ready"
	if err != nil {
		return SavedConversation{}, err
	}
	if note == "" || len(note) > 4096 {
		return SavedConversation{}, fmt.Errorf("invalid compaction summary")
	}
	s.summary = note
	s.history = append([]Message(nil), s.history[cut:]...)
	s.contextUsage = ContextUsage{}
	if err := s.saveLocked(); err != nil {
		return SavedConversation{}, fmt.Errorf("compacted, but conversation save failed: %w", err)
	}
	return SavedConversation{ID: s.savedID, Title: s.savedTitle, Summary: s.summary, Messages: append([]Message(nil), s.history...), Preferences: s.preferences.Clone()}, nil
}
