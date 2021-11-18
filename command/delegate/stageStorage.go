package delegate

import (
	"fmt"
	"sync"
	"time"

	"github.com/drone-runners/drone-runner-aws/engine"
)

// Stages is storage for stages
// Defined as interface to later add support for storage in external databases.
// TODO: Currently defined as global variable, change to use dependency injection
var Stages StageStorage

func init() { // nolint: gochecknoinits
	s := &storage{}
	s.storage = make(map[string]*stageStorageEntry)
	Stages = s
}

type StageStorage interface {
	Store(id string, instance engine.CloudInstance) error
	Remove(id string) (bool, error)
	Get(id string) (stage engine.CloudInstance, err error)
}

type storage struct {
	sync.Mutex
	storage map[string]*stageStorageEntry
}

type stageStorageEntry struct {
	sync.Mutex

	AddedAt  time.Time
	Instance engine.CloudInstance
}

func (s *storage) Store(stageID string, instance engine.CloudInstance) error {
	s.Lock()
	defer s.Unlock()

	_, ok := s.storage[stageID]
	if ok {
		return fmt.Errorf("stage with id=%s already present", stageID)
	}

	s.storage[stageID] = &stageStorageEntry{
		AddedAt:  time.Now(),
		Instance: instance,
	}

	return nil
}

func (s *storage) Remove(id string) (bool, error) {
	s.Lock()
	defer s.Unlock()

	_, ok := s.storage[id]
	if !ok {
		return false, nil
	}

	delete(s.storage, id)

	return true, nil
}

func (s *storage) Get(id string) (instance engine.CloudInstance, err error) {
	s.Lock()
	defer s.Unlock()

	entry := s.storage[id]

	return entry.Instance, nil
}
