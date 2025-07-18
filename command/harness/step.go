package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	ierrors "github.com/drone-runners/drone-runner-aws/app/types"
	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/command/harness/scripts"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
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
	CorrelationID        string `json:"correlation_id"`
	TaskID               string `json:"task_id,omitempty"`
	Distributed          bool   `json:"distributed,omitempty"`
	api.StartStepRequest `json:"start_step_request"`
	InstanceInfo         common.InstanceInfo `json:"instance_info,omitempty"`
}

var (
	StepTimeout = 10 * time.Hour
)

func HandleStep(ctx context.Context,
	r *ExecuteVMRequest,
	s store.StageOwnerStore,
	globalVolumes []string,
	enableMock bool, // only used for scale testing
	mockTimeoutSecs int, // only used for scale testing
	poolManager drivers.IManager,
	metrics *metric.Metrics,
	async bool) (*api.PollStepResponse, error) {
	if r.ID == "" && r.IPAddress == "" {
		return nil, ierrors.NewBadRequestError("either parameter 'id' or 'ip_address' must be provided")
	}

	logr := logrus.
		WithField("api", "dlite:step").
		WithField("stage_runtime_id", r.StageRuntimeID).
		WithField("step_id", r.StartStepRequest.ID).
		WithField("correlation_id", r.CorrelationID).
		WithField("async", async)

	var poolID string
	var inst *types.Instance
	err := common.ValidateStruct(r.InstanceInfo)
	// files := []*spec.File{
	// 	{
	// 		Path:  "/shared/.netrc",
	// 		Mode:  0600,
	// 		IsDir: false,
	// 	},
	// }
	// r.Files = files
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Print("Full PoolYaml step JSON : %s", string(b))
	if err != nil {
		logr.Infof("Instance information is not passed to the VM Execute Request, fetching it from the DB: %v", err)
		entity, findStageOwnerErr := s.Find(ctx, r.StageRuntimeID)
		if findStageOwnerErr != nil || entity == nil {
			return nil, errors.Wrap(
				findStageOwnerErr,
				fmt.Sprintf("failed to find stage owner entity for stage: %s", r.StageRuntimeID),
			)
		}
		poolID = entity.PoolName
		inst, err = getInstance(ctx, poolID, r.StageRuntimeID, r.InstanceID, poolManager)
		if err != nil {
			return nil, err
		}
	} else {
		logr.Infoln("Using the instance information from the VM Execute Request")
		inst = common.BuildInstanceFromRequest(r.InstanceInfo)
		poolID = r.InstanceInfo.PoolName
	}

	logr = logrus.WithField("pool", poolID)

	ctx = logger.WithContext(ctx, logr)

	// set the envs from previous step only for non-container steps
	if r.Image == "" {
		setPrevStepExportEnvs(r)
	}

	// add global volumes as mounts only if image is specified
	if r.Image != "" {
		for _, pair := range globalVolumes {
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

	logr = logr.WithField("ip", inst.Address)

	client, err := lehelper.GetClient(inst, poolManager.GetTLSServerName(), inst.Port, enableMock, mockTimeoutSecs)
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
