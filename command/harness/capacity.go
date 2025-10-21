package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	ierrors "github.com/drone-runners/drone-runner-aws/app/types"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"
	"github.com/harness/lite-engine/api"
	lespec "github.com/harness/lite-engine/engine/spec"
	"github.com/sirupsen/logrus"
)

type CapacityReservationRequest struct {
	ID              string               `json:"id"` // stage runtime ID
	PoolID          string               `json:"pool_id"`
	FallbackPoolIDs []string             `json:"fallback_pool_ids"`
	Tags            map[string]string    `json:"tags"`
	CorrelationID   string               `json:"correlation_id"`
	LogKey          string               `json:"log_key"`
	Context         Context              `json:"context,omitempty"`
	ResourceClass   string               `json:"resource_class"`
	VMImageConfig   lespec.VMImageConfig `json:"vm_image_config"`
	LogConfig       api.LogConfig        `json:"log_config"`
	MtlsConfig      lespec.MtlsConfig    `json:"mtls_config"`
	StorageConfig   types.StorageConfig  `json:"storage_config"`
	Zone            string               `json:"zone"`
	MachineType     string               `json:"machine_type"`
	Timeout         int64                `json:"timeout,omitempty"`
	Envs            map[string]string    `json:"envs,omitempty"`
}

// HandleCapacityReservation tries to reserve capacity for a future vm init an in any of the pools given in the
// setup request. It calls handleCapacityReservation internally for each pool instance trying to complete a
// capacity reservation.
func HandleCapacityReservation(
	ctx context.Context,
	r *CapacityReservationRequest,
	crs store.CapacityReservationStore,
	globalVolumes []string,
	poolMapByAccount map[string]map[string]string,
	runnerName string,
	poolManager drivers.IManager,
	metrics *metric.Metrics,
) (*types.CapacityReservation, error) {
	stageRuntimeID := r.ID

	if stageRuntimeID == "" {
		return nil, ierrors.NewBadRequestError("mandatory field 'id' in the request body is empty")
	}

	if r.PoolID == "" {
		return nil, ierrors.NewBadRequestError("mandatory field 'pool_id' in the request body is empty")
	}

	// Sets up logger to stream the logs in case log config is set
	log := logrus.New()
	internalLog := logrus.New()
	internalLog.SetFormatter(&logrus.JSONFormatter{})

	var (
		logr         *logrus.Entry
		internalLogr *logrus.Entry
	)
	if r.LogConfig.URL == "" {
		log.Out = os.Stdout
		logr = log.WithField("api", "dlite:setup").WithField("correlationID", r.CorrelationID)
	} else {
		wc := getStreamLogger(r.LogConfig, r.MtlsConfig, r.LogKey, r.CorrelationID)
		defer func() {
			if err := wc.Close(); err != nil {
				log.WithError(err).Debugln("failed to close log stream")
			}
		}()

		log.Out = wc
		log.SetLevel(logrus.TraceLevel)
		internalLog.SetLevel(logrus.TraceLevel)
		internalLog.Out = os.Stdout
		logr = log.WithField("stage_runtime_id", stageRuntimeID)
		internalLogr = internalLog.WithField("stage_runtime_id", stageRuntimeID)
	}

	logr = AddContext(logr, &r.Context, r.Tags)
	internalLogr = AddContext(internalLogr, &r.Context, r.Tags)
	ctx = logger.WithContext(ctx, logger.Logrus(internalLogr))

	pools := []string{}
	pools = append(pools, r.PoolID)
	pools = append(pools, r.FallbackPoolIDs...)

	var selectedPool string
	var poolErr error
	var capacity *types.CapacityReservation

	var (
		foundPool bool
	)

	var owner string

	capacityUnavailable := false
	var capacityUnavailableErr *ierrors.ErrCapacityUnavailable

	// TODO: Remove this once we start populating license information.
	if strings.Contains(r.PoolID, freeAccount) || getIsFreeAccount(&r.Context, r.Tags) {
		owner = freeAccount
	} else {
		owner = GetAccountID(&r.Context, r.Tags)
	}

	for _, p := range pools {
		pool := fetchPool(r.LogConfig.AccountID, p, poolMapByAccount)
		logr.WithField("pool_id", pool).Traceln("starting the capacity reservation process")
		capacity, _, poolErr = handleCapacityReservation(ctx, logr, r, runnerName, poolManager, pool, owner)
		if poolErr != nil {
			logr.WithField("pool_id", pool).WithError(poolErr).Errorln("could not reserve capacity")
			if !errors.As(poolErr, &capacityUnavailableErr) {
				capacityUnavailable = true
			}
			continue
		}
		selectedPool = pool
		foundPool = true
		_, _, _ = poolManager.Inspect(selectedPool)
		break
	}

	// If a successful fallback happened and we have an instance setup, record it
	if foundPool && capacity != nil { // check for instance != nil just in case
		// add an entry in stage pool mapping if instance was created.
		_, findErr := crs.Find(noContext, stageRuntimeID)
		if findErr != nil {
			capacity.StageID = stageRuntimeID
			if cerr := crs.Create(noContext, capacity); cerr != nil {
				if capacity.InstanceID != "" {
					if derr := poolManager.Destroy(noContext, selectedPool, capacity.InstanceID, nil, nil, capacity); derr != nil {
						logr.WithError(derr).Errorln("failed to cleanup instance on setup failure")
					}
				}
				return nil, fmt.Errorf("could not create capacity reservation entity: %w", cerr)
			}
		}
	} else {
		internalLogr.WithField("stage_runtime_id", stageRuntimeID).
			Errorln("Capacity Reservation failed")
		// If atleast one pool failed only with capacity unavailable then its not an actual failure
		if capacityUnavailable {
			return nil, nil
		}
		return nil, fmt.Errorf("could not reserve capacity for a VM from the pool: %w", poolErr)
	}

	logr.WithField("selected_pool", selectedPool).
		WithField("tried_pools", pools).
		Traceln("VM capacity reservation is complete")

	internalLogr.WithField("stage_runtime_id", stageRuntimeID).
		Traceln("Capacity reservation step completed successfully")

	return capacity, nil
}

// handleCapacityReservation tries to reserve capacity for a future vm init an instance in a given pool.
func handleCapacityReservation(
	ctx context.Context,
	logr *logrus.Entry,
	r *CapacityReservationRequest,
	runnerName string,
	poolManager drivers.IManager,
	pool,
	owner string,
) (
	capacityReservation *types.CapacityReservation,
	warmed bool,
	err error,
) {
	// check if the pool exists in the pool manager.
	if !poolManager.Exists(pool) {
		return nil, false, fmt.Errorf("could not find pool: %s", pool)
	}

	// try to provision an instance from the pool manager.
	// try to provision an instance from the pool manager.
	query := &types.QueryParams{
		RunnerName: runnerName,
	}

	shouldUseGoogleDNS := false
	if len(r.Envs) != 0 {
		if r.Envs["CI_HOSTED_USE_GOOGLE_DNS"] == "true" {
			shouldUseGoogleDNS = true
		}
	}

	capacityReservation, warmed, err = poolManager.ReserveCapacity(
		ctx,
		pool,
		poolManager.GetTLSServerName(),
		owner,
		r.ResourceClass,
		&r.VMImageConfig,
		query,
		&r.StorageConfig,
		r.Zone,
		r.MachineType,
		shouldUseGoogleDNS,
		r.Timeout,
	)
	if err != nil {
		return nil, false, err
	}

	logr.Traceln("successfully reserved capacity for VM in pool")

	return capacityReservation, warmed, nil
}
