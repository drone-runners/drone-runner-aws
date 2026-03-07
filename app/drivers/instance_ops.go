package drivers

import (
	"context"
	"fmt"
	"time"

	"github.com/drone/runner-go/logger"
	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/types"
)

// Find finds an instance by ID.
func (m *Manager) Find(ctx context.Context, instanceID string) (*types.Instance, error) {
	return m.instanceStore.Find(ctx, instanceID)
}

// GetInstanceByStageID gets an instance by stage ID.
func (m *Manager) GetInstanceByStageID(ctx context.Context, poolName, stage string) (*types.Instance, error) {
	if stage == "" {
		logger.FromContext(ctx).
			Errorln("manager: GetInstanceByStageID stage runtime ID is not set")
		return nil, fmt.Errorf("stage runtime ID is not set")
	}

	pool := m.poolMap[poolName]
	if pool == nil {
		err := fmt.Errorf("GetInstanceByStageID: pool name %s not found", poolName)
		logger.FromContext(ctx).WithError(err).WithField("stage_runtime_id", stage).
			Errorln("manager: GetInstanceByStageID failed find pool")
		return nil, err
	}
	query := types.QueryParams{Status: types.StateInUse, Stage: stage}
	list, err := m.instanceStore.List(ctx, pool.Name, &query)
	if err != nil {
		logger.FromContext(ctx).WithError(err).WithField("stage_runtime_id", stage).
			Errorln("manager: GetInstanceByStageID failed to list instances")
		return nil, err
	}

	if len(list) == 0 {
		return nil, fmt.Errorf("manager: instance for stage runtime ID %s not found", stage)
	}
	return list[0], nil
}

// List lists instances in a pool by state.
func (m *Manager) List(ctx context.Context, poolName string, queryParams *types.QueryParams) (busy, free, hibernating, provisioning []*types.Instance, err error) {
	pool := m.poolMap[poolName]
	if pool == nil {
		return nil, nil, nil, nil, fmt.Errorf("manager: pool %s not found", poolName)
	}
	return m.list(ctx, pool, queryParams)
}

// list is an internal helper to list instances in a pool.
func (m *Manager) list(ctx context.Context, pool *poolEntry, queryParams *types.QueryParams) (busy, free, hibernating, provisioning []*types.Instance, err error) {
	list, err := m.instanceStore.List(ctx, pool.Name, queryParams)
	if err != nil {
		logger.FromContext(ctx).WithError(err).
			Errorln("manager: failed to list instances")
		return
	}

	for _, instance := range list {
		// required to append instance not pointer
		loopInstance := instance
		if instance.State == types.StateInUse {
			busy = append(busy, loopInstance)
		} else if instance.State == types.StateHibernating {
			hibernating = append(hibernating, loopInstance)
		} else if instance.State == types.StateProvisioning {
			provisioning = append(provisioning, loopInstance)
		} else {
			free = append(free, loopInstance)
		}
	}

	return busy, free, hibernating, provisioning, nil
}

// Delete deletes an instance from the store.
func (m *Manager) Delete(ctx context.Context, instanceID string) error {
	return m.instanceStore.Delete(ctx, instanceID)
}

// Update updates an instance in the store.
func (m *Manager) Update(ctx context.Context, instance *types.Instance) error {
	return m.instanceStore.Update(ctx, instance)
}

// Destroy destroys an instance in a pool.
func (m *Manager) Destroy(ctx context.Context, poolName, instanceID string, instance *types.Instance, storageCleanupType *storage.CleanupType) error {
	pool := m.poolMap[poolName]
	if pool == nil {
		return fmt.Errorf("provision: pool name %q not found", poolName)
	}

	if instance == nil {
		instanceFromStore, err := m.Find(ctx, instanceID)
		if err != nil || instanceFromStore == nil {
			return fmt.Errorf("provision: failed to find instance %q: %w", instanceID, err)
		}
		instance = instanceFromStore
	}

	failedInstances, err := pool.Driver.DestroyInstanceAndStorage(ctx, []*types.Instance{instance}, storageCleanupType)
	if err != nil {
		return fmt.Errorf("provision: failed to destroy an instance of %q pool: %w", poolName, err)
	}

	// If this instance failed, it will be in failedInstances
	if len(failedInstances) > 0 {
		return fmt.Errorf("provision: instance %q failed to destroy", instance.ID)
	}

	if derr := m.Delete(ctx, instance.ID); derr != nil {
		logrus.Warnf("failed to delete instance %s from store with err: %s", instance.ID, derr)
	}
	logrus.WithField("instance", instance.ID).Infof("instance destroyed")
	return nil
}

