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
func (m *Manager) List(ctx context.Context, poolName string, queryParams *types.QueryParams) (busy, free, hibernating, provisioning, terminating []*types.Instance, err error) { //nolint:gocritic
	pool := m.poolMap[poolName]
	if pool == nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("manager: pool %s not found", poolName)
	}
	return m.list(ctx, pool, queryParams)
}

// list is an internal helper to list instances in a pool.
func (m *Manager) list(ctx context.Context, pool *poolEntry, queryParams *types.QueryParams) (busy, free, hibernating, provisioning, terminating []*types.Instance, err error) { //nolint:gocritic
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
		} else if instance.State == types.StateTerminating {
			terminating = append(terminating, loopInstance)
		} else if instance.State == types.StateCreated {
			free = append(free, loopInstance)
		}
	}

	return busy, free, hibernating, provisioning, terminating, nil
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
	caller := getCallerInfo(2) //nolint:mnd
	logr := logrus.WithFields(logrus.Fields{
		"pool":           poolName,
		"instance_id":    instanceID,
		"destroy_caller": caller,
	})

	logr.Infoln("destroy: initiating instance destroy")

	pool := m.poolMap[poolName]
	if pool == nil {
		logr.Errorln("destroy: pool not found")
		return fmt.Errorf("provision: pool name %q not found", poolName)
	}

	if instance == nil {
		logr.Infoln("destroy: instance not provided, fetching from store")
		instanceFromStore, err := m.Find(ctx, instanceID)
		if err != nil || instanceFromStore == nil {
			logr.WithError(err).Errorln("destroy: failed to find instance in store")
			return fmt.Errorf("provision: failed to find instance %q: %w", instanceID, err)
		}
		instance = instanceFromStore
	}

	logr = logr.WithFields(logrus.Fields{
		"instance_name":  instance.Name,
		"instance_state": string(instance.State),
		"instance_zone":  instance.Zone,
	})
	if storageCleanupType != nil {
		logr = logr.WithField("storage_cleanup_type", string(*storageCleanupType))
	}
	logr.Infoln("destroy: calling driver DestroyInstanceAndStorage")

	failedInstances, err := pool.Driver.DestroyInstanceAndStorage(ctx, []*types.Instance{instance}, storageCleanupType)
	if err != nil {
		logr.WithError(err).Errorln("destroy: driver DestroyInstanceAndStorage failed")
		return fmt.Errorf("provision: failed to destroy an instance of %q pool: %w", poolName, err)
	}

	// If this instance failed, it will be in failedInstances
	if len(failedInstances) > 0 {
		logr.Errorln("destroy: instance reported in failedInstances by driver")
		return fmt.Errorf("provision: instance %q failed to destroy", instance.ID)
	}

	if derr := m.Delete(ctx, instance.ID); derr != nil {
		logr.WithError(derr).Warnln("destroy: failed to delete instance from store after successful destroy")
	}
	logr.Infoln("destroy: instance destroyed successfully")

	// Best-effort async cleanup of egress firewall rules after instance is destroyed.
	go m.cleanupEgressFirewallRules(instance, pool, logr)

	return nil
}

