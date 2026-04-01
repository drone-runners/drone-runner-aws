package drivers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/Masterminds/squirrel"

	"github.com/drone/runner-go/logger"

	"github.com/harness/lite-engine/engine/spec"

	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"

	"github.com/sirupsen/logrus"
)

var _ IManager = (*DistributedManager)(nil)

const (
	defaultVariantID        = "default"
	stuckTerminatingMaxAge  = 2 * time.Minute
	stuckProvisioningMaxAge = 30 * time.Minute
)

type DistributedManager struct {
	Manager
	outboxStore store.OutboxStore
}

func NewDistributedManager(manager *Manager, outboxStore store.OutboxStore) *DistributedManager {
	return &DistributedManager{
		Manager:     *manager,
		outboxStore: outboxStore,
	}
}

// Provision returns an instance for a job execution and tags it as in use.
// This method and BuildPool method contain logic for maintaining pool size.
func (d *DistributedManager) Provision(
	ctx context.Context,
	poolName,
	serverName,
	ownerID string,
	provisionParams *types.ProvisionParams,
	query *types.QueryParams,
	gitspaceAgentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	instanceInfo *common.InstanceInfo,
	timeout int64,
	isMarkedForInfraReset bool,
	reservedCapacity *types.CapacityReservation,
	isCapacityTask bool,
) (inst *types.Instance, capReservation *types.CapacityReservation, warmed bool, variantID string, err error) {
	pool, err := d.validatePool(poolName)
	if err != nil {
		return nil, nil, false, "", err
	}
	return d.provisionFromPool(
		ctx,
		pool,
		serverName,
		ownerID,
		provisionParams,
		gitspaceAgentConfig,
		storageConfig,
		timeout,
		poolName,
		reservedCapacity,
		isCapacityTask,
	)
}

// GetPoolSpec returns the original provider-specific spec for the given pool name.
func (d *DistributedManager) GetPoolSpec(poolName string) (interface{}, error) {
	return d.Manager.GetPoolSpec(poolName)
}

func (d *DistributedManager) BuildPools(ctx context.Context) error {
	query := types.QueryParams{RunnerName: d.runnerName}
	buildPoolWrapper := func(ctx context.Context, pool *poolEntry, serverName string, query *types.QueryParams) error {
		return d.buildPool(ctx, pool, serverName, query, d.setupInstanceWithHibernate, d.setupInstanceAsync)
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
		allowedStates = append(allowedStates, types.StateCreated, types.StateHibernating, types.StateProvisioning)
	}

	// Set the pool name on the query parameters
	query.PoolName = pool.Name

	// 1. Find and claim all matching instances
	var instancesToDestroy []*types.Instance
	for {
		// Claim one instance at a time to avoid long transactions and to process them sequentially.
		instance, err := d.instanceStore.FindAndClaim(ctx, query, types.StateTerminating, allowedStates, false)
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
	instanceIDs := make([]string, len(instancesToDestroy))
	for i, inst := range instancesToDestroy {
		instanceIDs[i] = inst.ID
	}
	logrus.WithFields(logrus.Fields{
		"pool":           pool.Name,
		"instance_count": len(instancesToDestroy),
		"instance_ids":   instanceIDs,
		"destroy_busy":   destroyBusy,
		"destroy_free":   destroyFree,
		"destroy_caller": "distributed_cleanPool",
	}).Infoln("cleaning up instances")
	failedInstances, err := pool.Driver.Destroy(ctx, instancesToDestroy)
	if err != nil {
		logrus.WithError(err).Warnf("failed to destroy some instances in pool %q", pool.Name)
	}

	// Build a set of failed instance IDs for quick lookup
	failedIDs := make(map[string]bool)
	for _, inst := range failedInstances {
		failedIDs[inst.ID] = true
	}

	// 3. Delete only successfully destroyed instances from the database
	var successfulIDs []string
	var successfulInstances []*types.Instance
	for _, instance := range instancesToDestroy {
		if failedIDs[instance.ID] {
			logrus.Warnf("skipping db delete for failed instance %s", instance.ID)
			continue
		}
		successfulIDs = append(successfulIDs, instance.ID)
		successfulInstances = append(successfulInstances, instance)
	}

	if len(successfulIDs) > 0 {
		builder := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)
		deleteSQL, args, err := builder.Delete("instances").Where(squirrel.Eq{"instance_id": successfulIDs}).ToSql()
		if err != nil {
			return fmt.Errorf("failed to build delete query for cleaned instances: %w", err)
		}

		_, err = d.instanceStore.DeleteAndReturn(ctx, deleteSQL, args...)
		if err != nil {
			return fmt.Errorf("failed to delete destroyed instances from database: %w", err)
		}
	}

	d.destroyCapacity(ctx, successfulInstances)

	return nil
}

