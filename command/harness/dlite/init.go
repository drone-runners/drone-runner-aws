package dlite

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/httphelper"

	"github.com/drone-runners/drone-runner-aws/command/harness"
)

const (
	initTimeoutSec        = 30 * 60
	initTimeoutSecForBYOI = 60 * 60
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
	setupResp, selectedPoolDriver, err := harness.HandleSetup(
		ctx, &req.SetupVMRequest, poolManager.GetStageOwnerStore(), poolManager.GetCapacityReservationStore(),
		t.c.env.Runner.Volumes, t.c.env.Dlite.PoolMapByAccount.Convert(),
		t.c.env.Runner.Name, t.c.env.LiteEngine.EnableMock, t.c.env.LiteEngine.MockStepTimeoutSecs,
		poolManager, t.c.metrics, t.c.env.Settings.FallbackPoolIDs)
	if err != nil {
		t.c.metrics.ErrorCount.WithLabelValues(accountID, strconv.FormatBool(req.Distributed)).Inc()
		logr.WithError(err).WithField("account_id", accountID).Error("could not setup VM")
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
		resp, err := harness.HandleStep(ctx, req.Services[i], poolManager.GetStageOwnerStore(), t.c.env.Runner.Volumes,
			t.c.env.LiteEngine.EnableMock, t.c.env.LiteEngine.MockStepTimeoutSecs, poolManager, t.c.metrics, false)
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
		PoolDriverUsed:        selectedPoolDriver,
		GitspacesPortMappings: setupResp.GitspacesPortMappings,
		InstanceInfo:          setupResp.InstanceInfo,
	}
	httphelper.WriteJSON(w, resp, httpOK)
}
