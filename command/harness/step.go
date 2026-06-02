package harness

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/harness/lite-engine/api"
	lehttp "github.com/harness/lite-engine/cli/client"
	lespec "github.com/harness/lite-engine/engine/spec"
	"github.com/harness/lite-engine/logger"

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
	if err != nil {
		logr.Debugf("Instance information is not passed to the VM Execute Request, fetching it from the DB: %v", err)
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

	logr = logr.WithField("pool", poolID)

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
	// Enable verbose HTTP tracing so the start step request/response is logged.
	enableVerboseHTTP(client, logr)
	startStepResponse, err := client.RetryStartStep(ctx, &r.StartStepRequest, poolManager.GetStartStepTimeout())
	if err != nil {
		if !isStartStepDeadlineExceeded(err) {
			return nil, fmt.Errorf("failed to call LE.RetryStartStep: %w", err)
		}
		logr.Errorln(fmt.Errorf("failed to call LE.RetryStartStep: %w", err))
		// On a start-step deadline we preserve the VM for debugging, probe the
		// lite-engine health and retry the start step. If a retry succeeds the
		// step continues normally while the VM remains in the preserved state.
		startStepResponse, err = recoverStartStepAfterTimeout(ctx, r, inst, client, poolManager, logr)
		if err != nil {
			return nil, err
		}
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

func isStartStepDeadlineExceeded(err error) bool {
	if err == nil {
		return false
	}
	return stderrors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(err.Error(), "context deadline exceeded")
}

func preserveInstanceForDebug(
	ctx context.Context,
	inst *types.Instance,
	poolManager drivers.IManager,
	logr *logrus.Entry,
) error {
	if inst == nil || inst.ID == "" {
		return fmt.Errorf("instance id is required to preserve for debug")
	}

	dbInst, findErr := poolManager.Find(ctx, inst.ID)
	if findErr == nil && dbInst != nil {
		inst = dbInst
	}

	inst.State = types.StatePreserved
	inst.Updated = time.Now().Unix()
	if updateErr := poolManager.Update(ctx, inst); updateErr != nil {
		return fmt.Errorf("failed to update instance for debug preservation: %w", updateErr)
	}

	logr.WithField("instance_id", inst.ID).
		WithField("instance_state", types.StatePreserved).
		Warnln("updated instance state for debug preservation after lite-engine start step timeout")
	return nil
}

const (
	// maxStartStepDebugRetries is the number of additional RetryStartStep
	// attempts performed after a start-step deadline while debugging.
	maxStartStepDebugRetries = 5
	// startStepDebugHealthTimeout bounds the debug health check call.
	startStepDebugHealthTimeout = 30 * time.Second
)

// recoverStartStepAfterTimeout handles a lite-engine start-step deadline by
// preserving the VM, probing lite-engine health (with verbose HTTP tracing)
// and retrying the start step up to maxStartStepDebugRetries times. The VM is
// left in the preserved state regardless of the retry outcome.
func recoverStartStepAfterTimeout(
	ctx context.Context,
	r *ExecuteVMRequest,
	inst *types.Instance,
	client lehttp.Client,
	poolManager drivers.IManager,
	logr *logrus.Entry,
) (*api.StartStepResponse, error) {
	// 1. Preserve the VM so it is not cleaned up.
	if preserveErr := preserveInstanceForDebug(ctx, inst, poolManager, logr); preserveErr != nil {
		logr.WithError(preserveErr).Warnln("failed to mark instance for debug preservation")
	}

	// Verbose HTTP tracing is already enabled before the initial start step call.

	// 2. Health check the lite-engine and print the response.
	healthCtx, cancel := context.WithTimeout(ctx, startStepDebugHealthTimeout)
	defer cancel()
	healthResp, healthErr := client.Health(healthCtx, &api.HealthRequest{})
	if healthErr != nil {
		logr.WithError(healthErr).Warnln("lite-engine health check failed after start step timeout")
	} else {
		logr.WithField("health_ok", healthResp.OK).
			WithField("health_version", healthResp.Version).
			Infoln("lite-engine health check response after start step timeout")
	}

	// 3. Retry the start step up to maxStartStepDebugRetries with default timeout.
	var lastErr error
	for attempt := 1; attempt <= maxStartStepDebugRetries; attempt++ {
		resp, retryErr := client.RetryStartStep(ctx, &r.StartStepRequest, poolManager.GetStartStepTimeout())
		if retryErr == nil {
			logr.WithField("attempt", attempt).
				Infoln("retry start step succeeded after debug preservation; continuing step (vm remains preserved)")
			return resp, nil
		}
		lastErr = retryErr
		logr.WithError(retryErr).
			WithField("attempt", attempt).
			Warnln("retry start step after debug preservation failed")
	}

	return nil, fmt.Errorf("failed to call LE.RetryStartStep after %d debug retries: %w", maxStartStepDebugRetries, lastErr)
}

// enableVerboseHTTP wraps the lite-engine HTTP client transport so that each
// request/response is dumped to the logs. It is a no-op for non-HTTP clients
// (e.g. the mock client used in scale testing).
func enableVerboseHTTP(client lehttp.Client, logr *logrus.Entry) {
	hc, ok := client.(*lehttp.HTTPClient)
	if !ok || hc.Client == nil {
		logr.Warnln("verbose http tracing not enabled: unexpected lite-engine client type")
		return
	}
	base := hc.Client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if _, already := base.(*verboseRoundTripper); already {
		return
	}
	hc.Client.Transport = &verboseRoundTripper{base: base, logr: logr}
}

// verboseRoundTripper logs the full HTTP request and response for debugging.
type verboseRoundTripper struct {
	base http.RoundTripper
	logr *logrus.Entry
}

func (v *verboseRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if dump, derr := httputil.DumpRequestOut(req, true); derr == nil {
		v.logr.WithField("http_request", string(dump)).Infoln("lite-engine http request trace")
	} else {
		v.logr.WithError(derr).Debugln("failed to dump http request")
	}

	resp, err := v.base.RoundTrip(req)
	if err != nil {
		v.logr.WithError(err).WithField("url", req.URL.String()).Warnln("lite-engine http call error")
		return resp, err
	}

	if dump, derr := httputil.DumpResponse(resp, true); derr == nil {
		v.logr.WithField("http_response", string(dump)).Infoln("lite-engine http response trace")
	} else {
		v.logr.WithError(derr).Debugln("failed to dump http response")
	}
	return resp, err
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
