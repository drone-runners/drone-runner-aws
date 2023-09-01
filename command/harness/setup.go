package harness

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/le"
	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
	errors "github.com/drone-runners/drone-runner-aws/internal/types"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"
	"github.com/harness/lite-engine/api"
	lespec "github.com/harness/lite-engine/engine/spec"

	"github.com/sirupsen/logrus"
)

type SetupVMRequest struct {
	ID               string            `json:"id"` // stage runtime ID
	PoolID           string            `json:"pool_id"`
	FallbackPoolIDs  []string          `json:"fallback_pool_ids"`
	Tags             map[string]string `json:"tags"`
	CorrelationID    string            `json:"correlation_id"`
	LogKey           string            `json:"log_key"`
	Context          Context           `json:"context,omitempty"`
	api.SetupRequest `json:"setup_request"`
}

type SetupVMResponse struct {
	IPAddress  string `json:"ip_address"`
	InstanceID string `json:"instance_id"`
}

var (
	healthCheckTimeout = 3 * time.Minute
	freeAccount        = "free"
)

// HandleSetup tries to setup an instance in any of the pools given in the setup request.
// It calls handleSetup internally for each pool instance trying to complete a setup.
func HandleSetup(ctx context.Context, r *SetupVMRequest, s store.StageOwnerStore, env *config.EnvConfig, poolManager *drivers.Manager,
	metrics *metric.Metrics, clientFactory le.ClientFactory) (*SetupVMResponse, string, error) {
	stageRuntimeID := r.ID
	if stageRuntimeID == "" {
		return nil, "", errors.NewBadRequestError("mandatory field 'id' in the request body is empty")
	}

	if r.PoolID == "" {
		return nil, "", errors.NewBadRequestError("mandatory field 'pool_id' in the request body is empty")
	}

	// Sets up logger to stream the logs in case log config is set
	log := logrus.New()
	var logr *logrus.Entry
	if r.SetupRequest.LogConfig.URL == "" {
		log.Out = os.Stdout
		logr = log.WithField("api", "dlite:setup").WithField("correlationID", r.CorrelationID)
	} else {
		wc := getStreamLogger(r.SetupRequest.LogConfig, r.LogKey, r.CorrelationID)
		defer func() {
			if err := wc.Close(); err != nil {
				log.WithError(err).Debugln("failed to close log stream")
			}
		}()

		log.Out = wc
		log.SetLevel(logrus.TraceLevel)
		logr = log.WithField("stage_runtime_id", stageRuntimeID)

		ctx = logger.WithContext(ctx, logger.Logrus(logr))
	}

	// append global volumes to the setup request.
	for _, pair := range env.Runner.Volumes {
		src, _, ro, err := resource.ParseVolume(pair)
		if err != nil {
			log.Warn(err)
			continue
		}
		vol := lespec.Volume{
			HostPath: &lespec.VolumeHostPath{
				ID:       fileID(src),
				Name:     fileID(src),
				Path:     src,
				ReadOnly: ro,
			},
		}
		r.Volumes = append(r.Volumes, &vol)
	}

	logr = AddContext(logr, r.Context, r.Tags)

	pools := []string{}
	pools = append(pools, r.PoolID)
	pools = append(pools, r.FallbackPoolIDs...)

	var selectedPool, selectedPoolDriver string
	var poolErr error
	var instance *types.Instance
	foundPool := false
	fallback := false

	st := time.Now()

	// try to provision an instance with fallbacks
	for idx, p := range pools {
		if idx > 0 {
			fallback = true
		}
		pool := fetchPool(r.SetupRequest.LogConfig.AccountID, p, env.Dlite.PoolMapByAccount)
		logr.WithField("pool_id", pool).Traceln("starting the setup process")
		instance, poolErr = handleSetup(ctx, logr, r, s, env, poolManager, pool, clientFactory)
		if poolErr != nil {
			logr.WithField("pool_id", pool).WithError(poolErr).Errorln("could not setup instance")
			continue
		}
		selectedPool = pool
		foundPool = true
		_, _, selectedPoolDriver = poolManager.Inspect(selectedPool)
		break
	}

	setupTime := time.Since(st) // amount of time it took to provision an instance
	platform, _, driver := poolManager.Inspect(r.PoolID)

	// If a successful fallback happened and we have an instance setup, record it
	if foundPool && instance != nil { // check for instance != nil just in case
		if fallback {
			// fallback metric records the first pool ID which was tried and the associated driver.
			// We don't record final pool which was used as this metric is only used to get data about
			// which drivers and pools are causing fallbacks.
			metrics.PoolFallbackCount.WithLabelValues(r.PoolID, instance.OS, instance.Arch, driver, metric.True).Inc()
		}
		metrics.WaitDurationCount.WithLabelValues(r.PoolID, instance.OS, instance.Arch,
			driver, metric.ConvertBool(fallback)).Observe(setupTime.Seconds())
	} else {
		metrics.FailedCount.WithLabelValues(r.PoolID, platform.OS, platform.Arch, driver).Inc()
		metrics.BuildCount.WithLabelValues(r.PoolID, platform.OS, platform.Arch, driver).Inc()
		if fallback {
			metrics.PoolFallbackCount.WithLabelValues(r.PoolID, platform.OS, platform.Arch, driver, metric.False).Inc()
		}
		return nil, "", fmt.Errorf("could not provision a VM from the pool: %w", poolErr)
	}

	metrics.BuildCount.WithLabelValues(selectedPool, instance.OS, instance.Arch, string(instance.Provider)).Inc()
	resp := &SetupVMResponse{InstanceID: instance.ID, IPAddress: instance.Address}

	logr.WithField("selected_pool", selectedPool).
		WithField("ip", instance.Address).
		WithField("id", instance.ID).
		WithField("instance_name", instance.Name).
		WithField("response", fmt.Sprintf("%+v", resp)).
		WithField("tried_pools", pools).
		Traceln("VM setup is complete")

	return resp, selectedPoolDriver, nil
}