// DestroyCapacity destroys a capacity reservation.
func (m *Manager) DestroyCapacity(ctx context.Context, reservedCapacity *types.CapacityReservation) error {
	caller := getCallerInfo(2) //nolint:mnd

	if reservedCapacity == nil || reservedCapacity.PoolName == "" {
		logrus.WithField("destroy_caller", caller).Infoln("destroy_capacity: skipping, reservation is nil or has no pool name")
		return nil
	}

	logr := logrus.WithFields(logrus.Fields{
		"pool":             reservedCapacity.PoolName,
		"stage_runtime_id": reservedCapacity.StageID,
		"reservation_id":   reservedCapacity.ReservationID,
		"instance_id":      reservedCapacity.InstanceID,
		"destroy_caller":   caller,
	})

	logr.Infoln("destroy_capacity: initiating capacity reservation destroy")

	pool, err := m.validatePool(reservedCapacity.PoolName)
	if err != nil {
		logr.Warnln("destroy_capacity: pool not found")
		return fmt.Errorf("provision: pool name %q not found", reservedCapacity.PoolName)
	}

	// Atomically mark the reservation as Terminating so concurrent destroys don't race
	// and so the purger can retry if the driver call fails below.
	m.claimCapacityForTermination(ctx, reservedCapacity, logr)

	// Destroy associated instance if exists
	if reservedCapacity.InstanceID != "" {
		logr.Infoln("destroy_capacity: destroying associated instance")
		if err := m.Destroy(ctx, reservedCapacity.PoolName, reservedCapacity.InstanceID, nil, nil); err != nil {
			logr.WithError(err).Warnln("destroy_capacity: failed to destroy associated instance")
		}
	}

	if reservedCapacity.ReservationID != "" {
		logr.Infoln("destroy_capacity: calling driver DestroyCapacity")
		// Destroy the actual capacity reservation
		if err := pool.Driver.DestroyCapacity(ctx, reservedCapacity); err != nil {
			logr.WithError(err).Warnln("destroy_capacity: driver DestroyCapacity failed")
			return err
		}
		logr.Infoln("destroy_capacity: driver DestroyCapacity succeeded")
	}
	// Delete the capacity reservation record
	logr.Infoln("destroy_capacity: deleting capacity reservation record")
	ctxLogr := logger.FromContext(ctx).
		WithField("pool", reservedCapacity.PoolName).
		WithField("runtimeId", reservedCapacity.StageID)
	m.deleteCapacityReservationRecord(ctx, reservedCapacity.StageID, ctxLogr)
	logr.Infoln("destroy_capacity: capacity reservation destroyed successfully")
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

// claimCapacityForTermination atomically transitions a capacity reservation from
// Created/InUse to Terminating. If the reservation is already Terminating (claimed
// by another caller or the purger) or missing, this is a no-op. The state update
// ensures the purger can retry cleanup if the driver-side destroy fails.
func (m *Manager) claimCapacityForTermination(ctx context.Context, reservedCapacity *types.CapacityReservation, logr *logrus.Entry) {
	if m.capacityReservationStore == nil || reservedCapacity.StageID == "" {
		return
	}
	claimed, err := m.capacityReservationStore.FindAndClaim(
		ctx,
		&types.CapacityReservationQueryParams{StageID: reservedCapacity.StageID, Limit: 1},
		types.CapacityReservationStateTerminating,
		[]types.CapacityReservationState{
			types.CapacityReservationStateCreated,
			types.CapacityReservationStateInUse,
		},
	)
	if err != nil {
		logr.WithError(err).Warnln("destroy_capacity: failed to mark capacity reservation as terminating")
		return
	}
	if len(claimed) == 0 {
		logr.Infoln("destroy_capacity: capacity reservation already terminating or missing, proceeding with destroy")
		return
	}
	reservedCapacity.ReservationState = types.CapacityReservationStateTerminating
	logr.Infoln("destroy_capacity: marked capacity reservation as terminating")
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

// ApplyEgressPolicy creates cloud-level egress firewall rules for the instance.
func (m *Manager) ApplyEgressPolicy(ctx context.Context, instance *types.Instance, resolvedIPs []string) ([]string, error) {
	pool := m.poolMap[instance.Pool]
	return pool.Driver.ApplyEgressPolicy(ctx, instance, resolvedIPs)
}

// cleanupEgressFirewallRules performs best-effort cleanup of egress firewall rules for a destroyed instance.
func (m *Manager) cleanupEgressFirewallRules(instance *types.Instance, pool *poolEntry, logr *logrus.Entry) {
	if m.firewallStore == nil || instance.Stage == "" {
		return
	}
	rules, listErr := m.firewallStore.ListByStageID(context.Background(), instance.Stage)
	if listErr != nil || len(rules) == 0 {
		return
	}
	ruleIDs := make([]string, len(rules))
	for i, r := range rules {
		ruleIDs[i] = r.ResourceID
	}
	if cleanupErr := pool.Driver.CleanupEgressPolicy(context.Background(), ruleIDs); cleanupErr != nil {
		logr.WithError(cleanupErr).Warnln("egress: failed to cleanup egress policy during destroy")
	}
	if delErr := m.firewallStore.DeleteByStageID(context.Background(), instance.Stage); delErr != nil {
		logr.WithError(delErr).Warnln("egress: failed to delete firewall rule records from DB")
	}
}

// PurgeOrphanedFirewallRules finds and cleans up stale firewall rules.
// Rules older than maxAge are purged — same threshold and logic as busy instances.
func (m *Manager) PurgeOrphanedFirewallRules(ctx context.Context, maxAge time.Duration) {
	if m.firewallStore == nil {
		return
	}

	createdBefore := time.Now().Add(-maxAge).Unix()
	staleRules, err := m.firewallStore.ListOlderThan(ctx, createdBefore)
	if err != nil || len(staleRules) == 0 {
		return
	}

	// Group rules by stage_id
	rulesByStage := map[string][]*types.FirewallRule{}
	for _, r := range staleRules {
		rulesByStage[r.StageID] = append(rulesByStage[r.StageID], r)
	}

	for stageID, rules := range rulesByStage {
		// Clean up cloud rules (best-effort, ignore 404 for non-existent rules)
		ruleIDs := make([]string, 0, len(rules))
		for _, r := range rules {
			if r.ResourceID != "" {
				ruleIDs = append(ruleIDs, r.ResourceID)
			}
		}
		if len(ruleIDs) > 0 {
			for _, pool := range m.poolMap {
				_ = pool.Driver.CleanupEgressPolicy(ctx, ruleIDs)
				break
			}
		}

		_ = m.firewallStore.DeleteByStageID(ctx, stageID)
		logrus.WithField("stage_id", stageID).WithField("rule_count", len(rules)).
			Infoln("purger: cleaned stale firewall rules")
	}
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