func (d *DistributedManager) GetInstanceStore() store.InstanceStore {
	return d.instanceStore
}

func (d *DistributedManager) GetStageOwnerStore() store.StageOwnerStore {
	return d.stageOwnerStore
}

func (d *DistributedManager) GetCapacityReservationStore() store.CapacityReservationStore {
	return d.capacityReservationStore
}

func (d *DistributedManager) GetTLSServerName() string {
	// keep server name constant since any runner should be able to send request to LE
	return "distributed-dlite"
}

func (d *DistributedManager) IsDistributed() bool {
	return true
}

// GetRunnerName returns the runner name.
func (d *DistributedManager) GetRunnerName() string {
	return d.runnerName
}

// SetupInstanceForPool sets up an instance for the given pool with the specified configuration.
// This method implements the jobs.InstanceSetupManager interface.
func (d *DistributedManager) SetupInstanceForPool(
	ctx context.Context,
	poolName string,
	setupParams *types.SetupInstanceParams,
) (*types.Instance, error) {
	pool, ok := d.poolMap[poolName]
	if !ok {
		return nil, fmt.Errorf("pool not found: %s", poolName)
	}
	vmImageConfig := vmImageConfigFromSetupParams(setupParams)
	return d.setupInstanceWithHibernate(
		ctx,
		pool,
		d.GetTLSServerName(),
		"",
		setupParams,
		vmImageConfig,
		nil,
		nil,
		-1,
		nil,
	)
}

// provisionFromReservedCapacity handles provisioning when reserved capacity is available
func (d *DistributedManager) provisionFromReservedCapacity(
	ctx context.Context,
	pool *poolEntry,
	tlsServerName, ownerID string,
	setupParams *types.SetupInstanceParams,
	vmImageConfig *spec.VMImageConfig,
	agentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	timeout int64,
	poolName string,
	reservedCapacity *types.CapacityReservation,
	isCapacityTask bool,
) (*types.Instance, bool, error) {
	if reservedCapacity.InstanceID != "" {
		inst, err := d.Find(ctx, reservedCapacity.InstanceID)
		if err == nil {
			return inst, true, nil
		}
		logger.FromContext(ctx).
			WithField("pool", poolName).
			WithField("instance_id", reservedCapacity.InstanceID).
			WithField("hotpool", true).
			WithField("destroy_caller", "distributed_provision:reserved_instance_not_found").
			Warnln("provision: failed to get instance from reserved warm pool, destroying capacity")
		_ = d.DestroyCapacity(ctx, reservedCapacity)
		reservedCapacity = nil
	}
	inst, _, err := d.setupInstance(ctx,
		pool,
		tlsServerName,
		ownerID,
		setupParams,
		vmImageConfig,
		true,
		agentConfig,
		storageConfig,
		timeout,
		nil,
		reservedCapacity,
		isCapacityTask,
	)
	if err != nil {
		return nil, false, fmt.Errorf("provision: failed to create instance: %w", err)
	}
	return inst, false, nil
}

