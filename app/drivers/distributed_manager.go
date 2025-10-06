package drivers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/Masterminds/squirrel"

	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"
	"github.com/harness/lite-engine/engine/spec"

	"github.com/sirupsen/logrus"
)

var _ IManager = (*DistributedManager)(nil)

type DistributedManager struct {
	Manager
}

func NewDistributedManager(manager *Manager) *DistributedManager {
	return &DistributedManager{
		*manager,
	}
}


// Provision returns an instance for a job execution and tags it as in use.
// This method and BuildPool method contain logic for maintaining pool size.
func (d *DistributedManager) Provision(
	ctx context.Context,
	poolName,
	serverName,
	ownerID,
	resourceClass string,
	vmImageConfig *spec.VMImageConfig,
	query *types.QueryParams,
	gitspaceAgentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	zone string,
	machineType string,
	shouldUseGoogleDNS bool,
	instanceInfo *common.InstanceInfo,
	timeout int64,
	isMarkedForInfraReset bool,
) (*types.Instance, bool, error) {
	pool, err := d.validatePool(poolName)
	if err != nil {
		return nil, false, err
	}
	return d.provisionFromPool(
		ctx,
		pool,
		serverName,
		ownerID,
		resourceClass,
		vmImageConfig,
		gitspaceAgentConfig,
		storageConfig,
		zone,
		machineType,
		shouldUseGoogleDNS,
		timeout,
		poolName,
	)

// GetPoolConfig returns the original pool configuration instance for the given pool name.
func (d *DistributedManager) GetPoolConfig(poolName string) (*config.Instance, error) {
	return d.Manager.GetPoolConfig(poolName)
}

func (d *DistributedManager) BuildPools(ctx context.Context) error {
	query := types.QueryParams{RunnerName: d.runnerName}
	buildPoolWrapper := func(ctx context.Context, pool *poolEntry, serverName string, query *types.QueryParams) error {
		return d.buildPool(ctx, pool, serverName, query, d.setupInstanceWithHibernate)
	}
	return d.forEach(ctx, d.GetTLSServerName(), &query, buildPoolWrapper)
}

// This helps in cleaning the pools
func (d *DistributedManager) CleanPools(ctx context.Context, destroyBusy, destroyFree bool) error {
	var returnError error
	query := types.QueryParams{RunnerName: d.runnerName, MatchLabels: map[string]string{"retain": "false"}}
	for _, pool := range d.poolMap {
		err := d.cleanPool(ctx, pool, &query, destroyBusy, destroyFree)
		if err != nil {
			returnError = err
			logrus.Errorf("failed to clean pool %s with error: %s", pool.Name, err)
		}
	}

	return returnError
}

func (d *DistributedManager) cleanPool(ctx context.Context, pool *poolEntry, query *types.QueryParams, destroyBusy, destroyFree bool) error {
	if !destroyBusy && !destroyFree {
		return fmt.Errorf("distributed dlite: both destroyBusy and destroyFree cannot be false")
	}

	// Determine which states are allowed for cleanup
	var allowedStates []types.InstanceState
	if destroyBusy {
		allowedStates = append(allowedStates, types.StateInUse)
	}
	if destroyFree {
		allowedStates = append(allowedStates, types.StateCreated, types.StateHibernating)
	}

	// Set the pool name on the query parameters
	query.PoolName = pool.Name

	// 1. Find and claim all matching instances
	var instancesToDestroy []*types.Instance
	for {
		// Claim one instance at a time to avoid long transactions and to process them sequentially.
		instance, err := d.instanceStore.FindAndClaim(ctx, query, types.StateTerminating, allowedStates)
		if err != nil {
			// If no rows are found, it means we have claimed all available instances.
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			return fmt.Errorf("failed to claim instance for cleanup: %w", err)
		}
		if instance == nil {
			break // No more instances to claim
		}
		instancesToDestroy = append(instancesToDestroy, instance)
	}

	// If no instances were claimed, there's nothing to do.
	if len(instancesToDestroy) == 0 {
		return nil
	}

	// 2. Destroy the claimed instances
	logrus.WithField("pool", pool.Name).
		WithField("instance_count", len(instancesToDestroy)).
		WithField("instances", instancesToDestroy).
		Infoln("cleaning up instances")
	err := pool.Driver.Destroy(ctx, instancesToDestroy)
	if err != nil {
		// If destroy fails, we don't proceed to delete them from the DB.
		// The instances will remain in 'terminating' state for a later retry.
		return fmt.Errorf("failed to destroy instances in pool %q: %w", pool.Name, err)
	}

	// 3. Delete the instances from the database after they are destroyed
	instanceIDs := make([]string, len(instancesToDestroy))
	for i, instance := range instancesToDestroy {
		instanceIDs[i] = instance.ID
	}

	builder := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)
	deleteSQL, args, err := builder.Delete("instances").Where(squirrel.Eq{"instance_id": instanceIDs}).ToSql()
	if err != nil {
		return fmt.Errorf("failed to build delete query for cleaned instances: %w", err)
	}

	_, err = d.instanceStore.DeleteAndReturn(ctx, deleteSQL, args...)
	if err != nil {
		return fmt.Errorf("failed to delete destroyed instances from database: %w", err)
	}

	return nil
}

