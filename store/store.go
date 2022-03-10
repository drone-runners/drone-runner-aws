package store

import (
	"context"
	"sync"

	"github.com/drone-runners/drone-runner-aws/core"
)

func NewInstanceStore() core.InstanceStore {
	return &instanceStore{
		mu:        new(sync.Mutex),
		instances: map[string]*core.Instance{},
	}
}

type instanceStore struct {
	mu        sync.Locker
	instances map[string]*core.Instance
}

func (s *instanceStore) List(_ context.Context) ([]*core.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := []*core.Instance{}
	for _, instance := range s.instances {
		list = append(list, instance)
	}
	return list, nil
}

func (s *instanceStore) Create(_ context.Context, instance *core.Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instances[instance.ID] = instance
	return nil
}

func (s *instanceStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.instances, id)
	return nil
}

func (s *instanceStore) Update(_ context.Context, instance *core.Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instances[instance.ID] = instance
	return nil
}

func (s *instanceStore) Find(_ context.Context, id string) (*core.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, exists := s.instances[id]
	if !exists {
		return nil, core.ErrInstanceNotFound
	}
	return value, nil
}
