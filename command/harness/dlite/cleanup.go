package dlite

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/sirupsen/logrus"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/httphelper"
)

type VMCleanupTask struct {
	c *dliteCommand
}

func (t *VMCleanupTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background() // TODO: Get this from http Request
	log := logrus.New()
	task := &client.Task{}
	err := json.NewDecoder(r.Body).Decode(task)
	if err != nil {
		log.Errorln("could not decode HTTP body: %s", err)
		httphelper.WriteBadRequest(w, err)
		return
	}
	logr := log.WithField("task_id", task.ID)
	// Unmarshal the task data
	taskBytes, err := task.Data.MarshalJSON()
	if err != nil {
		logr.Errorln("could not unmarshal task data: %s", err)
		httphelper.WriteBadRequest(w, err)
		return
	}
	req := &harness.VMCleanupRequest{}
	err = json.Unmarshal(taskBytes, req)
	if err != nil {
		logr.Errorln("could not unmarshal task request data: %s", err)
		httphelper.WriteBadRequest(w, err)
		return
	}
	err = harness.HandleDestroy(ctx, req, t.c.stageOwnerStore, t.c.poolManager)
	if err != nil {
		logr.Errorln("could not destroy VM: %s", err)
		httphelper.WriteJSON(w, failedResponse(err.Error()), httpFailed)
	}
	httphelper.WriteJSON(w, VMTaskExecutionResponse{CommandExecutionStatus: Success}, httpOK)
}
