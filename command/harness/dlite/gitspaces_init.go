package dlite

import (
	"context"
	"encoding/json"
	"github.com/drone-runners/drone-runner-aws/types"
	"net/http"
	"strconv"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/sirupsen/logrus"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/httphelper"
)

type GitspacesVMInitTask struct {
	c *dliteCommand
}

type GitspacesVMInitRequest struct {
	SetupVMRequest      harness.SetupVMRequest    `json:"setup_vm_request"`
	Distributed         bool                      `json:"distributed,omitempty"`
	GitspaceAgentConfig types.GitspaceAgentConfig `json:"gitspace_agent_config"`
}

func (t *GitspacesVMInitTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), initTimeoutSec*time.Second) // TODO: Get this from the request
	defer cancel()

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
	req := &GitspacesVMInitRequest{}
	err = json.Unmarshal(taskBytes, req)
	if err != nil {
		logr.WithError(err).Errorln("could not unmarshal task request data")
		httphelper.WriteBadRequest(w, err)
		return
	}

	accountID := harness.GetAccountID(&req.SetupVMRequest.Context, map[string]string{})

	// Make the setup call
	req.SetupVMRequest.CorrelationID = task.ID
	poolManager := t.c.getPoolManager(req.Distributed)
	setupResp, selectedPoolDriver, err := harness.HandleSetup(ctx, &req.SetupVMRequest, poolManager.GetStageOwnerStore(), &t.c.env, poolManager, t.c.metrics, &req.GitspaceAgentConfig)
	if err != nil {
		t.c.metrics.ErrorCount.WithLabelValues(accountID, strconv.FormatBool(false)).Inc()
		logr.WithError(err).WithField("account_id", accountID).Error("could not setup VM")
		httphelper.WriteJSON(w, failedResponse(err.Error()), httpFailed)
		return
	}

	// Construct final response
	resp := GitspacesVMTaskExecutionResponse{
		VMTaskExecutionResponse: VMTaskExecutionResponse{
			IPAddress:              setupResp.IPAddress,
			CommandExecutionStatus: Success,
			DelegateMetaInfo: DelegateMetaInfo{
				HostName: t.c.delegateInfo.Host,
				ID:       t.c.delegateInfo.ID,
			},
			PoolDriverUsed: selectedPoolDriver,
		},
		GitspacesAgentPort: setupResp.GitspacesAgentPort,
		GitspacesSshPort:   setupResp.GitspacesSshPort,
	}
	httphelper.WriteJSON(w, resp, httpOK)
}
