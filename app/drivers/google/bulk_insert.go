package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/drone/runner-go/logger"
	"github.com/google/uuid"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/types"
)

// These settings are intentionally hardcoded while the pool and Helm
// configuration contract is being designed.
const (
	defaultBulkInsertReconcileTimeout      = 30 * time.Second
	defaultBulkInsertReconcilePollInterval = 2 * time.Second
	bulkInsertFallbackRankOffset           = 2
	bulkInsertOperationDone                = "DONE"
	maxBulkInsertFallbacks                 = 9
)

type definitiveBulkInsertError struct {
	err error
}

type bulkInsertSubmissionError struct {
	err error
}

func (e *definitiveBulkInsertError) Error() string {
	return e.err.Error()
}

func (e *definitiveBulkInsertError) Unwrap() error {
	return e.err
}

func (e *bulkInsertSubmissionError) Error() string {
	return e.err.Error()
}

func (e *bulkInsertSubmissionError) Unwrap() error {
	return e.err
}

// bulkInsertCandidates returns the candidates from the first candidate's
// region and network configuration. One regional bulkInsert request cannot
// span regions or use different instance network properties.
func bulkInsertCandidates(candidates []createCandidate) []createCandidate {
	if len(candidates) == 0 {
		return nil
	}

	matching := make([]createCandidate, 0, len(candidates))
	first := candidates[0]
	for _, candidate := range candidates {
		if sameBulkInsertCandidateGroup(&first, &candidate) {
			matching = append(matching, candidate)
		}
	}
	return matching
}

func (p *config) buildRegionalBulkInsertCandidates(candidates []createCandidate) []createCandidate {
	if len(candidates) == 0 {
		return nil
	}
	first := candidates[0]
	regional := bulkInsertCandidates(candidates)
	seen := make(map[string]struct{}, len(regional))
	for _, candidate := range regional {
		seen[candidate.zone] = struct{}{}
	}

	add := func(candidate createCandidate) {
		if _, exists := seen[candidate.zone]; exists || !sameBulkInsertCandidateGroup(&first, &candidate) {
			return
		}
		seen[candidate.zone] = struct{}{}
		regional = append(regional, candidate)
	}
	enumerate := func(network *networkConfig) {
		for _, zone := range network.zones {
			resolvedNetwork, subnetwork, resolvedZone, tags := network.resolve(p.projectID, zone, p.GetRegion)
			add(createCandidate{
				zone:       resolvedZone,
				network:    resolvedNetwork,
				subnetwork: subnetwork,
				tags:       tags,
				proxyURL:   network.proxyURL,
			})
		}
	}

	if len(p.networkConfigs) > 0 {
		for index := range p.networkConfigs {
			enumerate(&p.networkConfigs[index])
		}
	} else {
		enumerate(&networkConfig{
			network:    p.network,
			subnetwork: p.subnetwork,
			tags:       p.tags,
			zones:      p.zones,
		})
	}
	return regional
}

func sameBulkInsertCandidateGroup(first, candidate *createCandidate) bool {
	return regionFromZone(candidate.zone) == regionFromZone(first.zone) &&
		candidate.network == first.network &&
		candidate.subnetwork == first.subnetwork &&
		candidate.proxyURL == first.proxyURL &&
		slices.Equal(candidate.tags, first.tags)
}

func regionFromZone(zone string) string {
	if index := strings.LastIndex(zone, "-"); index > 0 {
		return zone[:index]
	}
	return ""
}

