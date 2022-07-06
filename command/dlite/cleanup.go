package dlite

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/logger"
)

type VmCleanupTask struct {
	c *dliteCommand
}

type VmCleanupRequest struct {
	PoolID         string `json:"pool_id"`
	StageRuntimeID string `json:"stage_runtime_id"`
}

func (t *VmCleanupTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background() // TODO: Get this from http Request
	task := &client.Task{}
	err := json.NewDecoder(r.Body).Decode(task)
	if err != nil {
		logger.WriteBadRequest(w, err)
		return
	}
	// Unmarshal the task data
	taskBytes, err := task.Data.MarshalJSON()
	if err != nil {
		logger.WriteBadRequest(w, err)
		return
	}
	req := &VmCleanupRequest{}
	err = json.Unmarshal(taskBytes, req)
	if err != nil {
		logger.WriteBadRequest(w, err)
		return
	}
	err = t.c.handleDestroy(ctx, req)
	if err != nil {
		logger.WriteJSON(w, failedResponse(err.Error()), 500)
	}
	logger.WriteJSON(w, VmTaskExecutionResponse{CommandExecutionStatus: Success}, 200)
}