// provisionFromPool overrides the Manager's provisionFromPool method to use FindAndClaim for distributed coordination
func (d *DistributedManager) provisionFromPool(
	ctx context.Context,
	pool *poolEntry,
	tlsServerName, ownerID string,
	provisionParams *types.ProvisionParams,
	agentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	timeout int64,
	poolName string,
	reservedCapacity *types.CapacityReservation,
	isCapacityTask bool,
) (inst *types.Instance, capReservation *types.CapacityReservation, warmed bool, variantID string, retErr error) {
	// Convert request params to internal setup params
	setupParams := provisionParams.ToSetupInstanceParams()
	vmImageConfig := provisionParams.GetVMImageConfig()

	// Variant filtering: select all matching variants in priority order based on provisionParams
	var matchedVariants []*types.PoolVariant
	if len(pool.PoolVariants) > 0 {
		matchedVariants = d.filterVariants(ctx, pool, provisionParams)
		if len(matchedVariants) > 0 {
			// Apply the first (highest priority) variant config for new instance creation
			applyVariantToSetupParams(setupParams, matchedVariants[0])
		}
	}

	// Clear the zones from the setup params for distributed driver to select the zone from the pool
	setupParams.Zones = []string{}

	// Case 1: Init task with reserved capacity
	if reservedCapacity != nil {
		resInst, hotpool, resErr := d.provisionFromReservedCapacity(
			ctx, pool, tlsServerName, ownerID, setupParams, vmImageConfig,
			agentConfig, storageConfig, timeout, poolName, reservedCapacity, isCapacityTask)
		if resErr != nil {
			return nil, nil, false, "", resErr
		}
		return resInst, nil, hotpool, resInst.VariantID, nil
	}

	// Case 2: Try to claim from hotpool across all matching variants (in priority order)
	allowedStates := []types.InstanceState{types.StateCreated}
	var err error
	var capacity *types.CapacityReservation

	variantsToTry := make([]string, 0, len(matchedVariants)+1)
	if len(matchedVariants) > 0 {
		for _, v := range matchedVariants {
			variantsToTry = append(variantsToTry, v.VariantID)
		}
	} else {
		variantsToTry = append(variantsToTry, defaultVariantID)
	}

	imageConfig := &types.VMImageConfig{}
	if vmImageConfig != nil {
		imageConfig.ImageName = vmImageConfig.ImageName
	}
	fullyQualifiedImageName, _ := pool.Driver.GetFullyQualifiedImage(ctx, imageConfig)

	for _, candidateVariantID := range variantsToTry {
		queryParams := &types.QueryParams{
			PoolName:             poolName,
			MachineType:          setupParams.MachineType,
			NestedVirtualization: setupParams.NestedVirtualization,
			VariantID:            candidateVariantID,
			ImageName:            fullyQualifiedImageName,
		}

		// Try to find and claim a free instance atomically
		inst, err = d.instanceStore.FindAndClaim(ctx, queryParams, types.StateInUse, allowedStates, true)
		if err != nil && err != sql.ErrNoRows {
			return nil, nil, false, candidateVariantID, fmt.Errorf("provision: failed to find and claim instance in %q pool for variant %q: %w", poolName, candidateVariantID, err)
		}

		// If we successfully claimed an instance, update it and return
		if inst != nil {
			inst.OwnerID = ownerID
			if err = d.instanceStore.Update(ctx, inst); err != nil {
				return nil, nil, false, candidateVariantID, fmt.Errorf("provision: failed to tag an instance in %q pool: %w", poolName, err)
			}
			logger.FromContext(ctx).
				WithField("pool", poolName).
				WithField("instance_id", inst.ID).
				WithField("hotpool", true).
				WithField("variant_id", candidateVariantID).
				Traceln("provision: claimed hotpool instance")

			d.setupInstanceAsync(ctx, inst.Pool, inst.RunnerName, &types.SetupInstanceParams{
				ImageName:            inst.Image,
				NestedVirtualization: inst.EnableNestedVirtualization,
				GPU:                  inst.GPU,
				MachineType:          inst.Size,
				Hibernate:            inst.IsHibernated,
				Zones:                []string{inst.Zone},
				VariantID:            inst.VariantID,
				DiskSize:             setupParams.DiskSize,
				DiskType:             setupParams.DiskType,
				ResourceClass:        setupParams.ResourceClass,
			})
			capacity = &types.CapacityReservation{
				InstanceID: inst.ID,
				PoolName:   poolName,
			}
			return inst, capacity, true, candidateVariantID, nil
		}
	}

	// set the variant ID to the first variant in the list
	variantID = variantsToTry[0]
	setupParams.VariantID = variantID

	// Case 3: No available hotpool instance across any variant → create new (using first variant's config)
	logger.FromContext(ctx).
		WithField("pool", poolName).
		WithField("hotpool", false).
		WithField("variant_id", variantID).
		WithField("variants_tried", variantsToTry).
		Traceln("provision: no hotpool instances available across any matching variant, creating new instance")

	inst, capacity, err = d.setupInstance(ctx,
		pool,
		tlsServerName,
		ownerID,
		setupParams,
		vmImageConfig,
		true,
		agentConfig,
		storageConfig,
		timeout,
		nil,
		reservedCapacity,
		isCapacityTask,
	)
	if err != nil {
		if isCapacityTask {
			return nil, nil, false, variantID, err
		}
		return nil, nil, false, variantID, fmt.Errorf("provision: failed to create instance: %w", err)
	}
	return inst, capacity, false, variantID, nil
}

