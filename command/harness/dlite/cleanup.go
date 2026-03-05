package dlite

import (
	"context"
	"net/http"
	"strconv"

	"github.com/drone-runners/drone-runner-aws/command/harness"
)

// VMCleanupTask handles VM cleanup/destroy tasks.
type VMCleanupTask struct {
	c *dliteCommand
}

// ServeHTTP handles the VM cleanup task request.
func (t *VMCleanupTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	_, taskBytes, logr, ok := decodeTask(w, r)
	if !ok {
		return
	}

	req := &harness.VMCleanupRequest{}
	if !unmarshalTaskRequest(w, taskBytes, req, logr) {
		return
	}

	accountID := harness.GetAccountID(&req.Context, map[string]string{})

	// Handle non-distributed context state cleanup.
	if !req.Distributed {
		harness.GetCtxState().Delete(req.StageRuntimeID)
	}

	// Execute cleanup.
	vmService := t.c.getVMService()
	err := vmService.Destroy(ctx, req)
	if err != nil {
		t.c.runner.Metrics.ErrorCount.WithLabelValues(accountID, strconv.FormatBool(req.Distributed)).Inc()
		logr.WithError(err).WithField("account_id", accountID).Error("could not destroy VM")
		writeErrorResponse(w, err)
		return
	}

	// Construct success response.
	resp := VMTaskExecutionResponse{
		CommandExecutionStatus: Success,
		DelegateMetaInfo: DelegateMetaInfo{
			HostName: t.c.delegateInfo.Host,
			ID:       t.c.delegateInfo.ID,
		},
	}

	writeSuccessResponse(w, &resp)
}
