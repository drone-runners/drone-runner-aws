package harness

import (
	"context"
	"fmt"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	ierrors "github.com/drone-runners/drone-runner-aws/app/types"
	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/harness/lite-engine/api"
	"github.com/harness/lite-engine/logger"
	"github.com/sirupsen/logrus"
)

const suspendTimeout = 5 * time.Minute

type SuspendVMRequest struct {
	PoolID             string `json:"pool_id"`
	StageRuntimeID     string `json:"stage_runtime_id"`
	api.SuspendRequest `json:"suspend_request"`
	Context            Context             `json:"context,omitempty"`
	InstanceInfo       common.InstanceInfo `json:"instance_info,omitempty"`
}

func HandleSuspend(
	ctx context.Context,
	r *SuspendVMRequest,
	enableMock bool, // only used for scale testing
	mockTimeoutSecs int, // only used for scale testing
	poolManager drivers.IManager,
) error {
	if r.StageRuntimeID == "" {
		return ierrors.NewBadRequestError("mandatory field 'stage_runtime_id' in the request body is empty")
	}
	logr := logrus.
		WithField("stage_runtime_id", r.StageRuntimeID).
		WithField("api", "suspend").
		WithField("task_id", r.Context.TaskID)

	logr.Info("Processing suspend request")

	if err := common.ValidateStruct(r.InstanceInfo); err != nil {
		errorMessage := fmt.Sprintf("invalid instance info: %s", err.Error())
		logr.Errorln(errorMessage)
		return ierrors.NewBadRequestError(errorMessage)
	}

	instance := common.BuildInstanceFromRequest(r.InstanceInfo)
	ctx = logger.WithContext(ctx, logr)
	logr = logr.WithField("ip", instance.Address)
	logr.Traceln("Found instance information in the request")

	client, err := lehelper.GetClient(instance, poolManager.GetTLSServerName(), instance.Port, enableMock, mockTimeoutSecs)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	logr.Traceln("created lite engine client")

	_, err = client.RetrySuspend(ctx, &api.SuspendRequest{
		LogKey:         r.LogKey,
		Labels:         r.Labels,
		LiteEnginePath: oshelp.GetLiteEngineLogsPath(instance.OS),
	}, suspendTimeout)
	if err != nil {
		return fmt.Errorf("failed to call LE.RetrySuspend: %w", err)
	}
	logr.Traceln("called lite engine suspend")

	if err = poolManager.Suspend(ctx, r.PoolID, instance.ID, instance.Zone); err != nil {
		return fmt.Errorf("failed to suspend instance: %w", err)
	}

	logr.Infoln("Suspend request completed")
	return nil
}
