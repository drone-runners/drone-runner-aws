package dlite

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/harness/lite-engine/api"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/delegate"
	"github.com/wings-software/dlite/httphelper"

	"github.com/drone-runners/drone-runner-aws/command/harness"
)

// Token generation constants.
const (
	audience          = "audience"
	issuer            = "issuer"
	tokenExpiryOffset = time.Minute * 30
)

// VMExecuteTask handles VM step execution tasks.
type VMExecuteTask struct {
	c *dliteCommand
}

// VMExecuteTaskRequest represents the request payload for VM step execution.
type VMExecuteTaskRequest struct {
	ExecuteVMRequest harness.ExecuteVMRequest `json:"execute_step_request"`
	Context          harness.Context          `json:"context,omitempty"`
}

// ServeHTTP handles the VM execute task request.
func (t *VMExecuteTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	task, taskBytes, logr, ok := decodeTask(w, r)
	if !ok {
		return
	}

	req := &VMExecuteTaskRequest{}
	if !unmarshalTaskRequest(w, taskBytes, req, logr) {
		return
	}

	accountID := harness.GetAccountID(&req.Context, map[string]string{})
	req.ExecuteVMRequest.CorrelationID = task.ID
	distributed := req.ExecuteVMRequest.Distributed

	// Setup distributed mode specifics.
	if distributed {
		if err := t.setupDistributedMode(req, task); err != nil {
			logr.WithError(err).
				WithField("stage_runtime_id", req.ExecuteVMRequest.StageRuntimeID).
				WithField("account_id", accountID).
				Error("unable to generate token")
			httphelper.WriteBadRequest(w, err)
			return
		}
	} else {
		harness.GetCtxState().Add(cancel, req.ExecuteVMRequest.StageRuntimeID, task.ID)
		defer harness.GetCtxState().DeleteTask(req.ExecuteVMRequest.StageRuntimeID, task.ID)
	}

	// Execute the step.
	vmService := t.c.getVMService()
	stepResp, err := vmService.Step(ctx, &req.ExecuteVMRequest, distributed)
	if err != nil {
		t.c.runner.Metrics.ErrorCount.WithLabelValues(accountID, strconv.FormatBool(distributed)).Inc()
		logr.WithError(err).
			WithField("stage_runtime_id", req.ExecuteVMRequest.StageRuntimeID).
			WithField("account_id", accountID).
			Error("could not execute step")
		writeErrorResponse(w, err)
		return
	}

	t.handleResponse(w, stepResp, distributed)
}

// setupDistributedMode configures the request for distributed mode execution.
func (t *VMExecuteTask) setupDistributedMode(req *VMExecuteTaskRequest, task *client.Task) error {
	// Generate temp token with step expiry + offset.
	token, err := delegate.Token(
		audience,
		issuer,
		t.c.runner.Config.Dlite.AccountID,
		t.c.runner.Config.Dlite.AccountSecret,
		harness.StepTimeout+tokenExpiryOffset,
	)
	if err != nil {
		return err
	}

	// Set distributed mode fields.
	req.ExecuteVMRequest.StartStepRequest.StageRuntimeID = req.ExecuteVMRequest.StageRuntimeID
	req.ExecuteVMRequest.StepStatus = api.StepStatusConfig{
		Endpoint:       t.c.runner.Config.Dlite.ManagerEndpoint,
		AccountID:      t.c.runner.Config.Dlite.AccountID,
		TaskID:         req.ExecuteVMRequest.TaskID,
		DelegateID:     task.DelegateInfo.ID,
		Token:          token,
		RunnerResponse: task.RunnerResponse,
	}

	return nil
}

// handleResponse writes the appropriate response based on execution mode.
func (t *VMExecuteTask) handleResponse(w http.ResponseWriter, resp *api.PollStepResponse, distributed bool) {
	if distributed {
		var r VMTaskExecutionResponse
		if resp.Error == "" {
			r = VMTaskExecutionResponse{CommandExecutionStatus: RunningState}
		} else {
			r = VMTaskExecutionResponse{CommandExecutionStatus: Failure, ErrorMessage: resp.Error}
		}
		httphelper.WriteJSON(w, r, httpOK)
	} else {
		vmResp := convert(resp)
		vmResp.DelegateMetaInfo.HostName = t.c.delegateInfo.Host
		vmResp.DelegateMetaInfo.ID = t.c.delegateInfo.ID
		httphelper.WriteJSON(w, vmResp, httpOK)
	}
}

// convert converts a PollStepResponse to a VMTaskExecutionResponse.
func convert(r *api.PollStepResponse) VMTaskExecutionResponse {
	if r.Error == "" {
		return VMTaskExecutionResponse{
			CommandExecutionStatus: Success,
			OutputVars:             r.Outputs,
			Artifact:               r.Artifact,
			Outputs:                r.OutputV2,
			OptimizationState:      r.OptimizationState,
		}
	}
	if r.OutputV2 != nil && len(r.OutputV2) > 0 {
		return VMTaskExecutionResponse{
			CommandExecutionStatus: Failure,
			OutputVars:             r.Outputs,
			Outputs:                r.OutputV2,
			ErrorMessage:           r.Error,
			OptimizationState:      r.OptimizationState,
		}
	}
	return VMTaskExecutionResponse{
		CommandExecutionStatus: Failure,
		ErrorMessage:           r.Error,
		OptimizationState:      r.OptimizationState,
	}
}
