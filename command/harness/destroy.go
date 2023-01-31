package harness

import (
	"context"
	"fmt"

	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	ierrors "github.com/drone-runners/drone-runner-aws/internal/types"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type VMCleanupRequest struct {
	PoolID         string `json:"pool_id"`
	StageRuntimeID string `json:"stage_runtime_id"`
}

func HandleDestroy(ctx context.Context, r *VMCleanupRequest, s store.StageOwnerStore, poolManager *drivers.Manager) error {
	if r.StageRuntimeID == "" {
		return ierrors.NewBadRequestError("mandatory field 'stage_runtime_id' in the request body is empty")
	}

	entity, err := s.Find(ctx, r.StageRuntimeID)
	if err != nil || entity == nil {
		return errors.Wrap(err, fmt.Sprintf("failed to find stage owner entity for stage: %s", r.StageRuntimeID))
	}
	poolID := entity.PoolName

	logr := logrus.
		WithField("stage_runtime_id", r.StageRuntimeID).
		WithField("pool", poolID).
		WithField("api", "dlite:destroy")

	logr.Traceln("starting the destroy process")

	inst, err := poolManager.GetInstanceByStageID(ctx, poolID, r.StageRuntimeID)
	if err != nil {
		return fmt.Errorf("cannot get the instance by tag: %w", err)
	}
	if inst == nil {
		return fmt.Errorf("instance with stage runtime ID %s not found", r.StageRuntimeID)
	}

	logr = logr.
		WithField("instance_id", inst.ID).
		WithField("instance_name", inst.Name)

	if err = poolManager.Destroy(ctx, poolID, inst.ID); err != nil {
		return fmt.Errorf("cannot destroy the instance: %w", err)
	}
	logr.Traceln("destroyed instance")

	envState().Delete(r.StageRuntimeID)

	if err = s.Delete(ctx, r.StageRuntimeID); err != nil {
		logr.WithError(err).Errorln("failed to delete stage owner entity")
	}

	return nil
}