func buildRegionalBulkInsertRequest(
	in *compute.Instance,
	machineType, bootDiskType string,
	fallbacks []types.MachineTypeFallback,
	zones []string,
) (*compute.BulkInsertInstanceResource, error) {
	if err := validateMachineTypeFallbacks(fallbacks); err != nil {
		return nil, err
	}

	locations := make(map[string]compute.LocationPolicyLocation, len(zones))
	zoneConfigurations := make([]*compute.LocationPolicyZoneConfiguration, 0, len(zones))
	for _, zone := range zones {
		zoneURL := "zones/" + zone
		locations[zoneURL] = compute.LocationPolicyLocation{Preference: "ALLOW"}
		zoneConfigurations = append(zoneConfigurations, &compute.LocationPolicyZoneConfiguration{Zone: zoneURL})
	}

	selections := make(map[string]compute.InstanceFlexibilityPolicyInstanceSelection, len(fallbacks)+1)
	selections["rank-1"] = compute.InstanceFlexibilityPolicyInstanceSelection{
		Rank:         1,
		MachineTypes: []string{machineType},
		Disks:        cloneBulkInsertBootDisks(in.Disks, bootDiskType),
	}
	for index, fallback := range fallbacks {
		rank := index + bulkInsertFallbackRankOffset
		selections[fmt.Sprintf("rank-%d", rank)] = compute.InstanceFlexibilityPolicyInstanceSelection{
			Rank:         int64(rank),
			MachineTypes: []string{fallback.MachineType},
			Disks:        cloneBulkInsertBootDisks(in.Disks, fallback.DiskType),
		}
	}

	return &compute.BulkInsertInstanceResource{
		Count:    1,
		MinCount: 1,
		PerInstanceProperties: map[string]compute.BulkInsertInstanceResourcePerInstanceProperties{
			in.Name: {},
		},
		InstanceProperties: &compute.InstanceProperties{
			AdvancedMachineFeatures:    in.AdvancedMachineFeatures,
			CanIpForward:               in.CanIpForward,
			ConfidentialInstanceConfig: in.ConfidentialInstanceConfig,
			Description:                in.Description,
			GuestAccelerators:          in.GuestAccelerators,
			KeyRevocationActionType:    in.KeyRevocationActionType,
			Labels:                     in.Labels,
			Metadata:                   in.Metadata,
			MinCpuPlatform:             in.MinCpuPlatform,
			NetworkInterfaces:          in.NetworkInterfaces,
			NetworkPerformanceConfig:   in.NetworkPerformanceConfig,
			PrivateIpv6GoogleAccess:    in.PrivateIpv6GoogleAccess,
			ReservationAffinity:        in.ReservationAffinity,
			ResourcePolicies:           in.ResourcePolicies,
			Scheduling:                 in.Scheduling,
			ServiceAccounts:            in.ServiceAccounts,
			ShieldedInstanceConfig:     in.ShieldedInstanceConfig,
			Tags:                       in.Tags,
			WorkloadIdentityConfig:     in.WorkloadIdentityConfig,
		},
		InstanceFlexibilityPolicy: &compute.InstanceFlexibilityPolicy{InstanceSelections: selections},
		LocationPolicy: &compute.LocationPolicy{
			Locations:   locations,
			TargetShape: "ANY",
			Zones:       zoneConfigurations,
		},
	}, nil
}

func validateMachineTypeFallbacks(fallbacks []types.MachineTypeFallback) error {
	for index, fallback := range fallbacks {
		if fallback.MachineType == "" || fallback.DiskType == "" {
			return fmt.Errorf("google: machine_type_fallbacks[%d] requires machine_type and disk_type", index)
		}
	}
	return nil
}

func cloneBulkInsertBootDisks(disks []*compute.AttachedDisk, bootDiskType string) []*compute.AttachedDisk {
	cloned := make([]*compute.AttachedDisk, len(disks))
	for index, disk := range disks {
		if disk == nil {
			continue
		}
		diskCopy := *disk
		if disk.InitializeParams != nil {
			initializeParamsCopy := *disk.InitializeParams
			diskCopy.InitializeParams = &initializeParamsCopy
			if disk.Boot {
				diskCopy.InitializeParams.DiskType = bootDiskType
			}
		}
		cloned[index] = &diskCopy
	}
	return cloned
}

// bulkInsertPlacement is set only when regional bulkInsert created a VM.
// The created machine type is known after the subsequent instance get.
type bulkInsertPlacement struct {
	poolID        string
	resourceClass string
	region        string
	zone          string
	requestedType string
	fallbacks     []types.MachineTypeFallback
}

