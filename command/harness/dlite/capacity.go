package dlite

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/sirupsen/logrus"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/httphelper"
)

const (
	capacityTimeoutSec = 10 * 60
)

type VMCapacityTask struct {
	c *dliteCommand
}

type VMCapacityRequest struct {
	CapacityReservationRequest harness.CapacityReservationRequest `json:"capacity_reservation_request"`
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
	req := &VMCapacityRequest{}
	err = json.Unmarshal(taskBytes, req)
	if err != nil {
		logr.WithError(err).Errorln("could not unmarshal task request data")
		httphelper.WriteBadRequest(w, err)
		return
	}
	timeout := capacityTimeoutSec
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Second)
	defer cancel()

	accountID := harness.GetAccountID(&req.CapacityReservationRequest.Context, map[string]string{})

	// Make the setup call
	req.CapacityReservationRequest.CorrelationID = task.ID
	poolManager := t.c.distributedPoolManager
	capacityReservationResponse, err := harness.HandleCapacityReservation(
		ctx, &req.CapacityReservationRequest, poolManager.GetCapacityReservationStore(),
		t.c.env.Dlite.PoolMapByAccount.Convert(),
		t.c.env.Runner.Name,
		poolManager)
	if err != nil {
		t.c.metrics.ErrorCount.WithLabelValues(accountID, strconv.FormatBool(true)).Inc()
		logr.WithError(err).WithField("account_id", accountID).Error("could not reserve capacity for VM")
		httphelper.WriteJSON(w, failedResponse(err.Error()), httpFailed)
		return
	}

	if capacityReservationResponse == nil {
		capacityReservationResponse = &types.CapacityReservation{}
	}

	// Construct final response
	resp := VMTaskExecutionResponse{
		CommandExecutionStatus: Success,
		CapacityReservation:    *capacityReservationResponse,
	}
	httphelper.WriteJSON(w, resp, httpOK)
}
