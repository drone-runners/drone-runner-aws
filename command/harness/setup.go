package harness

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/lehelper"
	errors "github.com/drone-runners/drone-runner-aws/internal/types"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"
	"github.com/harness/lite-engine/api"
	lespec "github.com/harness/lite-engine/engine/spec"
	"github.com/sirupsen/logrus"
)

type SetupVmRequest struct {
	ID               string            `json:"id"` // stage runtime ID
	PoolID           string            `json:"pool_id"`
	Tags             map[string]string `json:"tags"`
	CorrelationID    string            `json:"correlation_id"`
	LogKey           string            `json:"log_key"`
	api.SetupRequest `json:"setup_request"`
}

type SetupVmResponse struct {
	IPAddress  string `json:"ip_address"`
	InstanceID string `json:"instance_id"`
}

var (
	setupTimeout = 20 * time.Minute
)

func HandleSetup(ctx context.Context, r *SetupVmRequest, store store.StageOwnerStore, env config.EnvConfig, poolManager *drivers.Manager) (*SetupVmResponse, error) {
	id := r.ID
	pool := r.PoolID
	if id == "" {
		return nil, errors.NewBadRequestError("mandatory field 'id' in the request body is empty")
	}

	if pool == "" {
		return nil, errors.NewBadRequestError("mandatory field 'pool_id' in the request body is empty")
	}

	if err := store.Create(ctx, &types.StageOwner{StageID: id, PoolName: pool}); err != nil {
		return nil, fmt.Errorf("could not create stage owner entity: %w", err)
	}

	// Sets up logger to stream the logs in case log config is set
	log := logrus.New()
	var logr *logrus.Entry
	if r.SetupRequest.LogConfig.URL == "" {
		log.Out = os.Stdout
		logr = log.WithField("api", "dlite:setup").
			WithField("pool", pool).
			WithField("correlationID", r.CorrelationID)
	} else {
		wc := getStreamLogger(r.SetupRequest.LogConfig, r.LogKey, r.CorrelationID)
		defer func() {
			if err := wc.Close(); err != nil {
				logrus.WithError(err).Debugln("failed to close log stream")
			}
		}()

		log.Out = wc
		log.SetLevel(logrus.TraceLevel)
		logr = log.WithField("pool", r.PoolID)

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

	if !poolManager.Exists(pool) {
		return nil, fmt.Errorf("pool not defined")
	}

	instance, err := poolManager.Provision(ctx, pool, env.Runner.Name, env.Settings.LiteEnginePath)
	if err != nil {
		return nil, fmt.Errorf("failed provisioning")
	}

	if instance.IsHibernated {
		instance, err = poolManager.StartInstance(ctx, pool, instance.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to start the instance up")
		}
	}

	logr = logr.
		WithField("ip", instance.Address).
		WithField("id", instance.ID)

	// cleanUpFn is a function to terminate the instance if an error occurs later in the handleSetup function
	cleanUpFn := func() {
		errCleanUp := poolManager.Destroy(context.Background(), pool, instance.ID)
		if errCleanUp != nil {
			logr.WithError(errCleanUp).Errorln("failed to delete failed instance client")
		}
	}

	instance.Stage = id
	instance.Updated = time.Now().Unix()
	err = poolManager.Update(ctx, instance)
	if err != nil {
		go cleanUpFn()
		return nil, fmt.Errorf("failed to tag: %w", err)
	}

	client, err := lehelper.GetClient(instance, env.Runner.Name)
	if err != nil {
		go cleanUpFn()
		return nil, fmt.Errorf("failed to create LE client: %w", err)
	}

	// try the healthcheck api on the lite-engine until it responds ok
	logr.Traceln("running healthcheck and waiting for an ok response")
	if _, err = client.RetryHealth(ctx, setupTimeout); err != nil {
		go cleanUpFn()
		return nil, fmt.Errorf("failed to call lite-engine retry health: %w", err)
	}

	logr.Traceln("retry health check complete")

	setupResponse, err := client.Setup(ctx, &r.SetupRequest)
	if err != nil {
		go cleanUpFn()
		return nil, fmt.Errorf("failed to call setup lite-engine: %w", err)
	}

	logr.WithField("response", fmt.Sprintf("%+v", setupResponse)).Traceln("VM setup is complete")

	return &SetupVmResponse{InstanceID: instance.ID, IPAddress: instance.Address}, nil
}
