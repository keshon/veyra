package conversation

import (
	"context"
	"fmt"
	"sync"
)

type DuplexInput struct {
	Audio     []byte
	Interrupt bool
}
type duplexBackend interface {
	Duplex(context.Context, <-chan DuplexInput, func(Event) error) error
}

// Duplex owns the provider for the entire live connection, rather than admitting
// overlapping ordinary turns against a single native KV cache.
func (s *Session) Duplex(ctx context.Context, input <-chan DuplexInput, emit func(Event) error) error {
	provider, ok := s.backend.(duplexBackend)
	if !ok || !s.Capabilities().FullDuplex {
		return fmt.Errorf("native continuous streaming is experimental to use continuous conversation")
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return ErrBusy
	}
	if s.state != "ready" {
		s.mu.Unlock()
		return ErrNotReady
	}
	s.nextID++
	id := s.nextID
	s.activeID = id
	s.busy = true
	s.state = "busy"
	s.detail = "full-duplex conversation"
	liveCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()
	defer cancel()
	var delivery sync.Mutex
	var sequence uint64
	err := provider.Duplex(liveCtx, input, func(e Event) error {
		delivery.Lock()
		defer delivery.Unlock()
		if liveCtx.Err() != nil {
			return liveCtx.Err()
		}
		sequence++
		e.TurnID = id
		e.Sequence = sequence
		return emit(e)
	})
	delivery.Lock()
	defer delivery.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = false
	s.activeID = 0
	s.cancel = nil
	s.history = nil
	s.previousLanguage = ""
	if s.failure != nil {
		return s.failure
	}
	if err != nil {
		s.state = "error"
		s.detail = err.Error()
		return err
	}
	s.state = "ready"
	s.detail = "microphone ready"
	return nil
}