// filterVariants returns all matching variants in priority order based on provisionParams criteria.
// Step 1: Filter by ResourceClass AND NestedVirtualization (both required). Returns nil if no matches.
// Step 2: When the provision request has a non-empty fully qualified image name, refine by image:
// variants with a matching image name are preferred; otherwise variants with no image name are used.
// If nothing qualifies in step 2, returns nil (no fallback to all step-1 candidates).
// When the provision image is empty, step 2 is skipped and step 1 candidates are returned.
// The order preserves the original pool configuration order.
func (d *DistributedManager) filterVariants(ctx context.Context, pool *poolEntry, provisionParams *types.ProvisionParams) []*types.PoolVariant {
	logr := logger.FromContext(ctx).WithField("pool", pool.Name)

	// Step 1: Filter by ResourceClass AND NestedVirtualization (both required)
	var candidates []*types.PoolVariant
	for i := range pool.PoolVariants {
		if pool.PoolVariants[i].ResourceClass == provisionParams.ResourceClass &&
			pool.PoolVariants[i].NestedVirtualization == provisionParams.NestedVirtualization {
			candidates = append(candidates, &pool.PoolVariants[i])
		}
	}

	if len(candidates) == 0 {
		logr.WithField("resource_class", provisionParams.ResourceClass).
			WithField("nested_virtualization", provisionParams.NestedVirtualization).
			Warnln("provision: no variants found matching resource_class and nested_virtualization")
		return nil
	}

	// Step 2: Optionally refine by ImageName (best-effort)
	var fullyQualifiedImageName string
	vmImageConfig := provisionParams.GetVMImageConfig()
	imageConfig := &types.VMImageConfig{}
	if vmImageConfig != nil {
		imageConfig.ImageName = vmImageConfig.ImageName
	}
	fullyQualifiedImageName, _ = pool.Driver.GetFullyQualifiedImage(ctx, imageConfig)

	if fullyQualifiedImageName == "" {
		variantIDs := make([]string, len(candidates))
		for i := range candidates {
			variantIDs[i] = candidates[i].VariantID
		}
		logr.WithField("variant_ids", variantIDs).
			Debugln("provision: fully qualified image name is empty, returning variants by resource_class and nested_virtualization")
		return candidates
	}

	var matched []*types.PoolVariant
	var noImageName []*types.PoolVariant
	for _, variant := range candidates {
		if variant.ImageName == "" {
			noImageName = append(noImageName, variant)
			continue
		}
		variantFQ, _ := pool.Driver.GetFullyQualifiedImage(ctx, &types.VMImageConfig{ImageName: variant.ImageName})
		if variantFQ == fullyQualifiedImageName {
			matched = append(matched, variant)
		}
	}

	var imageMatchedCandidates []*types.PoolVariant
	if len(matched) > 0 {
		imageMatchedCandidates = matched
	} else {
		imageMatchedCandidates = noImageName
	}

	if len(imageMatchedCandidates) == 0 {
		logr.WithField("resource_class", provisionParams.ResourceClass).
			WithField("image_name", fullyQualifiedImageName).
			Debugln("provision: no variants matched image filter")
		return nil
	}

	variantIDs := make([]string, len(imageMatchedCandidates))
	for i := range imageMatchedCandidates {
		variantIDs[i] = imageMatchedCandidates[i].VariantID
	}
	logr.WithField("variant_ids", variantIDs).
		Debugln("provision: matched variants in priority order")

	return imageMatchedCandidates
}

// applyVariantToSetupParams applies the selected variant's configuration to setupParams
func applyVariantToSetupParams(setupParams *types.SetupInstanceParams, variant *types.PoolVariant) {
	// Override with variant-specific values if they are set
	if variant.MachineType != "" {
		setupParams.MachineType = variant.MachineType
	}
	if variant.DiskSize != 0 {
		setupParams.DiskSize = variant.DiskSize
	}
	if variant.DiskType != "" {
		setupParams.DiskType = variant.DiskType
	}
	if variant.GPU {
		setupParams.GPU = true
	}
	// Set VariantID for tracking
	setupParams.VariantID = variant.VariantID
}

