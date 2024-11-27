package dlite

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/harness/lite-engine/api"
	"github.com/sirupsen/logrus"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/delegate"
	"github.com/wings-software/dlite/httphelper"
)

const (
	audience          = "audience"
	issuer            = "issuer"
	tokenExpiryOffset = time.Minute * 30
)

type VMExecuteTask struct {
	c *dliteCommand
}

type VMExecuteTaskRequest struct {
	ExecuteVMRequest harness.ExecuteVMRequest `json:"execute_step_request"`
	Context          harness.Context          `json:"context,omitempty"`
}

func (t *VMExecuteTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context()) // TODO: (Vistaar) Set this in dlite
	defer cancel()
	log := logrus.New()
	task := &client.Task{}
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
	accountID := harness.GetAccountID(&req.Context, map[string]string{})

	req.ExecuteVMRequest.CorrelationID = task.ID
	distributed := req.ExecuteVMRequest.Distributed
	poolManager := t.c.getPoolManager(distributed)
	if distributed {
		// create a temp token with step expiry + offset
		token := ""
		token, err = delegate.Token(audience, issuer, t.c.env.Dlite.AccountID, t.c.env.Dlite.AccountSecret, harness.StepTimeout+tokenExpiryOffset)
		if err != nil {
			logr.WithError(err).
				WithField("stage_runtime_id", req.ExecuteVMRequest.StageRuntimeID).
				WithField("account_id", accountID).
				Error("unable to generate token")
			httphelper.WriteBadRequest(w, err)
			return
		}
		// setting this only for distributed mode
		req.ExecuteVMRequest.StartStepRequest.StageRuntimeID = req.ExecuteVMRequest.StageRuntimeID
		req.ExecuteVMRequest.StepStatus = api.StepStatusConfig{
			Endpoint:       t.c.env.Dlite.ManagerEndpoint,
			AccountID:      t.c.env.Dlite.AccountID,
			TaskID:         req.ExecuteVMRequest.TaskID,
			DelegateID:     task.DelegateInfo.ID,
			Token:          token,
			RunnerResponse: task.RunnerResponse,
		}
	} else {
		harness.GetCtxState().Add(cancel, req.ExecuteVMRequest.StageRuntimeID, task.ID)
		defer harness.GetCtxState().DeleteTask(req.ExecuteVMRequest.StageRuntimeID, task.ID)
	}

	var stepResp *api.PollStepResponse
	stepResp, err = harness.HandleStep(ctx, &req.ExecuteVMRequest, poolManager.GetStageOwnerStore(), t.c.env.Runner.Volumes,
		t.c.env.LiteEngine.EnableMock, t.c.env.LiteEngine.MockStepTimeoutSecs, poolManager, t.c.metrics, distributed)
	if err != nil {
		t.c.metrics.ErrorCount.WithLabelValues(accountID, strconv.FormatBool(distributed)).Inc()
		logr.WithError(err).
			WithField("stage_runtime_id", req.ExecuteVMRequest.StageRuntimeID).
			WithField("account_id", accountID).
			Error("could not execute step")
		httphelper.WriteJSON(w, failedResponse(err.Error()), httpFailed)
		return
	}
	t.handleResponse(w, stepResp, distributed)
}

func (t *VMExecuteTask) handleResponse(w http.ResponseWriter, resp *api.PollStepResponse, distributed bool) {
	if distributed {
		var r VMTaskExecutionResponse
		if resp.Error == "" {
			// mark the response running
			r = VMTaskExecutionResponse{CommandExecutionStatus: RunningState}
		} else {
			r = VMTaskExecutionResponse{CommandExecutionStatus: Failure, ErrorMessage: resp.Error}
		}
		httphelper.WriteJSON(w, r, httpOK)
	} else {
		resp := convert(resp)
		resp.DelegateMetaInfo.HostName = t.c.delegateInfo.Host
		resp.DelegateMetaInfo.ID = t.c.delegateInfo.ID
		httphelper.WriteJSON(w, resp, httpOK)
	}
}

// convert poll response to a Vm task execution response
func convert(r *api.PollStepResponse) VMTaskExecutionResponse {
	if r.Error == "" {
		return VMTaskExecutionResponse{CommandExecutionStatus: Success, OutputVars: r.Outputs, Artifact: r.Artifact, Outputs: r.OutputV2, OptimizationState: r.OptimizationState}
	}
	if r.OutputV2 != nil && len(r.OutputV2) > 0 {
		return VMTaskExecutionResponse{CommandExecutionStatus: Failure, OutputVars: r.Outputs, Outputs: r.OutputV2, ErrorMessage: r.Error, OptimizationState: r.OptimizationState}
	}
	return VMTaskExecutionResponse{CommandExecutionStatus: Failure, ErrorMessage: r.Error, OptimizationState: r.OptimizationState}
}