func (p *config) insertWithBulkFallback(
	ctx context.Context,
	in *compute.Instance,
	candidates []createCandidate,
	opts *types.InstanceCreateOpts,
	machineType, bootDiskType string,
	stockoutRetryEnabled, usesReservation bool,
	logr logger.Logger,
) (*compute.Operation, createCandidate, *bulkInsertPlacement, error) {
	if opts.DisableMachineTypeFallbacks {
		op, candidate, err := p.insertWithStockoutRetry(
			ctx, in, candidates, opts, machineType, bootDiskType,
			stockoutRetryEnabled, usesReservation, logr,
		)
		return op, candidate, nil, err
	}
	fallbacks := opts.MachineTypeFallbacks
	if len(fallbacks) == 0 {
		fallbacks = p.machineTypeFallbacks
	}
	if len(fallbacks) > maxBulkInsertFallbacks {
		fallbacks = fallbacks[:maxBulkInsertFallbacks]
	}
	if len(fallbacks) > 0 {
		if err := validateMachineTypeFallbacks(fallbacks); err != nil {
			return nil, createCandidate{}, nil, err
		}
	}
	regionalCandidates := p.buildRegionalBulkInsertCandidates(candidates)
	if len(fallbacks) == 0 || !stockoutRetryEnabled || usesReservation || len(regionalCandidates) == 0 {
		op, candidate, err := p.insertWithStockoutRetry(
			ctx, in, candidates, opts, machineType, bootDiskType,
			stockoutRetryEnabled, usesReservation, logr,
		)
		return op, candidate, nil, err
	}

	first := regionalCandidates[0]
	in.NetworkInterfaces[0].Network = first.network
	in.NetworkInterfaces[0].Subnetwork = first.subnetwork
	in.Tags = &compute.Tags{Items: append(append([]string{}, first.tags...), in.Name)}
	opts.EgressProxyURL = resolveEgressProxyURL(p.egressControl, first.proxyURL)
	userData, err := lehelper.GenerateUserdata(p.userData, opts)
	if err != nil {
		return nil, first, nil, err
	}
	setUserdataMetadata(in, p.userDataKey, userData)

	zones := make([]string, len(regionalCandidates))
	for index := range regionalCandidates {
		zones[index] = regionalCandidates[index].zone
	}
	region := regionFromZone(zones[0])
	started := time.Now()
	operation, zone, err := p.insertRegionalBulkInstance(
		ctx, in, machineType, bootDiskType, fallbacks, zones,
	)
	_, reason := classifyGCPError(context.Background(), err, classifyOpts{})
	if err == nil {
		succeeded := first
		succeeded.zone = zone
		logr.WithField("zone", zone).Debugln("google: regional bulkInsert provisioned VM")
		p.metrics.RecordBulkInsertAttempt(opts.PoolName, opts.ResourceClass, region, machineType, metric.BulkInsertOutcomeSuccess, reason, time.Since(started))
		return operation, succeeded, &bulkInsertPlacement{
			poolID:        opts.PoolName,
			resourceClass: opts.ResourceClass,
			region:        region,
			zone:          zone,
			requestedType: machineType,
			fallbacks:     append([]types.MachineTypeFallback(nil), fallbacks...),
		}, nil
	}

	var definitiveError *definitiveBulkInsertError
	if !errors.As(err, &definitiveError) {
		reconciled, reconcileOutcome := p.reconcileAmbiguousBulkInsert(in.Name, regionalCandidates, logr)
		p.metrics.RecordBulkInsertReconcile(region, reconcileOutcome)
		p.metrics.RecordBulkInsertAttempt(opts.PoolName, opts.ResourceClass, region, machineType, metric.BulkInsertOutcomeAmbiguous, reason, time.Since(started))
		return nil, reconciled, nil, err
	}
	logr.WithError(err).Warnln("google: regional bulkInsert failed; using zonal create path")
	p.metrics.RecordBulkInsertAttempt(opts.PoolName, opts.ResourceClass, region, machineType, metric.BulkInsertOutcomeRejected, reason, time.Since(started))
	op, candidate, retryErr := p.insertWithStockoutRetry(
		ctx, in, candidates, opts, machineType, bootDiskType,
		stockoutRetryEnabled, usesReservation, logr,
	)
	return op, candidate, nil, retryErr
}

func recordBulkInsertPlacement(m *metric.Metrics, placement *bulkInsertPlacement, createdMachineType string) {
	if m == nil || placement == nil {
		return
	}
	created := createdMachineType
	if created == "" || created == "." || created == "/" {
		created = placement.requestedType
	}
	m.RecordBulkInsertPlacement(
		placement.poolID,
		placement.resourceClass,
		placement.region,
		placement.zone,
		created,
		bulkInsertRank(created, placement.requestedType, placement.fallbacks),
	)
}

func bulkInsertRank(created, requested string, fallbacks []types.MachineTypeFallback) string {
	if created == requested {
		return "1"
	}
	for index, fallback := range fallbacks {
		if fallback.MachineType == created {
			return strconv.Itoa(index + bulkInsertFallbackRankOffset)
		}
	}
	return "0"
}

