package dlite

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/sirupsen/logrus"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/httphelper"
)

type VMCleanupTask struct {
	c *dliteCommand
}

func (t *VMCleanupTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context()) // TODO: Get this from http Request
	defer cancel()
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
	accountID := harness.GetAccountID(&req.Context, map[string]string{})
	poolManager := t.c.getPoolManager(req.Distributed)
	if !req.Distributed {
		harness.GetCtxState().Delete(req.StageRuntimeID)
	}
	err = harness.HandleDestroy(ctx, req, poolManager.GetStageOwnerStore(), &t.c.env, poolManager, t.c.metrics)
	if err != nil {
		t.c.metrics.ErrorCount.WithLabelValues(accountID, strconv.FormatBool(req.Distributed)).Inc()
		logr.WithError(err).Error("could not destroy VM")
		httphelper.WriteJSON(w, failedResponse(err.Error()), httpFailed)
		return
	}
	resp := VMTaskExecutionResponse{
		CommandExecutionStatus: Success,
		DelegateMetaInfo: DelegateMetaInfo{
			HostName: t.c.delegateInfo.Host,
			ID:       t.c.delegateInfo.ID,
		},
	}
	httphelper.WriteJSON(w, resp, httpOK)
}
