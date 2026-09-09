package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
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

// BulkInsertSelection is one ranked machine-type group. Types in the same
// selection share Disks (which override InstanceProperties.Disks when set).
type BulkInsertSelection struct {
	Name         string
	Rank         int64
	MachineTypes []string
	Disks        []*compute.AttachedDisk
}

// BulkInsertParams is a standalone regional bulkInsert request. It is not
// used by Create() or the rest of the driver.
type BulkInsertParams struct {
	Project string
	Region  string
	Name    string
	Zones   []string
	// MachineType is used when Selections is empty.
	MachineType string
	SourceImage string
	DiskType    string
	DiskSizeGb  int64
	Network     string
	Subnetwork  string
	// PrivateIP omits an external NAT access config.
	PrivateIP  bool
	Selections []BulkInsertSelection
	// DenyZones are marked DENY in locationPolicy so GCP cannot pick them.
	DenyZones []string
	// ExtraNames requests more than one VM. Count becomes 1+len(ExtraNames)
	// and MinCount stays 1 unless MinCount is set.
	ExtraNames []string
	MinCount   int64
	// TargetShape overrides locationPolicy.targetShape (default ANY).
	TargetShape string
}

// BulkInsertResult is the created VM after the regional operation completes.
type BulkInsertResult struct {
	Name        string
	Zone        string
	MachineType string
	NetworkIP   string
	Instance    *compute.Instance
	RequestID   string
	// InsertResponse is the Operation returned by regionInstances.bulkInsert.
	InsertResponse *compute.Operation
	// FinalOperation is the regional Operation after polling to DONE.
	FinalOperation *compute.Operation
}

// BulkInsertBatchResult contains every requested VM created by one regional
// bulkInsert operation.
type BulkInsertBatchResult struct {
	Instances      map[string]*BulkInsertResult
	Failures       map[string]error
	RequestID      string
	InsertResponse *compute.Operation
	FinalOperation *compute.Operation
}

// BulkInsertError preserves operation diagnostics without returning a
// success-shaped VM result.
type BulkInsertError struct {
	RequestID      string
	InsertResponse *compute.Operation
	FinalOperation *compute.Operation
	Err            error
}

func (e *BulkInsertError) Error() string {
	return e.Err.Error()
}

func (e *BulkInsertError) Unwrap() error {
	return e.Err
}

// BulkInsertOneVM creates exactly one VM via regionInstances.bulkInsert and
// waits on the single regional operation. Zone is taken from operation metadata.
func BulkInsertOneVM(ctx context.Context, svc *compute.Service, p BulkInsertParams) (*BulkInsertResult, error) {
	if len(p.ExtraNames) > 0 || p.MinCount > 1 {
		return nil, errors.New("google: BulkInsertOneVM requires exactly one VM; use BulkInsertVMs for batches")
	}
	batch, err := BulkInsertVMs(ctx, svc, p)
	if batch == nil {
		return nil, err
	}
	if err != nil {
		return nil, &BulkInsertError{
			RequestID:      batch.RequestID,
			InsertResponse: batch.InsertResponse,
			FinalOperation: batch.FinalOperation,
			Err:            err,
		}
	}
	result := batch.Instances[p.Name]
	if result == nil {
		return nil, &BulkInsertError{
			RequestID:      batch.RequestID,
			InsertResponse: batch.InsertResponse,
			FinalOperation: batch.FinalOperation,
			Err:            fmt.Errorf("google: bulkInsert instance %q missing from successful batch result", p.Name),
		}
	}
	return result, nil
}

