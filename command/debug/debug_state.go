package debug

import (
	"sync"
)

var (
	state *State
	once  sync.Once
)

// DebugState stores the variables map required for debugging vm flow.
type State struct {
	mu       sync.Mutex
	retainVM map[string]bool
}

func (s *State) Get(stageRuntimeID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	val, ok := s.retainVM[stageRuntimeID]
	if ok {
		return val
	}
	return false
}

func (s *State) Set(stageRuntimeID string, retainVM bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.retainVM[stageRuntimeID]; !ok {
		s.retainVM[stageRuntimeID] = true
	}
	s.retainVM[stageRuntimeID] = retainVM
}

func (s *State) Delete(stageRuntimeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.retainVM, stageRuntimeID)
}

func GetState() *State {
	once.Do(func() {
		state = &State{
			mu:       sync.Mutex{},
			retainVM: make(map[string]bool),
		}
	})
	return state
}