// setupInstanceAsync creates an outbox job for setting up the instance
func (d *DistributedManager) setupInstanceAsync(
	ctx context.Context, poolName, runnerName string, params *types.SetupInstanceParams,
) {
	if poolName == "" || runnerName == "" {
		logger.FromContext(ctx).Errorln("setupInstanceAsync: pool or runner name is empty")
		return
	}

	var jobParams *json.RawMessage
	if params != nil {
		// Marshal params to JSON
		paramsJSON, err := json.Marshal(params)
		if err != nil {
			logger.FromContext(ctx).WithError(err).Errorln("setupInstanceAsync: failed to marshal params")
			return
		}
		rawMsg := json.RawMessage(paramsJSON)
		jobParams = &rawMsg
	}

	// Create outbox job
	job := &types.OutboxJob{
		PoolName:   poolName,
		RunnerName: runnerName,
		JobType:    types.OutboxJobTypeSetupInstance,
		JobParams:  jobParams,
		Status:     types.OutboxJobStatusPending,
	}

	if err := d.outboxStore.Create(ctx, job); err != nil {
		logger.FromContext(ctx).WithError(err).Errorln("setupInstanceAsync: failed to create outbox job")
		return
	}

	logger.FromContext(ctx).
		WithField("job_id", job.ID).
		WithField("pool_name", poolName).
		WithField("runner_name", runnerName).
		Infoln("setupInstanceAsync: created outbox job for instance setup")
}

// setupInstanceWithHibernate handles setting up the instance into hibernate mode
func (d *DistributedManager) setupInstanceWithHibernate(
	ctx context.Context,
	pool *poolEntry,
	tlsServerName, ownerID string,
	setupParams *types.SetupInstanceParams,
	vmImageConfig *spec.VMImageConfig,
	agentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	timeout int64,
	platform *types.Platform,
) (*types.Instance, error) {
	inst, _, err := d.setupInstance(ctx,
		pool,
		tlsServerName,
		ownerID,
		setupParams,
		vmImageConfig,
		false,
		agentConfig,
		storageConfig,
		timeout,
		platform,
		nil,
		false)
	if err != nil {
		return nil, err
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logrus.WithField("panic", r).Errorln("panic in hibernate goroutine")
			}
		}()

		ctx := d.globalCtx
		// Step 1: Wait for instance connectivity
		if !d.waitForInstanceConnectivity(ctx, tlsServerName, inst.ID) {
			logrus.WithFields(logrus.Fields{
				"instanceID":     inst.ID,
				"pool":           pool.Name,
				"destroy_caller": "distributed_hibernator:connectivity_check_failed",
			}).Errorln("connectivity check failed, destroying instance and scheduling async setup")
			if derr := d.Destroy(ctx, pool.Name, inst.ID, inst, nil); derr != nil {
				logrus.WithError(derr).WithField("instanceID", inst.ID).Errorln("failed to cleanup instance after connectivity failure")
			}
			// Schedule async instance setup to replenish the pool
			d.setupInstanceAsync(ctx, pool.Name, d.runnerName, setupParams)
			return
		}

		// Step 2: Connectivity successful - update state to Created (VM is ready for use)
		inst.State = types.StateCreated
		if updateErr := d.instanceStore.Update(ctx, inst); updateErr != nil {
			logrus.WithError(updateErr).WithField("instanceID", inst.ID).Errorln("failed to update instance state to created")
			return
		}
		logrus.WithField("instanceID", inst.ID).Infoln("instance connectivity verified, state updated to created")

		// Step 3: Attempt to hibernate the instance
		shouldHibernate := false
		if setupParams != nil && setupParams.VariantID != "" && setupParams.VariantID != defaultVariantID {
			shouldHibernate = setupParams.Hibernate
		} else {
			shouldHibernate = pool.Driver.CanHibernate()
		}
		err = d.hibernate(ctx, pool.Name, inst, shouldHibernate)
		if err != nil {
			logrus.WithError(err).Errorln("failed to hibernate the vm")
		}
	}()
	return inst, nil
}