func (p *config) insertRegionalBulkInstance(
	ctx context.Context,
	in *compute.Instance,
	machineType, bootDiskType string,
	fallbacks []types.MachineTypeFallback,
	zones []string,
) (*compute.Operation, string, error) {
	region := regionFromZone(zones[0])
	request, err := buildRegionalBulkInsertRequest(in, machineType, bootDiskType, fallbacks, zones)
	if err != nil {
		return nil, "", err
	}
	requestID := uuid.New().String()
	var finalOperation *compute.Operation
	var zone string
	err = trackOperation(
		ctx,
		p.metrics,
		metric.GCPResourceInstance,
		metric.GCPOperationInsert,
		region,
		machineType,
		classifyOpts{},
		func() error {
			var operationErr error
			finalOperation, operationErr = p.submitAndWaitRegionalBulkInsert(
				ctx, region, requestID, request,
			)
			if operationErr != nil {
				if isDefinitiveBulkInsertRejection(operationErr) {
					return &definitiveBulkInsertError{
						err: fmt.Errorf("google: bulkInsert request rejected: %w", operationErr),
					}
				}
				finalOperation, operationErr = p.resolveAmbiguousBulkInsert(
					region, requestID, request,
				)
				if operationErr != nil {
					return fmt.Errorf("google: bulkInsert operation outcome is unknown: %w", operationErr)
				}
			}
			zone, operationErr = completedBulkInsertZone(finalOperation)
			return operationErr
		},
	)
	return finalOperation, zone, err
}

func completedBulkInsertZone(operation *compute.Operation) (string, error) {
	if zone, err := zoneFromBulkInsertMetadata(operation); err == nil {
		return zone, nil
	}
	if operation != nil && operation.Error != nil && len(operation.Error.Errors) > 0 {
		operationError := operation.Error.Errors[0]
		return "", &definitiveBulkInsertError{
			err: fmt.Errorf(
				"google: bulkInsert operation failed code=%s: %s",
				operationError.Code,
				operationError.Message,
			),
		}
	}
	if operation == nil ||
		operation.InstancesBulkInsertOperationMetadata == nil ||
		len(operation.InstancesBulkInsertOperationMetadata.PerLocationStatus) == 0 {
		return "", errors.New("google: bulkInsert operation completed without placement metadata")
	}
	return "", &definitiveBulkInsertError{
		err: errors.New("google: bulkInsert operation completed without creating a VM"),
	}
}

func (p *config) submitAndWaitRegionalBulkInsert(
	ctx context.Context,
	region, requestID string,
	request *compute.BulkInsertInstanceResource,
) (*compute.Operation, error) {
	insertOperation, err := apiCall(
		ctx,
		p.metrics,
		metric.GCPResourceInstance,
		metric.GCPOperationInsert,
		region,
		classifyOpts{},
		func() (*compute.Operation, error) {
			return p.service.RegionInstances.BulkInsert(p.projectID, region, request).
				RequestId(requestID).
				Context(ctx).
				Do()
		},
	)
	if err != nil {
		return nil, &bulkInsertSubmissionError{err: err}
	}
	if insertOperation == nil || insertOperation.Name == "" {
		return nil, errors.New("google: bulkInsert response did not include an operation")
	}
	return waitRegionOperationWithMetrics(
		ctx, p.service, p.metrics, p.projectID, region, insertOperation.Name,
	)
}

func (p *config) resolveAmbiguousBulkInsert(
	region, requestID string,
	request *compute.BulkInsertInstanceResource,
) (*compute.Operation, error) {
	timeout, pollInterval := p.bulkInsertReconcileSettings()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var lastErr error
	for {
		operation, err := p.submitAndWaitRegionalBulkInsert(ctx, region, requestID, request)
		if err == nil {
			return operation, nil
		}
		lastErr = err

		if waitForBulkInsertRetry(ctx, pollInterval) != nil {
			return nil, fmt.Errorf("timed out resolving idempotent bulkInsert request: %w", lastErr)
		}
	}
}

func isDefinitiveBulkInsertRejection(err error) bool {
	var submissionError *bulkInsertSubmissionError
	if !errors.As(err, &submissionError) {
		return false
	}
	var googleError *googleapi.Error
	if !errors.As(submissionError, &googleError) {
		return false
	}
	switch googleError.Code {
	case http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusMethodNotAllowed,
		http.StatusPreconditionFailed,
		http.StatusUnprocessableEntity:
		return true
	default:
		return false
	}
}