// DestroyCapacity destroys a capacity reservation.
func (m *Manager) DestroyCapacity(ctx context.Context, reservedCapacity *types.CapacityReservation) error {
	if reservedCapacity == nil || reservedCapacity.PoolName == "" {
		return nil
	}

	pool, err := m.validatePool(reservedCapacity.PoolName)
	if err != nil {
		logrus.Warnf("provision: pool name %q not found", reservedCapacity.PoolName)
		return fmt.Errorf("provision: pool name %q not found", reservedCapacity.PoolName)
	}

	logr := logger.FromContext(ctx).
		WithField("pool", reservedCapacity.PoolName).
		WithField("runtimeId", reservedCapacity.StageID)

	// Destroy associated instance if exists
	if reservedCapacity.InstanceID != "" {
		if err := m.Destroy(ctx, reservedCapacity.PoolName, reservedCapacity.InstanceID, nil, nil); err != nil {
			logrus.Warnf("failed to destroy instance %s from store with err: %s", reservedCapacity.InstanceID, err)
		}
	}

	if reservedCapacity.ReservationID != "" {
		// Destroy the actual capacity reservation
		if err := pool.Driver.DestroyCapacity(ctx, reservedCapacity); err != nil {
			logr.Warnln("provision: failed to destroy reserved capacity")
			return err
		}
	}
	// Delete the capacity reservation record
	m.deleteCapacityReservationRecord(ctx, reservedCapacity.StageID, logr)
	return nil
}

// deleteCapacityReservationRecord deletes a capacity reservation record.
func (m *Manager) deleteCapacityReservationRecord(ctx context.Context, stageID string, logr logger.Logger) {
	if m.capacityReservationStore == nil {
		return
	}
	if err := m.capacityReservationStore.Delete(ctx, stageID); err != nil {
		logr.Warnln("failed to delete capacity reservation entity")
	}
}

// StartInstance starts a hibernated instance.
func (m *Manager) StartInstance(ctx context.Context, poolName, instanceID string, instanceInfo *common.InstanceInfo) (*types.Instance, error) {
	pool := m.poolMap[poolName]
	if pool == nil {
		return nil, fmt.Errorf("start_instance: pool name %q not found", poolName)
	}

	var inst *types.Instance
	var err error
	if instanceInfo.ID != "" {
		if err = common.ValidateStruct(*instanceInfo); err != nil {
			logrus.Warnf("missing information in the instance info: %v", err)
		} else {
			inst = common.BuildInstanceFromRequest(*instanceInfo)
			inst.IsHibernated = true
			logrus.WithField("instanceID", instanceID).Traceln("found instance in request")
		}
	}

	if inst == nil {
		inst, err = m.Find(ctx, instanceID)
		if err != nil {
			return nil, fmt.Errorf("start_instance: failed to find the instance in db %s of %q pool: %w", instanceID, poolName, err)
		}
		logrus.WithField("instanceID", instanceID).Traceln("found instance in DB")
	}

	if !inst.IsHibernated {
		return inst, nil
	}

	logrus.WithField("instanceID", instanceID).Infoln("Starting vm from hibernate state")
	ipAddress, err := pool.Driver.Start(ctx, inst, poolName)
	if err != nil {
		return nil, fmt.Errorf("start_instance: failed to start the instance %s of %q pool: %w", instanceID, poolName, err)
	}

	inst.IsHibernated = false
	inst.Address = ipAddress
	if err := m.instanceStore.Update(ctx, inst); err != nil {
		return nil, fmt.Errorf("start_instance: failed to update instance store %s of %q pool: %w", instanceID, poolName, err)
	}
	return inst, nil
}

// Suspend suspends an instance.
func (m *Manager) Suspend(ctx context.Context, poolName string, instance *types.Instance) error {
	pool := m.poolMap[poolName]
	if pool == nil {
		return fmt.Errorf("suspend: pool name %q not found", poolName)
	}

	// hibernateOrStopWithRetries assumes that the instance is present in the store
	// and works only if the state is not InUse.
	var err error
	instance, err = m.findOrCreateInstance(ctx, pool, instance)
	if err != nil {
		return fmt.Errorf("suspend failed to find or create instance: %w", err)
	}

	if err := m.hibernateOrStopWithRetries(
		ctx,
		poolName,
		instance,
		true,
	); err != nil {
		return fmt.Errorf("suspend: failed to suspend an instance %s of %q pool: %w", instance.ID, poolName, err)
	}

	return nil
}

// SetInstanceTags sets tags on an instance.
func (m *Manager) SetInstanceTags(ctx context.Context, poolName string, instance *types.Instance,
	tags map[string]string) error {
	pool := m.poolMap[poolName]
	if pool == nil {
		return fmt.Errorf("provision: pool name %q not found", poolName)
	}

	if len(tags) == 0 {
		return nil
	}

	if err := pool.Driver.SetTags(ctx, instance, tags); err != nil {
		return fmt.Errorf("provision: failed to label an instance of %q pool: %w", poolName, err)
	}
	return nil
}

// InstanceLogs returns logs for an instance.
func (m *Manager) InstanceLogs(ctx context.Context, poolName, instanceID string) (string, error) {
	pool := m.poolMap[poolName]
	if pool == nil {
		return "", fmt.Errorf("instance_logs: pool name %q not found", poolName)
	}

	return pool.Driver.Logs(ctx, instanceID)
}

// PingDriver pings the driver to check connectivity.
func (m *Manager) PingDriver(ctx context.Context) error {
	for _, pool := range m.poolMap {
		err := pool.Driver.Ping(ctx)
		if err != nil {
			return err
		}

		const pauseBetweenChecks = 500 * time.Millisecond
		time.Sleep(pauseBetweenChecks)
	}

	return nil
}
