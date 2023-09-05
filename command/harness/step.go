package harness

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/harness/scripts"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/lehelper"
	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
	ierrors "github.com/drone-runners/drone-runner-aws/internal/types"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/harness/lite-engine/api"
	lespec "github.com/harness/lite-engine/engine/spec"
	"github.com/harness/lite-engine/logger"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type ExecuteVMRequest struct {
	StageRuntimeID       string `json:"stage_runtime_id"`
	InstanceID           string `json:"instance_id"`
	IPAddress            string `json:"ip_address"`
	PoolID               string `json:"pool_id"`
	CorrelationID        string `json:"correlation_id"`
	TaskID               string `json:"task_id,omitempty"`
	Distributed          bool   `json:"distributed,omitempty"`
	api.StartStepRequest `json:"start_step_request"`
}

var (
	StepTimeout = 10 * time.Hour
)

func HandleStep(ctx context.Context,
	r *ExecuteVMRequest,
	s store.StageOwnerStore,
	env *config.EnvConfig,
	poolManager drivers.IManager,
	metrics *metric.Metrics,
	async bool) (*api.PollStepResponse, error) {
	if r.ID == "" && r.IPAddress == "" {
		return nil, ierrors.NewBadRequestError("either parameter 'id' or 'ip_address' must be provided")
	}

	entity, err := s.Find(ctx, r.StageRuntimeID)
	if err != nil || entity == nil {
		return nil, errors.Wrap(err, fmt.Sprintf("failed to find stage owner entity for stage: %s", r.StageRuntimeID))
	}

	poolID := entity.PoolName
	logr := logrus.
		WithField("api", "dlite:step").
		WithField("stage_runtime_id", r.StageRuntimeID).
		WithField("step_id", r.StartStepRequest.ID).
		WithField("pool", poolID).
		WithField("correlation_id", r.CorrelationID).
		WithField("async", async)

	ctx = logger.WithContext(ctx, logr)

	// set the envs from previous step only for non-container steps
	if r.Image == "" {
		setPrevStepExportEnvs(r)
	}

	// add global volumes as mounts only if image is specified
	if r.Image != "" {
		for _, pair := range env.Runner.Volumes {
			src, dest, _, err := resource.ParseVolume(pair) //nolint:govet
			if err != nil {
				logr.Warn(err)
				continue
			}
			mount := &lespec.VolumeMount{
				Name: fileID(src),
				Path: dest,
			}
			r.Volumes = append(r.Volumes, mount)
		}
	}
	inst, err := getInstance(ctx, poolID, r.StageRuntimeID, r.InstanceID, poolManager)
	if err != nil {
		return nil, err
	}

	logr = logr.WithField("ip", inst.Address)

	client, err := lehelper.GetClient(inst, env.Runner.Name, inst.Port, env.LiteEngine.EnableMock, env.LiteEngine.MockStepTimeoutSecs)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	logr.Traceln("running StartStep")

	// Currently the OSX m1 architecture does not enable nested virtualization, so we disable docker.
	if inst.Platform.OS == oshelp.OSMac {
		b := false
		r.StartStepRequest.MountDockerSocket = &b
		if strings.Contains(r.StartStepRequest.Image, "harness/drone-git") {
			r.StartStepRequest.Image = ""
			r.Volumes = nil
			pipelinePlatform, _, _ := poolManager.Inspect(inst.Pool)

			cloneScript := scripts.Clone
			clonePath := fmt.Sprintf("%s/clone.sh", r.StartStepRequest.WorkingDir)

			entrypoint := oshelp.GetEntrypoint(pipelinePlatform.OS)
			command := []string{clonePath}
			r.StartStepRequest.ID = oshelp.Random()
			r.StartStepRequest.Name = "clone"
			r.StartStepRequest.Run.Entrypoint = entrypoint
			r.StartStepRequest.Run.Command = command
			r.StartStepRequest.Files = []*lespec.File{
				{
					Path: clonePath,
					Mode: 0700,
					Data: cloneScript,
				},
			}
		}
	}
	startStepResponse, err := client.RetryStartStep(ctx, &r.StartStepRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to call LE.RetryStartStep: %w", err)
	}

	logr.WithField("startStepResponse", startStepResponse).Traceln("LE.StartStep complete")

	pollResponse := &api.PollStepResponse{}

	if !async {
		pollResponse, err = client.RetryPollStep(ctx, &api.PollStepRequest{ID: r.StartStepRequest.ID}, StepTimeout)
		if err != nil {
			return nil, fmt.Errorf("failed to call LE.RetryPollStep: %w", err)
		}
	}

	logr.WithField("pollResponse", pollResponse).Traceln("completed LE.RetryPollStep")
	if len(pollResponse.Envs) > 0 {
		envState().Add(r.StageRuntimeID, pollResponse.Envs)
	}

	return pollResponse, nil
}

func getInstance(ctx context.Context, poolID, stageRuntimeID,
	instanceID string, poolManager drivers.IManager) (
	*types.Instance, error) {
	if instanceID != "" {
		inst, err := poolManager.Find(ctx, instanceID)
		if err != nil {
			return nil, fmt.Errorf("cannot get the instance by Id %s : %w", instanceID, err)
		}
		return inst, nil
	}

	inst, err := poolManager.GetInstanceByStageID(ctx, poolID, stageRuntimeID)
	if err != nil {
		return nil, fmt.Errorf("cannot get the instance by stageId %s: %w", stageRuntimeID, err)
	}
	return inst, nil
}

func setPrevStepExportEnvs(r *ExecuteVMRequest) {
	prevStepExportEnvs := envState().Get(r.StageRuntimeID)
	for k, v := range prevStepExportEnvs {
		if r.StartStepRequest.Envs == nil {
			r.StartStepRequest.Envs = make(map[string]string)
		}
		r.StartStepRequest.Envs[k] = v
	}
}
