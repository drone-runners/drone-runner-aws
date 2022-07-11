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

type VMExecuteTask struct {
	c *dliteCommand
}

type VMExecuteTaskRequest struct {
	ExecuteVMRequest harness.ExecuteVMRequest `json:"execute_step_request"`
}

func (t *VMExecuteTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	req := &VMExecuteTaskRequest{}
	err = json.Unmarshal(taskBytes, req)
	if err != nil {
		logger.WriteBadRequest(w, err)
		return
	}

	resp, err := harness.HandleStep(ctx, &req.ExecuteVMRequest, &t.c.env, t.c.poolManager)
	if err != nil {
		logger.WriteJSON(w, failedResponse(err.Error()), httpFailed)
		return
	}
	logger.WriteJSON(w, convert(resp), httpOK)
}

// convert poll response to a Vm task execution response
func convert(r *api.PollStepResponse) VMTaskExecutionResponse {
	if r.Error == "" {
		return VMTaskExecutionResponse{CommandExecutionStatus: Success, OutputVars: r.Outputs}
	}
	return VMTaskExecutionResponse{CommandExecutionStatus: Failure, ErrorMessage: r.Error}
}
