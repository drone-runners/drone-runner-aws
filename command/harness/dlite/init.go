package dlite

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/command/harness"
)

// Timeout constants for init tasks.
const (
	initTimeoutSec        = 30 * 60
	initTimeoutSecForBYOI = 60 * 60
)

// VMInitTask handles VM initialization tasks.
type VMInitTask struct {
	c *dliteCommand
}

// VMInitRequest represents the request payload for VM initialization.
type VMInitRequest struct {
	SetupVMRequest harness.SetupVMRequest      `json:"setup_vm_request"`
	Services       []*harness.ExecuteVMRequest `json:"services"`
	Distributed    bool                        `json:"distributed,omitempty"`
}

// ServeHTTP handles the VM init task request.
func (t *VMInitTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	task, taskBytes, logr, ok := decodeTask(w, r)
	if !ok {
		return
	}

	req := &VMInitRequest{}
	if !unmarshalTaskRequest(w, taskBytes, req, logr) {
		return
	}

	// Determine timeout based on BYOI setting.
	timeout := initTimeoutSec
	if val, ok := req.SetupVMRequest.SetupRequest.Envs["CI_ENABLE_BYOI_HOSTED"]; ok && val == "true" {
		timeout = initTimeoutSecForBYOI
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Second)
	defer cancel()

	accountID := harness.GetAccountID(&req.SetupVMRequest.Context, map[string]string{})

	// Set correlation ID.
	req.SetupVMRequest.CorrelationID = task.ID

	// Get VM service and execute setup.
	vmService := t.c.getVMService()
	setupResp, selectedPoolDriver, err := vmService.Setup(ctx, &req.SetupVMRequest)
	if err != nil {
		t.c.runner.Metrics.ErrorCount.WithLabelValues(accountID, strconv.FormatBool(req.Distributed)).Inc()
		logr.WithError(err).WithField("account_id", accountID).Error("could not setup VM")
		writeErrorResponse(w, err)
		return
	}

	// Start all services.
	serviceStatuses := t.startServices(ctx, req, setupResp, task.ID, logr)

	// Construct final response.
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

	writeSuccessResponse(w, resp)
}

// startServices starts all services for the VM.
func (t *VMInitTask) startServices(
	ctx context.Context,
	req *VMInitRequest,
	setupResp *harness.SetupVMResponse,
	taskID string,
	logr *logrus.Entry,
) []VMServiceStatus {
	serviceStatuses := make([]VMServiceStatus, 0, len(req.Services))
	vmService := t.c.getVMService()

	for i, s := range req.Services {
		// Set IP and correlation ID.
		req.Services[i].IPAddress = setupResp.IPAddress
		req.Services[i].CorrelationID = taskID

		status := VMServiceStatus{
			ID:           s.ID,
			Name:         s.Name,
			Image:        s.Image,
			LogKey:       s.LogKey,
			Status:       Running,
			ErrorMessage: "",
		}

		resp, err := vmService.Step(ctx, req.Services[i], false)
		if err != nil {
			status.Status = Error
			status.ErrorMessage = err.Error()
		} else if resp.Error != "" {
			status.Status = Error
			status.ErrorMessage = resp.Error
		}

		serviceStatuses = append(serviceStatuses, status)
	}

	return serviceStatuses
}
