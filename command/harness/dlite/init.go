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

const (
	initTimeoutSec = 30 * 60
)

type VMInitTask struct {
	c *dliteCommand
}

type VMInitRequest struct {
	SetupVMRequest harness.SetupVMRequest      `json:"setup_vm_request"`
	Services       []*harness.ExecuteVMRequest `json:"services"`
	Distributed    bool                        `json:"distributed,omitempty"`
}

func (t *VMInitTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), initTimeoutSec*time.Second) // TODO: Get this from the request
	defer cancel()

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

	accountID := harness.GetAccountID(&req.SetupVMRequest.Context, map[string]string{})

	// Make the setup call
	req.SetupVMRequest.CorrelationID = task.ID
	poolManager := t.c.getPoolManager(req.Distributed)
	setupResp, selectedPoolDriver, err := harness.HandleSetup(ctx, &req.SetupVMRequest, poolManager.GetStageOwnerStore(), &t.c.env, poolManager, t.c.metrics)
	if err != nil {
		t.c.metrics.ErrorCount.WithLabelValues(accountID, strconv.FormatBool(req.Distributed)).Inc()
		logr.WithError(err).Error("could not setup VM")
		httphelper.WriteJSON(w, failedResponse(err.Error()), httpFailed)
		return
	}

	serviceStatuses := []VMServiceStatus{}
	var status VMServiceStatus

	// Start all the services
	for i, s := range req.Services {
		req.Services[i].IPAddress = setupResp.IPAddress
		req.Services[i].CorrelationID = task.ID
		status = VMServiceStatus{ID: s.ID, Name: s.Name, Image: s.Image, LogKey: s.LogKey, Status: Running, ErrorMessage: ""}
		resp, err := harness.HandleStep(ctx, req.Services[i], poolManager.GetStageOwnerStore(), &t.c.env, poolManager, t.c.metrics, false)
		if err != nil {
			status.Status = Error
			status.ErrorMessage = err.Error()
		} else if resp.Error != "" {
			status.Status = Error
			status.ErrorMessage = resp.Error
		}
		serviceStatuses = append(serviceStatuses, status)
	}

	// Construct final response
	resp := VMTaskExecutionResponse{
		ServiceStatuses:        serviceStatuses,
		IPAddress:              setupResp.IPAddress,
		CommandExecutionStatus: Success,
		DelegateMetaInfo: DelegateMetaInfo{
			HostName: t.c.delegateInfo.Host,
			ID:       t.c.delegateInfo.ID,
		},
		PoolDriverUsed: selectedPoolDriver,
	}
	httphelper.WriteJSON(w, resp, httpOK)
}