// BulkInsertVMs creates all names in BulkInsertParams and returns each VM by
// name, including batches distributed across multiple zones.
func BulkInsertVMs(ctx context.Context, svc *compute.Service, p BulkInsertParams) (*BulkInsertBatchResult, error) {
	if svc == nil {
		return nil, errors.New("google: bulkInsert requires a compute service")
	}
	if err := validateBulkInsertParams(p); err != nil {
		return nil, err
	}

	req := buildBulkInsertRequest(p)

	requestID := uuid.New().String()
	insertOp, finalOp, err := submitAndWaitStandaloneBulkInsert(
		ctx, svc, p.Project, p.Region, requestID, req,
	)
	out := &BulkInsertBatchResult{
		Instances:      make(map[string]*BulkInsertResult),
		Failures:       make(map[string]error),
		RequestID:      requestID,
		InsertResponse: insertOp,
		FinalOperation: finalOp,
	}
	if err != nil {
		return out, fmt.Errorf("google: bulkInsert request failed: %w", err)
	}

	if finalOp.Error != nil && len(finalOp.Error.Errors) > 0 {
		e := finalOp.Error.Errors[0]
		return out, fmt.Errorf("google: bulkInsert operation failed code=%s: %s", e.Code, e.Message)
	}

	zones := survivingBulkInsertZones(finalOp)
	if len(zones) == 0 {
		return out, errors.New("google: bulkInsert operation completed with no surviving VM locations")
	}
	var lookupErrors []error
	for _, name := range bulkInsertNames(p) {
		vm, zone, getErr := getBulkInsertInstance(ctx, svc, p.Project, name, zones)
		if getErr != nil {
			lookupErrors = append(lookupErrors, getErr)
			out.Failures[name] = getErr
			continue
		}
		out.Instances[name] = &BulkInsertResult{
			Name:           vm.Name,
			Zone:           zone,
			MachineType:    path.Base(vm.MachineType),
			NetworkIP:      instanceInternalIP(vm),
			Instance:       vm,
			RequestID:      requestID,
			InsertResponse: insertOp,
			FinalOperation: finalOp,
		}
	}
	if int64(len(out.Instances)) >= bulkInsertMinCount(p) {
		return out, nil
	}
	return out, fmt.Errorf(
		"google: bulkInsert resolved %d VMs, below minCount %d: %w",
		len(out.Instances),
		bulkInsertMinCount(p),
		errors.Join(lookupErrors...),
	)
}

func validateBulkInsertParams(p BulkInsertParams) error {
	if p.Project == "" || p.Region == "" || p.Name == "" {
		return errors.New("google: bulkInsert requires project, region, and name")
	}
	if len(p.Zones) == 0 {
		return errors.New("google: bulkInsert requires at least one zone")
	}
	if p.SourceImage == "" {
		return errors.New("google: bulkInsert requires a source image")
	}
	if (p.MachineType == "") == (len(p.Selections) == 0) {
		return errors.New("google: bulkInsert requires exactly one of machine type or selections")
	}

	names := bulkInsertNames(p)
	seenNames := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" {
			return errors.New("google: bulkInsert VM names cannot be empty")
		}
		if _, exists := seenNames[name]; exists {
			return fmt.Errorf("google: duplicate bulkInsert VM name %q", name)
		}
		seenNames[name] = struct{}{}
	}
	if p.MinCount < 0 {
		return errors.New("google: bulkInsert minCount cannot be negative")
	}
	minCount := bulkInsertMinCount(p)
	if minCount > int64(len(names)) {
		return fmt.Errorf("google: bulkInsert minCount %d exceeds count %d", minCount, len(names))
	}

	allowedZones := make(map[string]struct{}, len(p.Zones))
	for _, zone := range p.Zones {
		if regionFromZone(zone) != p.Region {
			return fmt.Errorf("google: bulkInsert zone %q is outside region %q", zone, p.Region)
		}
		if _, exists := allowedZones[zone]; exists {
			return fmt.Errorf("google: duplicate bulkInsert allowed zone %q", zone)
		}
		allowedZones[zone] = struct{}{}
	}
	deniedZones := make(map[string]struct{}, len(p.DenyZones))
	for _, zone := range p.DenyZones {
		if regionFromZone(zone) != p.Region {
			return fmt.Errorf("google: bulkInsert denied zone %q is outside region %q", zone, p.Region)
		}
		if _, allowed := allowedZones[zone]; allowed {
			return fmt.Errorf("google: bulkInsert zone %q cannot be both allowed and denied", zone)
		}
		if _, exists := deniedZones[zone]; exists {
			return fmt.Errorf("google: duplicate bulkInsert denied zone %q", zone)
		}
		deniedZones[zone] = struct{}{}
	}

	selectionNames := make(map[string]struct{}, len(p.Selections))
	for index, selection := range p.Selections {
		name := bulkInsertSelectionName(selection, index)
		if _, exists := selectionNames[name]; exists {
			return fmt.Errorf("google: duplicate selection name %q", name)
		}
		selectionNames[name] = struct{}{}
		if selection.Rank <= 0 {
			return fmt.Errorf("google: bulkInsert selection %q rank must be positive", name)
		}
		if len(selection.MachineTypes) == 0 {
			return fmt.Errorf("google: bulkInsert selection %d requires at least one machine type", index+1)
		}
		for _, machineType := range selection.MachineTypes {
			if machineType == "" {
				return fmt.Errorf("google: bulkInsert selection %q contains an empty machine type", name)
			}
		}
	}
	return nil
}

