package dlite

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/harness/lite-engine/api"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/logger"
)

type VmInitTask struct {
	c *dliteCommand
}

type SetupVmRequest struct {
	ID               string            `json:"id"` // stage runtime ID
	PoolID           string            `json:"pool_id"`
	Tags             map[string]string `json:"tags"`
	CorrelationID    string            `json:"correlation_id"`
	LogKey           string            `json:"log_key"`
	api.SetupRequest `json:"setup_request"`
}

type SetupVmResponse struct {
	IPAddress  string `json:"ip_address"`
	InstanceID string `json:"instance_id"`
}

type VmInitRequest struct {
	SetupVmRequest SetupVmRequest     `json:"setup_vm_request"`
	Services       []ExecuteVmRequest `json:"services"`
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
	setupResp, err := t.c.handleSetup(ctx, &req.SetupVmRequest)
	if err != nil {
		logger.WriteJSON(w, failedResponse(err.Error()), 500)
		return
	}

	serviceStatuses := []VmServiceStatus{}

	// Start all the services
	for _, s := range req.Services {
		s.IPAddress = setupResp.IPAddress
		status := VmServiceStatus{ID: s.ID, Name: s.Name, Image: s.Image, LogKey: s.LogKey, Status: Running}
		resp, err := t.c.handleStep(ctx, &s)
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
