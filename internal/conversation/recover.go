package conversation

import (
	"context"
	"fmt"
)

// Recover is optional. Stateful native runtimes must not inherit a no-op reset.
type recoverer interface{ Recover(context.Context) error }

// Recover resumes a composed conversation without throwing away prior context.
// Generated text is marked interrupted; it is not claimed to be heard speech.
func (s *Session) Recover(ctx context.Context) error {
	provider, ok := s.backend.(recoverer)
	if !ok {
		return fmt.Errorf("this backend cannot recover from interruption; restart the application")
	}
	s.mu.Lock()
	if s.failure != nil {
		err := s.failure
		s.mu.Unlock()
		return err
	}
	if s.busy {
		s.mu.Unlock()
		return ErrBusy
	}
	s.busy = true
	s.state = "loading"
	s.detail = "recovering interrupted conversation"
	s.mu.Unlock()
	err := provider.Recover(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = false
	if s.failure != nil {
		return s.failure
	}
	if err != nil {
		s.state = "error"
		s.detail = err.Error()
		return err
	}
	if n := len(s.history); n > 0 && s.history[n-1].Role == "assistant" {
		s.history[n-1].Interrupted = true
	}
	s.state = "ready"
	s.detail = "listening after interruption"
	if err := s.saveLocked(); err != nil {
		s.detail = "conversation save failed: " + err.Error()
		return fmt.Errorf("%s", s.detail)
	}
	return nil
}