func bulkInsertNames(p BulkInsertParams) []string {
	return append([]string{p.Name}, p.ExtraNames...)
}

func bulkInsertMinCount(p BulkInsertParams) int64 {
	if p.MinCount > 0 {
		return p.MinCount
	}
	return 1
}

func bulkInsertSelectionName(selection BulkInsertSelection, index int) string {
	if selection.Name != "" {
		return selection.Name
	}
	return fmt.Sprintf("selection-%d", index+1)
}

func survivingBulkInsertZones(op *compute.Operation) []string {
	if op == nil || op.InstancesBulkInsertOperationMetadata == nil {
		return nil
	}
	zones := make([]string, 0, len(op.InstancesBulkInsertOperationMetadata.PerLocationStatus))
	for zoneURL, status := range op.InstancesBulkInsertOperationMetadata.PerLocationStatus {
		if status.CreatedVmCount > status.DeletedVmCount {
			zones = append(zones, path.Base(zoneURL))
		}
	}
	return zones
}

func getBulkInsertInstance(
	ctx context.Context,
	svc *compute.Service,
	project, name string,
	zones []string,
) (*compute.Instance, string, error) {
	for _, zone := range zones {
		vm, err := svc.Instances.Get(project, zone, name).Context(ctx).Do()
		if err == nil {
			return vm, zone, nil
		}
		var googleError *googleapi.Error
		if errors.As(err, &googleError) && googleError.Code == http.StatusNotFound {
			continue
		}
		return nil, "", fmt.Errorf("google: bulkInsert get instance %q in zone %q failed: %w", name, zone, err)
	}
	return nil, "", fmt.Errorf("google: bulkInsert instance %q was not found in surviving zones", name)
}

