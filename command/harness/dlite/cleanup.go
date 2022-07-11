package dlite

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/logger"
)

type VMCleanupTask struct {
	c *dliteCommand
}

func (t *VMCleanupTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	req := &harness.VmCleanupRequest{}
	err = json.Unmarshal(taskBytes, req)
	if err != nil {
		logger.WriteBadRequest(w, err)
		return
	}
	err = harness.HandleDestroy(ctx, req, t.c.stageOwnerStore, t.c.poolManager)
	if err != nil {
		logger.WriteJSON(w, failedResponse(err.Error()), httpFailed)
	}
	logger.WriteJSON(w, VMTaskExecutionResponse{CommandExecutionStatus: Success}, httpOK)
}