// handleSetup tries to setup an instance in a given pool. It tries to provision an instance and
// run a health check on the lite engine. It returns information about the setup
// VM and an error if setup failed.
// It is idempotent so in case there was a setup failure, it cleans up any intermediate state.
func handleSetup(
	ctx context.Context,
	logr *logrus.Entry,
	r *SetupVMRequest,
	s store.StageOwnerStore,
	env *config.EnvConfig,
	poolManager *drivers.Manager,
	pool string,
	factory le.ClientFactory,
) (*types.Instance, error) {
	var owner string

	// check if the pool exists in the pool manager.
	if !poolManager.Exists(pool) {
		return nil, fmt.Errorf("could not find pool: %s", pool)
	}

	stageRuntimeID := r.ID

	// add an entry in stage pool mapping if it doesn't exist.
	_, findErr := s.Find(ctx, stageRuntimeID)
	if findErr != nil {
		if cerr := s.Create(ctx, &types.StageOwner{StageID: stageRuntimeID, PoolName: pool}); cerr != nil {
			return nil, fmt.Errorf("could not create stage owner entity: %w", cerr)
		}
	}

	// TODO: Remove this once we start populating license information.
	if strings.Contains(r.PoolID, freeAccount) {
		owner = freeAccount
	} else {
		owner = getAccountID(r.Context, r.Tags)
	}

	// try to provision an instance from the pool manager.
	instance, err := poolManager.Provision(ctx, pool, owner, env)
	if err != nil {
		if derr := s.Delete(ctx, stageRuntimeID); derr != nil {
			logr.WithError(derr).Errorln("could not remove stage ID mapping after provision failure")
		}
		return nil, fmt.Errorf("failed to provision instance: %w", err)
	}

	logr = logr.WithField("pool_id", pool).
		WithField("ip", instance.Address).
		WithField("id", instance.ID).
		WithField("instance_name", instance.Name)

	logr.Traceln("successfully provisioned VM in pool")

	// cleanUpInstanceFn is a function to terminate the instance if an error occurs later in the handleSetup function
	cleanUpInstanceFn := func(consoleLogs bool) {
		if consoleLogs {
			out, logErr := poolManager.InstanceLogs(context.Background(), pool, instance.ID)
			if logErr != nil {
				logr.WithError(logErr).Errorln("failed to fetch console output logs")
			} else {
				// Serial console output is limited to 60000 characters since stackdriver only supports 64KB per log entry
				l := math.Min(float64(len(out)), 60000) //nolint:gomnd
				logrus.WithField("id", instance.ID).
					WithField("instance_name", instance.Name).
					WithField("ip", instance.Address).
					WithField("pool_id", pool).
					WithField("stage_runtime_id", stageRuntimeID).
					Infof("serial console output: %s", out[len(out)-int(l):])
			}
		}
		err = poolManager.Destroy(context.Background(), pool, instance.ID)
		if err != nil {
			logr.WithError(err).Errorln("failed to cleanup instance on setup failure")
		}
	}

	cleanUpStageOwnerMappingFn := func() {
		err = s.Delete(context.Background(), stageRuntimeID)
		if err != nil {
			logr.WithError(err).Errorln("failed to remove stage owner mapping")
		}
	}

	if instance.IsHibernated {
		instance, err = poolManager.StartInstance(ctx, pool, instance.ID)
		if err != nil {
			defer cleanUpStageOwnerMappingFn()
			go cleanUpInstanceFn(false)
			return nil, fmt.Errorf("failed to start the instance up: %w", err)
		}
	}

	instance.Stage = stageRuntimeID
	instance.Updated = time.Now().Unix()
	err = poolManager.Update(ctx, instance)
	if err != nil {
		defer cleanUpStageOwnerMappingFn()
		go cleanUpInstanceFn(false)
		return nil, fmt.Errorf("failed to tag: %w", err)
	}

	err = poolManager.SetInstanceTags(ctx, pool, instance, r.Tags)
	if err != nil {
		defer cleanUpStageOwnerMappingFn()
		go cleanUpInstanceFn(false)
		return nil, fmt.Errorf("failed to add tags to the instance: %w", err)
	}

	client, err := factory.NewClient(instance, env.Runner.Name, instance.Port, env.LiteEngine.EnableMock, env.LiteEngine.MockStepTimeoutSecs)
	if err != nil {
		defer cleanUpStageOwnerMappingFn()
		go cleanUpInstanceFn(false)
		return nil, fmt.Errorf("failed to create LE client: %w", err)
	}

	// try the healthcheck api on the lite-engine until it responds ok
	logr.Traceln("running healthcheck and waiting for an ok response")
	if _, err = client.RetryHealth(ctx, healthCheckTimeout); err != nil {
		defer cleanUpStageOwnerMappingFn()
		go cleanUpInstanceFn(true)
		return nil, fmt.Errorf("failed to call lite-engine retry health: %w", err)
	}

	logr.Traceln("retry health check complete")

	// Currently m1 architecture does not enable nested virtualisation, so we disable docker.
	if instance.Platform.OS == oshelp.OSMac {
		b := false
		r.SetupRequest.MountDockerSocket = &b
	}

	_, err = client.Setup(ctx, &r.SetupRequest)
	if err != nil {
		defer cleanUpStageOwnerMappingFn()
		go cleanUpInstanceFn(true)
		return nil, fmt.Errorf("failed to call setup lite-engine: %w", err)
	}

	return instance, nil
}
