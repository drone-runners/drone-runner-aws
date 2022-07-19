package harness

import (
	"context"
	"fmt"

	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	errors "github.com/drone-runners/drone-runner-aws/internal/types"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/sirupsen/logrus"
)

type VMCleanupRequest struct {
	PoolID         string `json:"pool_id"`
	StageRuntimeID string `json:"stage_runtime_id"`
}

func HandleDestroy(ctx context.Context, r *VMCleanupRequest, s store.StageOwnerStore, poolManager *drivers.Manager) error {
	if r.PoolID == "" {
		return errors.NewBadRequestError("mandatory field 'pool_id' in the request body is empty")
	}

	if r.StageRuntimeID == "" {
		return errors.NewBadRequestError("mandatory field 'stage_runtime_id' in the request body is empty")
	}

	inst, err := poolManager.GetInstanceByStageID(ctx, r.PoolID, r.StageRuntimeID)
	if err != nil {
		return fmt.Errorf("cannot get the instance by tag: %w", err)
	}
	if inst == nil {
		return fmt.Errorf("instance with provided ID not found")
	}

	logr := logrus.
		WithField("instance_id", inst.ID).
		WithField("api", "dlite:destroy").
		WithField("stage_runtime_id", r.StageRuntimeID).
		WithField("pool", r.PoolID)

	if err = poolManager.Destroy(ctx, r.PoolID, inst.ID); err != nil {
		return fmt.Errorf("cannot destroy the instance: %w", err)
	}
	logr.Traceln("destroyed instance")
	exists, _ := s.Find(ctx, r.StageRuntimeID, r.PoolID)
	if exists != nil {
		if err = s.Delete(ctx, r.StageRuntimeID); err != nil {
			logr.WithError(err).Errorln("failed to delete stage owner entity")
		}
	}

	return nil
}
