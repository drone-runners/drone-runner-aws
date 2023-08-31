package dlite

import (
	"context"
	"sync"
)

var (
	cState *CtxState
	once   sync.Once
)

// CtxState stores the cancel contexts for all the steps of a stage.
type CtxState struct {
	mu  sync.Mutex
	ctx map[string]map[string]*context.CancelFunc
}

func (c *CtxState) Add(cancel context.CancelFunc, stageRuntimeID, taskID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.ctx[stageRuntimeID]; !ok {
		c.ctx[stageRuntimeID] = make(map[string]*context.CancelFunc)
	}
	c.ctx[stageRuntimeID][taskID] = &cancel
}

func (c *CtxState) Delete(stageRuntimeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, cancel := range c.ctx[stageRuntimeID] {
		(*cancel)()
	}

	delete(c.ctx, stageRuntimeID)
}

func (c *CtxState) DeleteTask(stageRuntimeID, taskID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.ctx[stageRuntimeID], taskID)
}

func ctxState() *CtxState {
	once.Do(func() {
		cState = &CtxState{
			mu:  sync.Mutex{},
			ctx: make(map[string]map[string]*context.CancelFunc),
		}
	})
	return cState
}