func (d *DistributedManager) GetInstanceStore() store.InstanceStore {
	return d.instanceStore
}

func (d *DistributedManager) GetStageOwnerStore() store.StageOwnerStore {
	return d.stageOwnerStore
}

func (d *DistributedManager) GetTLSServerName() string {
	// keep server name constant since any runner should be able to send request to LE
	return "distributed-dlite"
}

func (d *DistributedManager) IsDistributed() bool {
	return true
}

// provisionFromPool overrides the Manager's provisionFromPool method to use FindAndClaim for distributed coordination
func (d *DistributedManager) provisionFromPool(
	ctx context.Context,
	pool *poolEntry,
	tlsServerName, ownerID, resourceClass string,
	vmImageConfig *spec.VMImageConfig,
	agentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	zone, machineType string,
	shouldUseGoogleDNS bool,
	timeout int64,
	poolName string,
) (*types.Instance, bool, error) {
	// For distributed manager, first try to claim an existing free instance
	allowedStates := []types.InstanceState{types.StateCreated}

	// Try to find and claim a free instance atomically
	inst, err := d.instanceStore.FindAndClaim(ctx, &types.QueryParams{PoolName: poolName}, types.StateInUse, allowedStates)
	if err != nil && err != sql.ErrNoRows {
		return nil, false, fmt.Errorf("provision: failed to find and claim instance in %q pool: %w", poolName, err)
	}

	// If we successfully claimed an instance, update it and return
	if inst != nil {
		inst.OwnerID = ownerID
		if inst.IsHibernated {
			// update started time after bringing instance from hibernate
			// this will make sure that purger only picks it when it is actually used for max age
			inst.Started = time.Now().Unix()
		}
		if err = d.instanceStore.Update(ctx, inst); err != nil {
			return nil, false, fmt.Errorf("provision: failed to tag an instance in %q pool: %w", poolName, err)
		}
		logger.FromContext(ctx).
			WithField("pool", poolName).
			WithField("instance_id", inst.ID).
			WithField("hotpool", true).
			Traceln("provision: claimed hotpool instance")

		// TODO: change this to an outbox entry
		d.setupInstanceAsync(pool, tlsServerName, "", "", nil, nil, nil, "", "", false, timeout)
		return inst, true, nil
	}

	// No free instances available, create a new one
	// In distributed mode, we don't check pool capacity limits since:
	// 1. Pool MaxSize is typically per-runner, but we'd be checking against global counts
	// 2. FindAndClaim already provides natural backpressure through database constraints
	// 3. Infrastructure limits (cloud quotas, etc.) will provide the real boundaries
	logger.FromContext(ctx).
		WithField("pool", poolName).
		WithField("hotpool", false).
		Traceln("provision: no hotpool instances available, creating new instance")
	// Create a new instance
	inst, err = d.setupInstance(ctx, pool, tlsServerName, ownerID, resourceClass, vmImageConfig, true, agentConfig, storageConfig, zone, machineType, shouldUseGoogleDNS, timeout, nil)
	if err != nil {
		return nil, false, fmt.Errorf("provision: failed to create instance: %w", err)
	}
	return inst, false, nil
}

// setupInstanceAsync handles setting up the instance asynchronously
func (d *DistributedManager) setupInstanceAsync(
	pool *poolEntry,
	tlsServerName, ownerID, resourceClass string,
	vmImageConfig *spec.VMImageConfig,
	agentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	zone, machineType string,
	shouldUseGoogleDNS bool,
	timeout int64,
) {
	go func(ctx context.Context) {
		_, err := d.setupInstanceWithHibernate(ctx, pool, tlsServerName, ownerID, resourceClass, vmImageConfig, agentConfig, storageConfig, zone, machineType, shouldUseGoogleDNS, timeout, nil)
		if err != nil {
			logrus.WithError(err).Errorln("failed to setup instance asynchronously")
		}
	}(d.globalCtx)
}

