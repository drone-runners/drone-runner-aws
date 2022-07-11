package dlite

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/logger"
)

type VMInitTask struct {
	c *dliteCommand
}

type VMInitRequest struct {
	SetupVMRequest harness.SetupVMRequest     `json:"setup_vm_request"`
	Services       []harness.ExecuteVMRequest `json:"services"`
}

func (t *VMInitTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background() // TODO: Get this from the request
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
	req := &VMInitRequest{}
	err = json.Unmarshal(taskBytes, req)
	if err != nil {
		logger.WriteBadRequest(w, err)
		return
	}

	// Make the setup call
	setupResp, err := harness.HandleSetup(ctx, &req.SetupVMRequest, t.c.stageOwnerStore, &t.c.env, t.c.poolManager)
	if err != nil {
		logger.WriteJSON(w, failedResponse(err.Error()), httpFailed)
		return
	}

	serviceStatuses := []VMServiceStatus{}
	var status VMServiceStatus

	// Start all the services
	for i, s := range req.Services {
		s.IPAddress = setupResp.IPAddress
		status = VMServiceStatus{ID: s.ID, Name: s.Name, Image: s.Image, LogKey: s.LogKey, Status: Running, ErrorMessage: ""}
		resp, err := harness.HandleStep(ctx, &req.Services[i], &t.c.env, t.c.poolManager)
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
	resp := VMTaskExecutionResponse{}
	resp.ServiceStatuses = serviceStatuses
	resp.IPAddress = setupResp.IPAddress
	resp.CommandExecutionStatus = Success
	logger.WriteJSON(w, resp, httpOK)
}
