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
		log.WithError(err).Error("could not decode VM cleanup HTTP body")
		httphelper.WriteBadRequest(w, err)
		return
	}
	logr := log.WithField("task_id", task.ID)
	// Unmarshal the task data
	taskBytes, err := task.Data.MarshalJSON()
	if err != nil {
		logr.WithError(err).Error("could not unmarshal task data")
		httphelper.WriteBadRequest(w, err)
		return
	}
	req := &harness.VMCleanupRequest{}
	err = json.Unmarshal(taskBytes, req)
	if err != nil {
		logr.WithError(err).Error("could not unmarshal task request data")
		httphelper.WriteBadRequest(w, err)
		return
	}
	err = harness.HandleDestroy(ctx, req, t.c.stageOwnerStore, t.c.poolManager)
	if err != nil {
		logr.WithError(err).Error("could not destroy VM")
		httphelper.WriteJSON(w, failedResponse(err.Error()), httpFailed)
	}
	resp := VMTaskExecutionResponse{}
	resp.CommandExecutionStatus = Success
	resp.DelegateMetaInfo.HostName = t.c.delegateInfo.Host
	resp.DelegateMetaInfo.ID = t.c.delegateInfo.ID
	httphelper.WriteJSON(w, resp, httpOK)
}
