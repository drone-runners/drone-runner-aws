package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	ierrors "github.com/drone-runners/drone-runner-aws/app/types"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"
	lespec "github.com/harness/lite-engine/engine/spec"
	"github.com/sirupsen/logrus"
)

type CapacityReservationRequest struct {
	ID                     string               `json:"id"` // stage runtime ID
	PoolID                 string               `json:"pool_id"`
	FallbackPoolIDs        []string             `json:"fallback_pool_ids"`
	Tags                   map[string]string    `json:"tags"`
	Context                Context              `json:"context,omitempty"`
	ResourceClass          string               `json:"resource_class"`
	RequestedVMImageConfig lespec.VMImageConfig `json:"vm_image_config"`
	StorageConfig          types.StorageConfig  `json:"storage_config"`
	Zone                   string               `json:"zone"`
	MachineType            string               `json:"machine_type"`
	ReservationTimeout     int64                `json:"timeout,omitempty"`
}

// HandleCapacityReservation tries to reserve capacity for a future vm init an in any of the pools given in the
// setup request. It calls handleCapacityReservation internally for each pool instance trying to complete a
// capacity reservation.
func HandleCapacityReservation(
	ctx context.Context,
	r *CapacityReservationRequest,
	crs store.CapacityReservationStore,
	poolMapByAccount map[string]map[string]string,
	runnerName string,
	poolManager drivers.IManager,
) (*types.CapacityReservation, error) {
	capacityStartTime := time.Now()
	stageRuntimeID := r.ID
	if stageRuntimeID == "" {
		return nil, ierrors.NewBadRequestError("mandatory field 'id' in the request body is empty")
	}

	if r.PoolID == "" {
		return nil, ierrors.NewBadRequestError("mandatory field 'pool_id' in the request body is empty")
	}

	// Sets up logger to stream the logs in case log config is set
	internalLog := logrus.New()
	internalLog.SetFormatter(&logrus.JSONFormatter{})

	var internalLogr *logrus.Entry
	internalLog.SetLevel(logrus.TraceLevel)
	internalLog.Out = os.Stdout
	internalLogr = internalLog.WithField("stage_runtime_id", stageRuntimeID)

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
		pool := fetchPool(r.Context.AccountID, p, poolMapByAccount)
		internalLogr.WithField("pool_id", pool).Traceln("starting the capacity reservation process")
		capacity, _, poolErr = handleCapacityReservation(ctx, internalLogr, r, runnerName, poolManager, pool, owner)
		if poolErr != nil {
			internalLogr.WithField("pool_id", pool).WithError(poolErr).Errorln("could not reserve capacity")
			if errors.As(poolErr, &capacityUnavailableErr) {
				capacityUnavailable = true
			}
			continue
		}
		selectedPool = pool
		foundPool = true
		break
	}

	// If a successful fallback happened and we have an instance setup, record it
	if foundPool && capacity != nil { // check for instance != nil just in case
		// add an entry in stage pool mapping if instance was created.
		_, findErr := crs.Find(noContext, stageRuntimeID)
		if findErr != nil {
			capacity.StageID = stageRuntimeID
			capacity.ReservationState = types.CapacityReservationStateAvailable
			if cerr := crs.Create(noContext, capacity); cerr != nil {
				if derr := poolManager.DestroyCapacity(noContext, capacity); derr != nil {
					internalLogr.WithError(derr).Errorln("failed to cleanup capacity reservation on failure")
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

	internalLogr.WithField("selected_pool", selectedPool).
		WithField("tried_pools", pools).
		Traceln("VM capacity reservation is complete")

	internalLogr.WithField("stage_runtime_id", stageRuntimeID).
		Traceln("Capacity reservation step completed successfully")

	totalCapacityTime := time.Since(capacityStartTime)
	internalLogr.
		WithField("selected_pool", selectedPool).
		WithField("requested_pool", r.PoolID).
		Tracef("total capacity reservation time for vm setup is %.2fs", totalCapacityTime.Seconds())

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
	query := &types.QueryParams{
		RunnerName: runnerName,
	}

	timeout := time.Duration(r.ReservationTimeout) * time.Millisecond
	timeoutSeconds := int64(timeout / time.Second)

	_, capacityReservation, warmed, err = poolManager.Provision(
		ctx,
		pool,
		poolManager.GetTLSServerName(),
		owner,
		r.ResourceClass,
		&r.RequestedVMImageConfig,
		query,
		nil,
		&r.StorageConfig,
		r.Zone,
		r.MachineType,
		false,
		nil,
		timeoutSeconds,
		false,
		nil,
		true,
	)
	if err != nil {
		return nil, false, err
	}

	logr.Traceln("successfully reserved capacity for VM in pool")

	return capacityReservation, warmed, nil
}
