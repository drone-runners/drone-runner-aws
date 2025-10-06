package dlite

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/sirupsen/logrus"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/httphelper"
)

type VMCapacityTask struct {
	c *dliteCommand
}

type VMCapacityRequest struct {
	SetupVMRequest harness.SetupVMRequest `json:"setup_vm_request"`
	Distributed    bool                   `json:"distributed,omitempty"`
}

func (t *VMCapacityTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := logrus.New()
	task := &client.Task{}
	err := json.NewDecoder(r.Body).Decode(task)
	if err != nil {
		log.WithError(err).Error("could not decode VM setup HTTP body")
		httphelper.WriteBadRequest(w, err)
		return
	}
	logr := log.WithField("task_id", task.ID)
	// Unmarshal the task data
	taskBytes, err := task.Data.MarshalJSON()
	if err != nil {
		logr.WithError(err).Errorln("could not unmarshal task data")
		httphelper.WriteBadRequest(w, err)
		return
	}
	req := &VMInitRequest{}
	err = json.Unmarshal(taskBytes, req)
	if err != nil {
		logr.WithError(err).Errorln("could not unmarshal task request data")
		httphelper.WriteBadRequest(w, err)
		return
	}
	timeout := initTimeoutSec
	if val, ok := req.SetupVMRequest.SetupRequest.Envs["CI_ENABLE_BYOI_HOSTED"]; ok && val == "true" {
		timeout = initTimeoutSecForBYOI
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Second)
	defer cancel()

	accountID := harness.GetAccountID(&req.SetupVMRequest.Context, map[string]string{})

	// Make the setup call
	req.SetupVMRequest.CorrelationID = task.ID
	poolManager := t.c.getPoolManager(req.Distributed)
	setupResp, err := harness.HandleCapacityReservation(
		ctx, &req.SetupVMRequest, poolManager.GetCapacityReservationStore(),
		t.c.env.Runner.Volumes, t.c.env.Dlite.PoolMapByAccount.Convert(),
		t.c.env.Runner.Name,
		poolManager, t.c.metrics)
	if err != nil {
		t.c.metrics.ErrorCount.WithLabelValues(accountID, strconv.FormatBool(req.Distributed)).Inc()
		logr.WithError(err).WithField("account_id", accountID).Error("could not setup VM")
		httphelper.WriteJSON(w, failedResponse(err.Error()), httpFailed)
		return
	}

	// Construct final response
	resp := VMTaskExecutionResponse{
		CapacityReservation: *setupResp,
	}
	httphelper.WriteJSON(w, resp, httpOK)
}
