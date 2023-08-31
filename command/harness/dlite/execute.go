package dlite

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/drone-runners/drone-runner-aws/internal/le"
	"github.com/harness/lite-engine/api"
	"github.com/sirupsen/logrus"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/httphelper"
)

type VMExecuteTask struct {
	c *dliteCommand
}

type VMExecuteTaskRequest struct {
	ExecuteVMRequest harness.ExecuteVMRequest `json:"execute_step_request"`
}

func (t *VMExecuteTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(context.Background()) // TODO: (Vistaar) Set this in dlite
	defer cancel()
	log := logrus.New()
	task := &client.Task{}
	factory := &le.LiteEngineClientFactory{}
	err := json.NewDecoder(r.Body).Decode(task)
	if err != nil {
		log.WithError(err).Error("could not decode VM step execute HTTP body")
		httphelper.WriteBadRequest(w, err)
		return
	}
	logr := log.WithField("task_id", task.ID)
	// Unmarshal the task data
	taskBytes, err := task.Data.MarshalJSON()
	if err != nil {
		logr.WithError(err).Error("could not unmarshal task data")
		httphelper.WriteBadRequest(w, err)
		return
	}
	req := &VMExecuteTaskRequest{}
	err = json.Unmarshal(taskBytes, req)
	if err != nil {
		logr.WithError(err).Error("could not unmarshal task request data")
		httphelper.WriteBadRequest(w, err)
		return
	}

	ctxState().Add(cancel, req.ExecuteVMRequest.StageRuntimeID, task.ID)

	req.ExecuteVMRequest.CorrelationID = task.ID
	stepResp, err := harness.HandleStep(ctx, &req.ExecuteVMRequest, t.c.stageOwnerStore, &t.c.env, t.c.poolManager, t.c.metrics, factory)
	ctxState().DeleteTask(req.ExecuteVMRequest.StageRuntimeID, task.ID)
	if err != nil {
		logr.WithError(err).
			WithField("stage_runtime_id", req.ExecuteVMRequest.StageRuntimeID).
			Error("could not execute step")
		httphelper.WriteJSON(w, failedResponse(err.Error()), httpFailed)
		return
	}
	resp := convert(stepResp)
	resp.DelegateMetaInfo.HostName = t.c.delegateInfo.Host
	resp.DelegateMetaInfo.ID = t.c.delegateInfo.ID
	httphelper.WriteJSON(w, resp, httpOK)
}

// convert poll response to a Vm task execution response
func convert(r *api.PollStepResponse) VMTaskExecutionResponse {
	if r.Error == "" {
		return VMTaskExecutionResponse{CommandExecutionStatus: Success, OutputVars: r.Outputs, Artifact: r.Artifact}
	}
	return VMTaskExecutionResponse{CommandExecutionStatus: Failure, ErrorMessage: r.Error}
}