// hibernate handles hibernation for distributed manager using FindAndClaim
func (d *DistributedManager) hibernate(
	ctx context.Context,
	poolName string,
	instance *types.Instance,
	shouldHibernate bool,
) error {
	pool := d.poolMap[poolName]
	if pool == nil {
		return fmt.Errorf("hibernate: pool name %q not found", poolName)
	}

	if !shouldHibernate {
		return nil
	}

	// Use FindAndClaim to atomically set the instance state to hibernating
	queryParams := &types.QueryParams{
		PoolName:   poolName,
		InstanceID: instance.ID,
	}
	allowedStates := []types.InstanceState{types.StateCreated}
	claimedInstance, err := d.instanceStore.FindAndClaim(ctx, queryParams, types.StateHibernating, allowedStates, false)
	if err != nil {
		return fmt.Errorf("hibernate: failed to claim instance for hibernation for %q pool: %w", poolName, err)
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
func (d *DistributedManager) StartInstancePurger(ctx context.Context, maxAgeBusy, maxAgeFree, freeCapacityMaxAge, purgerTime time.Duration) error {
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
						d.startInstancePurger(ctx, pool, maxAgeBusy, maxAgeFree, freeCapacityMaxAge, &queryParams)
					}
				}
			}()
		}
	}()

	return nil
}

func (d *DistributedManager) startInstancePurger(ctx context.Context, pool *poolEntry, maxAgeBusy, maxAgeFree, freeCapacityMaxAge time.Duration, queryParams *types.QueryParams) {
	logr := logger.FromContext(ctx).
		WithField("driver", pool.Driver.DriverName()).
		WithField("pool", pool.Name)

	// Handle busy instance cleanup
	if maxAgeBusy != 0 {
		if err := d.cleanupBusyInstances(ctx, pool, maxAgeBusy, queryParams); err != nil {
			logr.WithError(err).Error("distributed dlite: purger: failed to cleanup busy instances")
		}
	}

	// Handle free instance cleanup
	if maxAgeFree != 0 {
		if err := d.cleanupFreeInstances(ctx, pool, maxAgeFree, queryParams); err != nil {
			logr.WithError(err).Error("distributed dlite: purger: failed to cleanup free instances")
		}
	}

	if freeCapacityMaxAge != 0 {
		d.cleanupCapacities(ctx, pool, freeCapacityMaxAge)
	}
}

// cleanupBusyInstances handles cleanup of busy instances (StateInUse, StateTerminating)
func (d *DistributedManager) cleanupBusyInstances(ctx context.Context, pool *poolEntry, maxAgeBusy time.Duration, queryParams *types.QueryParams) error {
	conditions := squirrel.Or{}
	currentTime := time.Now()
	extendedMaxBusy := 7 * 24 * time.Hour

	// First condition: instances without 'ttl' key using default max age
	busyCondition := squirrel.And{
		squirrel.Eq{"instance_pool": pool.Name},
		squirrel.Or{
			squirrel.Eq{"instance_state": types.StateInUse},
			squirrel.Eq{"instance_state": types.StateTerminating},
		},
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
		squirrel.Or{
			squirrel.Eq{"instance_state": types.StateInUse},
			squirrel.Eq{"instance_state": types.StateTerminating},
		},
		squirrel.Lt{"instance_started": currentTime.Add(-extendedMaxBusy).Unix()},
		squirrel.Expr("instance_labels ?? 'ttl'"),
	}
	for key, value := range queryParams.MatchLabels {
		condition := squirrel.Expr("(instance_labels->>?) = ?", key, value)
		extendedBusyCondition = append(extendedBusyCondition, condition)
	}

	// Third condition: instances stuck in terminating state for more than 5 minutes
	stuckTerminatingCondition := squirrel.And{
		squirrel.Eq{"instance_pool": pool.Name},
		squirrel.Eq{"instance_state": types.StateTerminating},
		squirrel.Lt{"instance_updated": currentTime.Add(-stuckTerminatingMaxAge).Unix()},
	}
	conditions = append(conditions, busyCondition, extendedBusyCondition, stuckTerminatingCondition)
	_, err := d.executeInstanceCleanup(ctx, pool, conditions, "busy")
	return err
}

