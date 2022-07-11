package dlite

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/logger"
)

type VmInitTask struct {
	c *dliteCommand
}

type VmInitRequest struct {
	SetupVmRequest harness.SetupVmRequest     `json:"setup_vm_request"`
	Services       []harness.ExecuteVmRequest `json:"services"`
}

func (t *VmInitTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	req := &VmInitRequest{}
	err = json.Unmarshal(taskBytes, req)
	if err != nil {
		logger.WriteBadRequest(w, err)
		return
	}

	// fmt.Printf("init request: %+v\n", req)

	// Make the setup call
	setupResp, err := harness.HandleSetup(ctx, &req.SetupVmRequest, t.c.stageOwnerStore, t.c.env, t.c.poolManager)
	if err != nil {
		logger.WriteJSON(w, failedResponse(err.Error()), 500)
		return
	}

	serviceStatuses := []VmServiceStatus{}

	// Start all the services
	for _, s := range req.Services {
		s.IPAddress = setupResp.IPAddress
		status := VmServiceStatus{ID: s.ID, Name: s.Name, Image: s.Image, LogKey: s.LogKey, Status: Running}
		resp, err := harness.HandleStep(ctx, &s, t.c.env, t.c.poolManager)
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
	resp := VmTaskExecutionResponse{}
	resp.ServiceStatuses = serviceStatuses
	resp.IPAddress = setupResp.IPAddress
	resp.CommandExecutionStatus = Success
	logger.WriteJSON(w, resp, 200)
}
