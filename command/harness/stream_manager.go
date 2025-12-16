package harness

import (
	"context"
	"sync"

	"github.com/sirupsen/logrus"
)

// StreamManager manages lite-engine log streaming lifecycles
type StreamManager struct {
	mu      sync.RWMutex
	streams map[string]context.CancelFunc // map[stageRuntimeID]cancelFunc
}

var (
	streamManager     *StreamManager
	streamManagerOnce sync.Once
)

// GetStreamManager returns the singleton StreamManager instance
func GetStreamManager() *StreamManager {
	streamManagerOnce.Do(func() {
		streamManager = &StreamManager{
			streams: make(map[string]context.CancelFunc),
		}
	})
	return streamManager
}

// RegisterStream stores the cancel function for a lite-engine log stream
func (sm *StreamManager) RegisterStream(stageRuntimeID string, cancelFunc context.CancelFunc) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	// If there's already a stream for this stage, cancel it first
	if existingCancel, exists := sm.streams[stageRuntimeID]; exists {
		existingCancel()
		logrus.WithField("stage_runtime_id", stageRuntimeID).
			Warnln("cancelled existing stream before registering new one")
	}
	
	sm.streams[stageRuntimeID] = cancelFunc
	logrus.WithField("stage_runtime_id", stageRuntimeID).
		Traceln("registered lite-engine log stream")
}

// CloseStream cancels the stream for the given stage and removes it from the map
func (sm *StreamManager) CloseStream(stageRuntimeID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	if cancelFunc, exists := sm.streams[stageRuntimeID]; exists {
		cancelFunc()
		delete(sm.streams, stageRuntimeID)
		logrus.WithField("stage_runtime_id", stageRuntimeID).
			Infoln("closed lite-engine log stream")
	} else {
		logrus.WithField("stage_runtime_id", stageRuntimeID).
			Traceln("no stream found to close")
	}
}

// CleanupStaleStreams removes streams that are no longer active (optional maintenance)
func (sm *StreamManager) CleanupStaleStreams() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	// This could be enhanced to track stream creation time and clean up old streams
	logrus.WithField("active_streams", len(sm.streams)).
		Traceln("stream manager status")
}