func (p *config) reconcileAmbiguousBulkInsert(
	name string,
	candidates []createCandidate,
	logr logger.Logger,
) (createCandidate, string) {
	timeout, pollInterval := p.bulkInsertReconcileSettings()

	// The caller context is commonly the source of the ambiguous outcome. Use a
	// bounded independent context so a canceled request cannot skip cleanup
	// reconciliation for a VM that GCP may still be creating.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	sawNonNotFound := false
	for {
		for _, candidate := range candidates {
			_, err := apiCall(
				ctx,
				p.metrics,
				metric.GCPResourceInstance,
				metric.GCPOperationGet,
				candidate.zone,
				classifyOpts{isZoneScan: true},
				func() (*compute.Instance, error) {
					return p.service.Instances.Get(p.projectID, candidate.zone, name).
						Context(ctx).
						Do()
				},
			)
			if err == nil {
				logr.WithField("zone", candidate.zone).
					Warnln("google: found VM after ambiguous regional bulkInsert outcome")
				return candidate, metric.BulkInsertReconcileFound
			}
			var googleError *googleapi.Error
			if !errors.As(err, &googleError) || googleError.Code != http.StatusNotFound {
				sawNonNotFound = true
				logr.WithField("zone", candidate.zone).
					WithError(err).
					Warnln("google: failed to reconcile ambiguous regional bulkInsert outcome")
			}
		}

		if waitForBulkInsertRetry(ctx, pollInterval) != nil {
			if sawNonNotFound {
				return createCandidate{}, metric.BulkInsertReconcileTimeout
			}
			return createCandidate{}, metric.BulkInsertReconcileNotFound
		}
	}
}

// getCreatedInstance completes the read after a create operation that was
// confirmed despite the original caller context expiring during reconciliation.
func (p *config) getCreatedInstance(
	ctx context.Context,
	project, zone, name string,
) (*compute.Instance, error) {
	vm, err := p.getInstance(ctx, project, zone, name)
	if err == nil || ctx.Err() == nil {
		return vm, err
	}

	timeout, _ := p.bulkInsertReconcileSettings()
	reconcileCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	vm, reconcileErr := p.getInstance(reconcileCtx, project, zone, name)
	if reconcileErr != nil {
		return nil, fmt.Errorf(
			"google: get VM %q after create with expired caller context: %w",
			name, reconcileErr,
		)
	}
	return vm, nil
}

func (p *config) bulkInsertReconcileSettings() (timeout, pollInterval time.Duration) {
	timeout = p.bulkInsertReconcileTimeout
	if timeout <= 0 {
		timeout = defaultBulkInsertReconcileTimeout
	}
	pollInterval = p.bulkInsertReconcilePollInterval
	if pollInterval <= 0 {
		pollInterval = defaultBulkInsertReconcilePollInterval
	}
	return timeout, pollInterval
}

func waitRegionOperation(ctx context.Context, svc *compute.Service, project, region, name string) (*compute.Operation, error) {
	return waitRegionOperationWithMetrics(ctx, svc, nil, project, region, name)
}

func waitRegionOperationWithMetrics(
	ctx context.Context,
	svc *compute.Service,
	m *metric.Metrics,
	project, region, name string,
) (*compute.Operation, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		opCtx, cancel := context.WithTimeout(ctx, operationGetTimeout*time.Second)
		op, err := apiCall(
			opCtx,
			m,
			metric.GCPResourceRegion,
			metric.GCPOperationGet,
			region,
			classifyOpts{},
			func() (*compute.Operation, error) {
				return svc.RegionOperations.Get(project, region, name).Context(opCtx).Do()
			},
		)
		cancel()
		if err != nil {
			if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == http.StatusNotFound {
				return nil, errors.New("not Found")
			}
			if shouldRetry(err) {
				recordRetry(m, metric.GCPResourceRegion, metric.GCPOperationGet, region, err)
				if waitErr := waitForBulkInsertRetry(ctx, retryBackoffSec*time.Second); waitErr != nil {
					return nil, waitErr
				}
				continue
			}
			return nil, err
		}
		if op.Status == bulkInsertOperationDone {
			return op, nil
		}
		if err := waitForBulkInsertRetry(ctx, retryBackoffSec*time.Second); err != nil {
			return nil, err
		}
	}
}

func waitForBulkInsertRetry(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func zoneFromBulkInsertMetadata(op *compute.Operation) (string, error) {
	if op == nil || op.InstancesBulkInsertOperationMetadata == nil {
		return "", errors.New("google: bulkInsert operation missing per-location metadata")
	}
	for zoneURL, status := range op.InstancesBulkInsertOperationMetadata.PerLocationStatus {
		if status.CreatedVmCount > status.DeletedVmCount {
			return path.Base(zoneURL), nil
		}
	}
	return "", errors.New("google: bulkInsert operation completed with no created VM")
}
