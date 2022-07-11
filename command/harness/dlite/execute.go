package dlite

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/harness/lite-engine/api"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/logger"
)

type VmExecuteTask struct {
	c *dliteCommand
}

type VmExecuteTaskRequest struct {
	ExecuteVmRequest harness.ExecuteVmRequest `json:"execute_step_request"`
}

func (t *VmExecuteTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background() // TODO: (Vistaar) Set this in dlite
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
	req := &VmExecuteTaskRequest{}
	err = json.Unmarshal(taskBytes, req)
	if err != nil {
		logger.WriteBadRequest(w, err)
		return
	}
	// fmt.Printf("req is: %+v\n", req)
	resp, err := harness.HandleStep(ctx, &req.ExecuteVmRequest, t.c.env, t.c.poolManager)
	if err != nil {
		logger.WriteJSON(w, failedResponse(err.Error()), 500)
		return
	}
	logger.WriteJSON(w, convert(resp), 200)
}

// convert poll response to a Vm task execution response
func convert(r *api.PollStepResponse) VmTaskExecutionResponse {
	if r.Error == "" {
		return VmTaskExecutionResponse{CommandExecutionStatus: Success, OutputVars: r.Outputs}
	}
	return VmTaskExecutionResponse{CommandExecutionStatus: Failure, ErrorMessage: r.Error}
}