func submitAndWaitStandaloneBulkInsert(
	ctx context.Context,
	svc *compute.Service,
	project, region, requestID string,
	request *compute.BulkInsertInstanceResource,
) (*compute.Operation, *compute.Operation, error) {
	var ambiguousDeadline time.Time
	var lastInsertOperation *compute.Operation
	for attempt := 0; ; attempt++ {
		if !ambiguousDeadline.IsZero() && time.Now().After(ambiguousDeadline) {
			return lastInsertOperation, nil, errors.New("google: timed out resolving ambiguous bulkInsert submission")
		}
		if attempt > 1 {
			if err := waitForBulkInsertRetry(ctx, retryBackoffSec*time.Second); err != nil {
				return lastInsertOperation, nil, err
			}
		}

		insertOperation, err := svc.RegionInstances.BulkInsert(project, region, request).
			RequestId(requestID).
			Context(ctx).
			Do()
		if err != nil {
			if ctx.Err() != nil {
				return lastInsertOperation, nil, ctx.Err()
			}
			submissionError := &bulkInsertSubmissionError{err: err}
			if isDefinitiveBulkInsertRejection(submissionError) {
				return lastInsertOperation, nil, submissionError
			}
			if ambiguousDeadline.IsZero() {
				ambiguousDeadline = time.Now().Add(defaultBulkInsertReconcileTimeout)
			}
			continue
		}
		if insertOperation == nil || insertOperation.Name == "" {
			if ambiguousDeadline.IsZero() {
				ambiguousDeadline = time.Now().Add(defaultBulkInsertReconcileTimeout)
			}
			continue
		}
		lastInsertOperation = insertOperation

		finalOperation, err := waitRegionOperation(ctx, svc, project, region, insertOperation.Name)
		if err == nil {
			return insertOperation, finalOperation, nil
		}
		if ctx.Err() != nil {
			return lastInsertOperation, nil, ctx.Err()
		}
		if ambiguousDeadline.IsZero() {
			ambiguousDeadline = time.Now().Add(defaultBulkInsertReconcileTimeout)
		}
	}
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
		if sameBulkInsertCandidateGroup(first, candidate) {
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
		if _, exists := seen[candidate.zone]; exists || !sameBulkInsertCandidateGroup(first, candidate) {
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

func sameBulkInsertCandidateGroup(first, candidate createCandidate) bool {
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
		selections[fmt.Sprintf("rank-%d", index+2)] = compute.InstanceFlexibilityPolicyInstanceSelection{
			Rank:         int64(index + 2),
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

func (p *config) insertWithBulkFallback(
	ctx context.Context,
	in *compute.Instance,
	candidates []createCandidate,
	opts *types.InstanceCreateOpts,
	machineType, bootDiskType string,
	stockoutRetryEnabled, usesReservation bool,
	logr logger.Logger,
) (*compute.Operation, createCandidate, error) {
	if opts.DisableMachineTypeFallbacks {
		return p.insertWithStockoutRetry(
			ctx, in, candidates, opts, machineType, bootDiskType,
			stockoutRetryEnabled, usesReservation, logr,
		)
	}
	fallbacks := opts.MachineTypeFallbacks
	if len(fallbacks) == 0 {
		fallbacks = p.machineTypeFallbacks
	}
	if len(fallbacks) > 0 {
		if err := validateMachineTypeFallbacks(fallbacks); err != nil {
			return nil, createCandidate{}, err
		}
	}
	regionalCandidates := p.buildRegionalBulkInsertCandidates(candidates)
	if len(fallbacks) == 0 || !stockoutRetryEnabled || usesReservation || len(regionalCandidates) == 0 {
		return p.insertWithStockoutRetry(
			ctx, in, candidates, opts, machineType, bootDiskType,
			stockoutRetryEnabled, usesReservation, logr,
		)
	}

	first := regionalCandidates[0]
	in.NetworkInterfaces[0].Network = first.network
	in.NetworkInterfaces[0].Subnetwork = first.subnetwork
	in.Tags = &compute.Tags{Items: append(append([]string{}, first.tags...), in.Name)}
	opts.EgressProxyURL = resolveEgressProxyURL(p.egressControl, first.proxyURL)
	userData, err := lehelper.GenerateUserdata(p.userData, opts)
	if err != nil {
		return nil, first, err
	}
	setUserdataMetadata(in, p.userDataKey, userData)

	zones := make([]string, len(regionalCandidates))
	for index := range regionalCandidates {
		zones[index] = regionalCandidates[index].zone
	}
	operation, zone, err := p.insertRegionalBulkInstance(
		ctx, in, machineType, bootDiskType, fallbacks, zones,
	)
	if err == nil {
		succeeded := first
		succeeded.zone = zone
		logr.WithField("zone", zone).Debugln("google: regional bulkInsert provisioned VM")
		return operation, succeeded, nil
	}

	var definitiveError *definitiveBulkInsertError
	if !errors.As(err, &definitiveError) {
		reconciled := p.reconcileAmbiguousBulkInsert(in.Name, regionalCandidates, logr)
		return nil, reconciled, err
	}
	logr.WithError(err).Warnln("google: regional bulkInsert failed; using zonal create path")
	return p.insertWithStockoutRetry(
		ctx, in, candidates, opts, machineType, bootDiskType,
		stockoutRetryEnabled, usesReservation, logr,
	)
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
			var err error
			finalOperation, err = p.submitAndWaitRegionalBulkInsert(
				ctx, region, requestID, request,
			)
			if err != nil {
				if isDefinitiveBulkInsertRejection(err) {
					return &definitiveBulkInsertError{
						err: fmt.Errorf("google: bulkInsert request rejected: %w", err),
					}
				}
				finalOperation, err = p.resolveAmbiguousBulkInsert(
					region, requestID, request,
				)
				if err != nil {
					return fmt.Errorf("google: bulkInsert operation outcome is unknown: %w", err)
				}
			}
			zone, err = completedBulkInsertZone(finalOperation)
			return err
		},
	)
	return finalOperation, zone, err
}

func completedBulkInsertZone(operation *compute.Operation) (string, error) {
	if zone, err := zoneFromBulkInsertMetadata(operation); err == nil {
		return zone, nil
	}
	if operation == nil ||
		operation.InstancesBulkInsertOperationMetadata == nil ||
		len(operation.InstancesBulkInsertOperationMetadata.PerLocationStatus) == 0 {
		return "", errors.New("google: bulkInsert operation completed without placement metadata")
	}
	if operation.Error != nil && len(operation.Error.Errors) > 0 {
		operationError := operation.Error.Errors[0]
		return "", &definitiveBulkInsertError{
			err: fmt.Errorf(
				"google: bulkInsert operation failed code=%s: %s",
				operationError.Code,
				operationError.Message,
			),
		}
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
) createCandidate {
	timeout, pollInterval := p.bulkInsertReconcileSettings()

	// The caller context is commonly the source of the ambiguous outcome. Use a
	// bounded independent context so a cancelled request cannot skip cleanup
	// reconciliation for a VM that GCP may still be creating.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

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
				return candidate
			}
			var googleError *googleapi.Error
			if !errors.As(err, &googleError) || googleError.Code != http.StatusNotFound {
				logr.WithField("zone", candidate.zone).
					WithError(err).
					Warnln("google: failed to reconcile ambiguous regional bulkInsert outcome")
			}
		}

		if waitForBulkInsertRetry(ctx, pollInterval) != nil {
			return createCandidate{}
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

func (p *config) bulkInsertReconcileSettings() (time.Duration, time.Duration) {
	timeout := p.bulkInsertReconcileTimeout
	if timeout <= 0 {
		timeout = defaultBulkInsertReconcileTimeout
	}
	pollInterval := p.bulkInsertReconcilePollInterval
	if pollInterval <= 0 {
		pollInterval = defaultBulkInsertReconcilePollInterval
	}
	return timeout, pollInterval
}

func buildBulkInsertRequest(p BulkInsertParams) *compute.BulkInsertInstanceResource {
	props := &compute.InstanceProperties{
		MinCpuPlatform: "Automatic",
		Disks:          bulkInsertBootDisk(p.SourceImage, p.DiskType, p.DiskSizeGb),
		NetworkInterfaces: []*compute.NetworkInterface{
			{
				Network:    qualifyNetwork(p.Project, p.Network),
				Subnetwork: qualifySubnetwork(p.Project, p.Region, p.Subnetwork),
			},
		},
		Scheduling: &compute.Scheduling{
			Preemptible:      false,
			AutomaticRestart: googleapi.Bool(true),
		},
		Labels: map[string]string{
			"bulk-insert-test": "true",
		},
	}
	if !p.PrivateIP {
		props.NetworkInterfaces[0].AccessConfigs = []*compute.AccessConfig{
			{Name: "External NAT", Type: "ONE_TO_ONE_NAT"},
		}
	}
	if p.MachineType != "" && len(p.Selections) == 0 {
		props.MachineType = p.MachineType
	}

	locations := make(map[string]compute.LocationPolicyLocation, len(p.Zones)+len(p.DenyZones))
	for _, z := range p.Zones {
		locations["zones/"+z] = compute.LocationPolicyLocation{Preference: "ALLOW"}
	}
	for _, z := range p.DenyZones {
		locations["zones/"+z] = compute.LocationPolicyLocation{Preference: "DENY"}
	}
	// Zones omitted from this map are still eligible unless listed in DenyZones.

	names := bulkInsertNames(p)
	perInstance := make(map[string]compute.BulkInsertInstanceResourcePerInstanceProperties, len(names))
	for _, n := range names {
		perInstance[n] = compute.BulkInsertInstanceResourcePerInstanceProperties{}
	}
	targetShape := p.TargetShape
	if targetShape == "" {
		targetShape = "ANY"
	}

	req := &compute.BulkInsertInstanceResource{
		Count:                 int64(len(names)),
		MinCount:              bulkInsertMinCount(p),
		PerInstanceProperties: perInstance,
		InstanceProperties:    props,
		LocationPolicy: &compute.LocationPolicy{
			Locations:   locations,
			TargetShape: targetShape,
		},
	}

	if len(p.Selections) > 0 {
		req.InstanceFlexibilityPolicy = &compute.InstanceFlexibilityPolicy{
			InstanceSelections: make(map[string]compute.InstanceFlexibilityPolicyInstanceSelection, len(p.Selections)),
		}
		for i, sel := range p.Selections {
			name := bulkInsertSelectionName(sel, i)
			req.InstanceFlexibilityPolicy.InstanceSelections[name] = compute.InstanceFlexibilityPolicyInstanceSelection{
				Rank:         sel.Rank,
				MachineTypes: sel.MachineTypes,
				Disks:        sel.Disks,
			}
		}
	}
	return req
}

func bulkInsertBootDisk(sourceImage, diskType string, sizeGb int64) []*compute.AttachedDisk {
	if diskType == "" {
		diskType = "pd-standard"
	}
	if sizeGb <= 0 {
		sizeGb = 10
	}
	return []*compute.AttachedDisk{
		{
			Type:       "PERSISTENT",
			Boot:       true,
			Mode:       "READ_WRITE",
			AutoDelete: true,
			InitializeParams: &compute.AttachedDiskInitializeParams{
				SourceImage: bulkInsertSourceImageURL(sourceImage),
				DiskSizeGb:  sizeGb,
				DiskType:    diskType,
			},
		},
	}
}

func bulkInsertSourceImageURL(image string) string {
	const prefix = "https://www.googleapis.com/compute/v1/"
	if strings.HasPrefix(image, prefix) {
		return image
	}
	if strings.HasPrefix(image, "projects/") {
		return prefix + image
	}
	return prefix + "projects/" + image
}

func qualifyNetwork(project, network string) string {
	if network == "" {
		return fmt.Sprintf("projects/%s/global/networks/default", project)
	}
	if strings.Contains(network, "/") {
		return network
	}
	return fmt.Sprintf("projects/%s/global/networks/%s", project, network)
}

func qualifySubnetwork(project, region, subnetwork string) string {
	if subnetwork == "" {
		return ""
	}
	if strings.Contains(subnetwork, "/") {
		return subnetwork
	}
	return fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s", project, region, subnetwork)
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
		if op.Status == "DONE" {
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

func instanceInternalIP(vm *compute.Instance) string {
	if vm == nil || len(vm.NetworkInterfaces) == 0 {
		return ""
	}
	return vm.NetworkInterfaces[0].NetworkIP
}
