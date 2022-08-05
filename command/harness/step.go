package harness

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/harness/scripts"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/lehelper"
	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
	errors "github.com/drone-runners/drone-runner-aws/internal/types"
	"github.com/harness/lite-engine/api"
	lespec "github.com/harness/lite-engine/engine/spec"

	"github.com/sirupsen/logrus"
)

type ExecuteVMRequest struct {
	StageRuntimeID       string `json:"stage_runtime_id"`
	IPAddress            string `json:"ip_address"`
	PoolID               string `json:"pool_id"`
	CorrelationID        string `json:"correlation_id"`
	api.StartStepRequest `json:"start_step_request"`
}

var (
	stepTimeout = 4 * time.Hour
)

func HandleStep(ctx context.Context, r *ExecuteVMRequest, env *config.EnvConfig, poolManager *drivers.Manager) (*api.PollStepResponse, error) {
	if r.ID == "" && r.IPAddress == "" {
		return nil, errors.NewBadRequestError("either parameter 'id' or 'ip_address' must be provided")
	}

	if r.PoolID == "" {
		return nil, errors.NewBadRequestError("mandatory field 'pool_id' in the request body is empty")
	}

	logr := logrus.
		WithField("api", "dlite:step").
		WithField("stage_runtime_id", r.StageRuntimeID).
		WithField("step_id", r.StartStepRequest.ID).
		WithField("pool", r.PoolID).
		WithField("correlation_id", r.CorrelationID)

	// add global volumes as mounts only if image is specified
	if r.Image != "" {
		for _, pair := range env.Runner.Volumes {
			src, dest, _, err := resource.ParseVolume(pair)
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
	inst, err := poolManager.GetInstanceByStageID(ctx, r.PoolID, r.StageRuntimeID)
	if err != nil {
		return nil, fmt.Errorf("cannot get the instance by stageId: %w", err)
	}

	logr = logr.WithField("ip", inst.Address)

	client, err := lehelper.GetClient(inst, env.Runner.Name, inst.Port)
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
			pipelinePlatform, _ := poolManager.Inspect(inst.Pool)

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
	startStepResponse, err := client.StartStep(ctx, &r.StartStepRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to call LE.StartStep: %w", err)
	}

	logr.WithField("startStepResponse", startStepResponse).Traceln("LE.StartStep complete")

	pollResponse, err := client.RetryPollStep(ctx, &api.PollStepRequest{ID: r.StartStepRequest.ID}, stepTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to call LE.RetryPollStep: %w", err)
	}

	logr.WithField("pollResponse", pollResponse).Traceln("completed LE.RetryPollStep")

	return pollResponse, nil
}