// setupInstanceWithHibernate handles setting up the instance into hibernate mode
func (d *DistributedManager) setupInstanceWithHibernate(
	ctx context.Context,
	pool *poolEntry,
	tlsServerName, ownerID, resourceClass string,
	vmImageConfig *spec.VMImageConfig,
	agentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	zone, machineType string,
	shouldUseGoogleDNS bool,
	timeout int64,
	platform *types.Platform,
) (*types.Instance, error) {
	inst, err := d.setupInstance(ctx, pool, tlsServerName, ownerID, resourceClass, vmImageConfig, false, agentConfig, storageConfig, zone, machineType, shouldUseGoogleDNS, timeout, platform)
	if err != nil {
		return nil, err
	}
	go func() {
		err = d.hibernate(context.Background(), pool.Name, tlsServerName, inst)
		if err != nil {
			logrus.WithError(err).Errorln("failed to hibernate the vm")
		}
	}()
	return inst, nil
}

// hibernate handles hibernation for distributed manager using FindAndClaim
func (d *DistributedManager) hibernate(
	ctx context.Context,
	poolName, tlsServerName string,
	instance *types.Instance,
) error {
	pool := d.poolMap[poolName]
	if pool == nil {
		return fmt.Errorf("hibernate: pool name %q not found", poolName)
	}

	if !pool.Driver.CanHibernate() {
		return nil
	}

	// Check connectivity before attempting hibernation
	if !d.waitForInstanceConnectivity(ctx, tlsServerName, instance.ID) {
		return fmt.Errorf("hibernate: connectivity check deadline exceeded")
	}

	// Use FindAndClaim to atomically set the instance state to hibernating
	queryParams := &types.QueryParams{
		PoolName:   poolName,
		InstanceID: instance.ID,
	}
	allowedStates := []types.InstanceState{types.StateCreated}
	claimedInstance, err := d.instanceStore.FindAndClaim(ctx, queryParams, types.StateHibernating, allowedStates)
	if err != nil {
		return fmt.Errorf("hibernate: failed to claim instance for hibernation %s of %q pool: %w", claimedInstance.ID, poolName, err)
	}

	// Perform the actual hibernation using the driver with retries
	logrus.WithField("instanceID", claimedInstance.ID).Infoln("Hibernating vm")

	const maxRetries = 3
	const baseDelay = 30 * time.Second // Start with 30 seconds as AWS suggests "a few minutes"

	var hibernateErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		hibernateErr = pool.Driver.Hibernate(ctx, claimedInstance.ID, poolName, claimedInstance.Zone)
		if hibernateErr == nil {
			break // Success, exit retry loop
		}

		// Check if this is a retryable hibernation error
		if attempt < maxRetries {
			delay := time.Duration(attempt) * baseDelay // Linear backoff: 30s, 60s, 90s
			logrus.WithError(hibernateErr).
				WithField("instanceID", claimedInstance.ID).
				WithField("attempt", attempt).
				WithField("retryAfter", delay).
				Warnln("hibernation not ready, retrying after delay")
			time.Sleep(delay)
			continue
		}

		// For max retries reached, break immediately
		break
	}

	if hibernateErr != nil {
		// Revert state back to created if hibernation fails
		claimedInstance.State = types.StateCreated
		if updateErr := d.instanceStore.Update(ctx, claimedInstance); updateErr != nil {
			return fmt.Errorf("hibernate: update state: failed to update instance in db %s of %q pool: %w", claimedInstance.ID, poolName, updateErr)
		}
		return fmt.Errorf("hibernate: failed to hibernate instance %s of %q pool after %d attempts: %w", claimedInstance.ID, poolName, maxRetries, hibernateErr)
	}

	// Update the instance to mark it as hibernated and set state back to created
	claimedInstance.IsHibernated = true
	claimedInstance.State = types.StateCreated
	if err = d.instanceStore.Update(ctx, claimedInstance); err != nil {
		return fmt.Errorf("hibernate: failed to update hibernated instance %s of %q pool: %w", claimedInstance.ID, poolName, err)
	}

	logrus.WithField("instanceID", claimedInstance.ID).Infoln("hibernate complete")
	return nil
}