// cleanupFreeInstances handles cleanup of free instances (StateCreated, StateHibernating)
func (d *DistributedManager) cleanupFreeInstances(ctx context.Context, pool *poolEntry, maxAgeFree time.Duration, queryParams *types.QueryParams) error {
	currentTime := time.Now()

	// Condition for free instances (StateCreated, StateHibernating)
	freeCondition := squirrel.And{
		squirrel.Eq{"instance_pool": pool.Name},
		squirrel.Or{
			squirrel.Eq{"instance_state": types.StateCreated},
			squirrel.Eq{"instance_state": types.StateHibernating},
		},
		squirrel.Lt{"instance_started": currentTime.Add(-maxAgeFree).Unix()},
	}
	for key, value := range queryParams.MatchLabels {
		condition := squirrel.Expr("(instance_labels->>?) = ?", key, value)
		freeCondition = append(freeCondition, condition)
	}

	// Condition for stuck provisioning instances (StateProvisioning for more than 30 minutes)
	stuckProvisioningCondition := squirrel.And{
		squirrel.Eq{"instance_pool": pool.Name},
		squirrel.Eq{"instance_state": types.StateProvisioning},
		squirrel.Lt{"instance_started": currentTime.Add(-stuckProvisioningMaxAge).Unix()},
	}

	conditions := squirrel.Or{freeCondition, stuckProvisioningCondition}

	// Execute cleanup and call setupInstanceAsync for each cleaned instance
	instances, err := d.executeInstanceCleanup(ctx, pool, conditions, "free")

	// Call setupInstanceAsync for each cleaned free instance
	for _, instance := range instances {
		d.setupInstanceAsync(ctx, pool.Name, instance.RunnerName, &types.SetupInstanceParams{
			ImageName:            instance.Image,
			NestedVirtualization: instance.EnableNestedVirtualization,
			GPU:                  instance.GPU,
			MachineType:          instance.Size,
			VariantID:            instance.VariantID,
			Zones:                []string{instance.Zone},
			Hibernate:            instance.IsHibernated,
		})
	}
	return err
}

func (d *DistributedManager) cleanupCapacities(ctx context.Context, pool *poolEntry, freeCapacityMaxAge time.Duration) {
	// Calculate the cutoff time for stale capacity reservations
	createdAtBefore := time.Now().Add(-freeCapacityMaxAge).Unix()

	var capacitiesToDelete []*types.CapacityReservation

	// List capacity reservations stuck in "terminating" state
	// Always clean stale terminating capacity reservations first to duplicate detection
	staleCapacities, err := d.capacityReservationStore.List(
		ctx,
		&types.CapacityReservationQueryParams{
			PoolName:        pool.Name,
			CreatedAtBefore: createdAtBefore,
		},
		[]types.CapacityReservationState{types.CapacityReservationStateTerminating},
	)

	if err != nil {
		logger.FromContext(ctx).
			WithField("pool", pool.Name).
			WithError(err).
			Error("distributed dlite: purger: failed to list stale terminating capacity reservations")
	} else {
		capacitiesToDelete = append(capacitiesToDelete, staleCapacities...)
	}

	// Use FindAndClaim to atomically find and claim stale capacity reservations
	// Only claim capacities that are in "created" state (not yet in use)
	freeCapacities, err := d.capacityReservationStore.FindAndClaim(
		ctx,
		&types.CapacityReservationQueryParams{
			PoolName:        pool.Name,
			CreatedAtBefore: createdAtBefore,
		},
		types.CapacityReservationStateTerminating,
		[]types.CapacityReservationState{types.CapacityReservationStateCreated},
	)
	if err != nil {
		logger.FromContext(ctx).
			WithField("pool", pool.Name).
			WithError(err).
			Error("distributed dlite: purger: failed to find and claim stale capacity reservations")
	} else {
		capacitiesToDelete = append(capacitiesToDelete, freeCapacities...)
	}

	if len(capacitiesToDelete) == 0 {
		return
	}

	stageIDs := make([]string, len(capacitiesToDelete))
	reservationIDs := make([]string, len(capacitiesToDelete))
	instanceIDs := make([]string, len(capacitiesToDelete))
	reservationStates := make([]string, len(capacitiesToDelete))
	for i, c := range capacitiesToDelete {
		stageIDs[i] = c.StageID
		reservationIDs[i] = c.ReservationID
		instanceIDs[i] = c.InstanceID
		reservationStates[i] = string(c.ReservationState)
	}
	logger.FromContext(ctx).
		WithField("pool", pool.Name).
		WithField("count", len(capacitiesToDelete)).
		WithField("stage_ids", stageIDs).
		WithField("reservation_ids", reservationIDs).
		WithField("instance_ids", instanceIDs).
		WithField("reservation_states", reservationStates).
		WithField("destroy_caller", "distributed_purger:capacity_cleanup").
		Infof("distributed dlite: purger: cleaning up %d stale capacity reservations", len(capacitiesToDelete))

	d.destroyCapacityFromReservation(ctx, capacitiesToDelete)
}