// Instance purger for distributed dlite
// Delete all instances irrespective of runner name
func (d *DistributedManager) StartInstancePurger(ctx context.Context, maxAgeBusy, maxAgeFree, purgerTime time.Duration) error {
	const minMaxAge = 5 * time.Minute
	if maxAgeBusy < minMaxAge || maxAgeFree < minMaxAge {
		return fmt.Errorf("distributed dlite: minimum value of max age is %.2f minutes", minMaxAge.Minutes())
	}
	if maxAgeBusy > maxAgeFree {
		return fmt.Errorf(
			"distributed dlite: max age of used instances (set to %.2fmin) should be less than max age of free instances (set to %.2fmin)",
			maxAgeBusy.Minutes(), maxAgeFree.Minutes())
	}

	if d.cleanupTimer != nil {
		panic("distributed dlite: purger already started")
	}

	d.cleanupTimer = time.NewTicker(purgerTime)

	logrus.Infof("distributed dlite: Instance purger started. It will run every %.2f minutes", purgerTime.Minutes())

	go func() {
		defer d.cleanupTimer.Stop()
		for {
			func() {
				defer func() {
					if r := recover(); r != nil {
						logrus.Errorf("distributed dlite: PANIC %v\n%s", r, debug.Stack())
					}
				}()

				select {
				case <-ctx.Done():
					return
				case <-d.cleanupTimer.C:
					logrus.Traceln("distributed dlite: Launching instance purger")

					queryParams := types.QueryParams{MatchLabels: map[string]string{"retain": "false"}}
					// All instances are labeled with retain: true/false
					// If retain is true, instance is not cleaned up while we clean the pools or run the instance purger
					// These instances are only cleaned up when there's a cleanup request from client explicitly.
					// This is the case for VMs created for CDE
					// If retain is false, the instance is cleaned up as earlier. This is the case for CI VMs
					// MatchLabels in the query params are used in a generic manner to match it against the labels stored in the instance
					// This is similar to how K8s matchLabels and labels work.
					for _, pool := range d.poolMap {
						if err := d.startInstancePurger(ctx, pool, maxAgeBusy, &queryParams); err != nil {
							logger.FromContext(ctx).WithError(err).
								Errorln("distributed dlite: purger: Failed to purge stale instances")
						}
					}
				}
			}()
		}
	}()

	return nil
}

func (d *DistributedManager) startInstancePurger(ctx context.Context, pool *poolEntry, maxAgeBusy time.Duration, queryParams *types.QueryParams) error {
	logr := logger.FromContext(ctx).
		WithField("driver", pool.Driver.DriverName()).
		WithField("pool", pool.Name)

	conditions := squirrel.Or{}
	currentTime := time.Now()

	if maxAgeBusy != 0 {
		extendedMaxBusy := 7 * 24 * time.Hour
		// First condition: instances without 'ttl' key using default max age
		busyCondition := squirrel.And{
			squirrel.Eq{"instance_pool": pool.Name},
			squirrel.Eq{"instance_state": types.StateInUse},
			squirrel.Lt{"instance_started": currentTime.Add(-maxAgeBusy).Unix()},
			squirrel.Expr("NOT (instance_labels ?? 'ttl')"),
		}
		for key, value := range queryParams.MatchLabels {
			condition := squirrel.Expr("(instance_labels->>?) = ?", key, value)
			busyCondition = append(busyCondition, condition)
		}

		// Second condition: instances with 'ttl' key using extended max age
		extendedBusyCondition := squirrel.And{
			squirrel.Eq{"instance_pool": pool.Name},
			squirrel.Eq{"instance_state": types.StateInUse},
			squirrel.Lt{"instance_started": currentTime.Add(-extendedMaxBusy).Unix()},
			squirrel.Expr("instance_labels ?? 'ttl'"),
		}
		for key, value := range queryParams.MatchLabels {
			condition := squirrel.Expr("(instance_labels->>?) = ?", key, value)
			extendedBusyCondition = append(extendedBusyCondition, condition)
		}
		conditions = append(conditions, busyCondition, extendedBusyCondition)
	}

	builder := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)
	deleteSQL, args, err := builder.Delete("instances").Where(conditions).Suffix("RETURNING instance_id, instance_name, instance_node_id").ToSql()
	if err != nil {
		return err
	}

	instances, err := d.instanceStore.DeleteAndReturn(ctx, deleteSQL, args...)
	if err != nil {
		return err
	}

	if len(instances) == 0 {
		return nil
	}

	var instanceNames []string

	for _, instance := range instances {
		instanceNames = append(instanceNames, instance.Name)
	}

	logr.Infof("distributed dlite: purger: Terminating stale instances\n%s", instanceNames)

	err = pool.Driver.Destroy(ctx, instances)
	if err != nil {
		logr.WithError(err).Errorf("distributed dlite: failed to delete instances of pool=%q", pool.Name)
	}

	// TODO: Move to outbox
	err = d.buildPool(ctx, pool, d.GetTLSServerName(), nil, d.setupInstanceWithHibernate)
	if err != nil {
		return fmt.Errorf("distributed dlite: failed to rebuld pool=%q error: %w", pool.Name, err)
	}

	return nil
}