// executeInstanceCleanup performs cleanup and returns the instances for further processing
func (d *DistributedManager) executeInstanceCleanup(ctx context.Context, pool *poolEntry, conditions squirrel.Or, cleanupType string) ([]*types.Instance, error) {
	logr := logger.FromContext(ctx).
		WithField("driver", pool.Driver.DriverName()).
		WithField("pool", pool.Name).
		WithField("cleanup_type", cleanupType)
	builder := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)
	deleteSQL, args, err := builder.Delete("instances").Where(conditions).Suffix("RETURNING instance_id, instance_name, instance_node_id, runner_name").ToSql()
	if err != nil {
		return nil, err
	}

	instances, err := d.instanceStore.DeleteAndReturn(ctx, deleteSQL, args...)
	if err != nil {
		return nil, err
	}

	if len(instances) == 0 {
		return instances, nil
	}

	var instanceNames []string
	for _, instance := range instances {
		instanceNames = append(instanceNames, instance.Name)
	}

	logr.WithField("instance_names", instanceNames).
		WithField("destroy_caller", "distributed_purger:"+cleanupType).
		Infof("distributed dlite: purger: Terminating %d stale %s instances", len(instances), cleanupType)

	failedInstances, err := pool.Driver.Destroy(ctx, instances)
	if err != nil {
		logr.WithError(err).Errorf("distributed dlite: failed to delete %s instances of pool=%q", cleanupType, pool.Name)
	}

	// Build a set of failed instance IDs for quick lookup
	failedIDs := make(map[string]bool)
	for _, inst := range failedInstances {
		failedIDs[inst.ID] = true
	}

	// Only destroy capacity for successfully deleted instances
	var successfulInstances []*types.Instance
	for _, inst := range instances {
		if !failedIDs[inst.ID] {
			successfulInstances = append(successfulInstances, inst)
		}
	}

	d.destroyCapacity(ctx, successfulInstances)
	return successfulInstances, nil
}

func (d *DistributedManager) destroyCapacity(ctx context.Context, instances []*types.Instance) {
	if d.capacityReservationStore != nil {
		// traverse the instances and destroy the capacity reservation
		for _, instance := range instances {
			if instance.Stage != "" {
				capacity, _ := d.capacityReservationStore.Find(ctx, instance.Stage)
				if capacity != nil {
					logrus.WithFields(logrus.Fields{
						"instance_id":      instance.ID,
						"stage_runtime_id": instance.Stage,
						"reservation_id":   capacity.ReservationID,
						"destroy_caller":   "distributed_destroyCapacity:post_instance_destroy",
					}).Infoln("destroy_capacity: destroying capacity reservation for destroyed instance")
					err := d.DestroyCapacity(ctx, capacity)
					if err != nil {
						logrus.WithError(err).Errorf("failed to delete capacity of stage %s from reservation store", capacity.StageID)
					}
				}
			}
		}
	}
}

func (d *DistributedManager) destroyCapacityFromReservation(ctx context.Context, capacities []*types.CapacityReservation) {
	if d.capacityReservationStore != nil {
		// traverse the instances and destroy the capacity reservation
		for _, capacity := range capacities {
			if capacity != nil {
				logrus.WithFields(logrus.Fields{
					"stage_runtime_id": capacity.StageID,
					"reservation_id":   capacity.ReservationID,
					"pool":             capacity.PoolName,
					"destroy_caller":   "distributed_destroyCapacityFromReservation:stale_reservation",
				}).Infoln("destroy_capacity: destroying stale capacity reservation")
				err := d.DestroyCapacity(ctx, capacity)
				if err != nil {
					logrus.WithError(err).Errorf("failed to delete capacity of stage %s from reservation store", capacity.StageID)
				}
			}
		}
	}
}
