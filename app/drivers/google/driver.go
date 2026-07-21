package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/drone/runner-go/logger"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	itypes "github.com/drone-runners/drone-runner-aws/app/types"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/types"

	"github.com/dchest/uniuri"
	"github.com/google/uuid"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

var _ drivers.Driver = (*config)(nil)

const (
	maxInstanceNameLen  = 63
	randStrLen          = 5
	tagRetries          = 3
	getRetries          = 3
	insertRetries       = 3
	deleteRetries       = 3
	secSleep            = 1
	tagRetrySleepMs     = 1000
	operationGetTimeout = 30
	maxStockoutAttempts = 3

	stockoutCacheTTL = 300 * time.Second
	// stockoutCacheSize bounds the number of remembered (zone, machineType) keys.
	stockoutCacheSize = 1024
)

var (
	// ErrInstanceNotFound is returned when an instance cannot be found in any configured zone.
	// This is a sentinel error that indicates the instance has been deleted or never existed,
	// and is safe to skip during cleanup operations.
	ErrInstanceNotFound = errors.New("google: instance not found in any zone")

	defaultTags = []string{
		"allow-docker",
	}

	defaultScopes = []string{
		"https://www.googleapis.com/auth/devstorage.read_only",
		"https://www.googleapis.com/auth/logging.write",
		"https://www.googleapis.com/auth/monitoring.write",
		"https://www.googleapis.com/auth/trace.append",
	}
)

// networkConfig holds a network/subnetwork/tags/zones combination.
type networkConfig struct {
	network    string
	subnetwork string
	tags       []string
	zones      []string
	proxyURL   string
}

type config struct {
	init sync.Once

	projectID string
	JSONPath  string
	JSON      []byte

	rootDir string

	// vm instance data
	diskSize                   int64
	diskType                   string
	hibernate                  bool
	image                      string
	network                    string
	noServiceAccount           bool
	subnetwork                 string
	privateIP                  bool
	scopes                     []string
	serviceAccountEmail        string
	size                       string
	tags                       []string
	zones                      []string
	userData                   string
	userDataKey                string
	egressControl              bool
	service                    *compute.Service
	labels                     map[string]string
	enableNestedVirtualization bool
	enableC4D                  bool
	gpu                        bool
	networkConfigs             []networkConfig
	networkConfigIndex         uint64

	stockoutCache *expirable.LRU[string, struct{}]
}

// resolveNetworkAndZone determines the final zone, network, subnetwork, and tags for an instance.
// reservationZone is the zone from a capacity reservation (may be empty).
// requestZones is opts.Zones from the request (may be empty).
//
// Zone priority: reservationZone > random zone from requestZones > network config zone > pool RandomZone.
// Network priority: networkConfigs (zone-matched or round-robin) > single network/subnetwork/tags.
func (p *config) resolveNetworkAndZone(reservationZone string, requestZones []string) (zone, network, subnetwork string, tags []string) {
	zone, network, subnetwork, tags, _ = p.resolveNetworkAndZoneWithProxy(reservationZone, requestZones)
	return
}

// resolveNetworkAndZoneWithProxy is resolveNetworkAndZone plus the selected network's proxy_url
// (empty when unset — caller should keep the env fallback on InstanceCreateOpts).
func (p *config) resolveNetworkAndZoneWithProxy(reservationZone string, requestZones []string) (zone, network, subnetwork string, tags []string, proxyURL string) {
	zone = reservationZone

	// Fallback to a random zone from the request so load is spread across zones
	if zone == "" && len(requestZones) > 0 {
		zone = requestZones[rand.Intn(len(requestZones))] //nolint:gosec
	}

	// Select network (zone-matched if zone is known, round-robin otherwise)
	selected := p.selectNetwork(zone)

	// Resolve fully qualified paths; may pick a zone from the network entry
	var resolvedZone string
	network, subnetwork, resolvedZone, tags = selected.resolve(p.projectID, zone, p.GetRegion)
	if zone == "" && resolvedZone != "" {
		zone = resolvedZone
	}
	return zone, network, subnetwork, tags, selected.proxyURL
}

// resolveEgressProxyURL returns the proxy URL to bake into userdata and persist on
// the instance. When egress_control is false the result is always empty so non-egress
// pools never store a proxy_url (even if networks[] or env define one). When egress is
// on, a non-empty network proxy_url wins over the provisioner-stamped env fallback.
func resolveEgressProxyURL(egressControl bool, envFallback, networkProxyURL string) string {
	if !egressControl {
		return ""
	}
	if networkProxyURL != "" {
		return networkProxyURL
	}
	return envFallback
}

// selectNetwork returns the network entry to use for an instance.
//
// When networkConfigs is empty, it falls back to the single network/subnetwork/tags fields.
// When networkConfigs is set:
//   - If zone is known (e.g. from a capacity reservation), pick the first entry whose
//     zones list contains that zone. If none match, fall back to the first entry.
//   - If zone is unknown, pick the next entry via atomic round-robin.
func (p *config) selectNetwork(zone string) *networkConfig {
	if len(p.networkConfigs) == 0 {
		return &networkConfig{
			network:    p.network,
			subnetwork: p.subnetwork,
			tags:       p.tags,
			zones:      p.zones,
		}
	}

	if zone != "" {
		for i := range p.networkConfigs {
			for _, z := range p.networkConfigs[i].zones {
				if z == zone {
					return &p.networkConfigs[i]
				}
			}
		}
		// No entry lists this zone — fall back to first entry (zone-agnostic use)
		return &p.networkConfigs[0]
	}

	// No zone constraint — round-robin
	nc := p.nextNetworkConfig()
	return &nc
}

// nextNetworkConfig picks the next network config using atomic round-robin.
func (p *config) nextNetworkConfig() networkConfig {
	n := uint64(len(p.networkConfigs))
	start := time.Now()
	for {
		current := atomic.LoadUint64(&p.networkConfigIndex)
		next := (current + 1) % n
		if atomic.CompareAndSwapUint64(&p.networkConfigIndex, current, next) {
			return p.networkConfigs[current]
		}
		if time.Since(start) > 10*time.Second {
			return p.networkConfigs[rand.Intn(len(p.networkConfigs))] //nolint:gosec
		}
	}
}

// allZones returns a deduplicated list of zones across all network configs.
// Falls back to p.zones when no network configs are defined.
func (p *config) allZones() []string {
	if len(p.networkConfigs) == 0 {
		return p.zones
	}
	seen := make(map[string]bool)
	var zones []string
	for _, nc := range p.networkConfigs {
		for _, z := range nc.zones {
			if !seen[z] {
				seen[z] = true
				zones = append(zones, z)
			}
		}
	}
	if len(zones) == 0 {
		return p.zones
	}
	return zones
}

// resolve builds fully qualified GCP resource paths and picks a random zone from the entry.
// fallbackZone is used for subnetwork region derivation when the entry has no zones.
func (nc *networkConfig) resolve(projectID, regionZone string, getRegion func(string) string) (network, subnetwork, zone string, tags []string) {
	tags = nc.tags

	if regionZone == "" && len(nc.zones) > 0 {
		zone = nc.zones[rand.Intn(len(nc.zones))] //nolint:gosec
	} else if regionZone != "" {
		zone = regionZone
	}

	if nc.network != "" {
		if strings.LastIndex(nc.network, "/") == -1 {
			network = fmt.Sprintf("projects/%s/global/networks/%s", projectID, nc.network)
		} else {
			network = nc.network
		}
	}

	if nc.subnetwork != "" {
		if strings.LastIndex(nc.subnetwork, "/") == -1 {
			subnetwork = fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s",
				projectID, getRegion(zone), nc.subnetwork)
		} else {
			subnetwork = nc.subnetwork
		}
	}

	return network, subnetwork, zone, tags
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(config)
	for _, opt := range opts {
		opt(p)
	}

	p.stockoutCache = expirable.NewLRU[string, struct{}](stockoutCacheSize, nil, stockoutCacheTTL)

	ctx := context.Background()
	var err error
	if p.service == nil {
		if p.JSONPath != "" {
			p.service, err = compute.NewService(ctx, option.WithCredentialsFile(p.JSONPath)) //nolint:staticcheck // SA1019: pre-existing usage, not changed by this PR
		} else {
			p.service, err = compute.NewService(ctx)
		}

		if err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (p *config) RootDir() string {
	return p.rootDir
}

func (p *config) RandomZone() string {
	return p.zones[rand.Intn(len(p.zones))] //nolint: gosec
}

func (p *config) GetRegion(zone string) string {
	parts := strings.Split(zone, "-")
	return strings.Join(parts[:len(parts)-1], "-")
}

func (p *config) DriverName() string {
	return string(types.Google)
}

func (p *config) InstanceType() string {
	return p.image
}

func (p *config) CanHibernate() bool {
	return p.hibernate
}

func (p *config) Logs(ctx context.Context, instance string) (string, error) {
	zone, err := p.findInstanceZone(ctx, instance)
	if err != nil {
		return "", err
	}

	output, err := p.service.Instances.GetSerialPortOutput(p.projectID, zone, instance).Context(ctx).Do()
	if err != nil {
		return "", err
	}

	if output == nil {
		return "", fmt.Errorf("nil value for serial console output")
	}

	return output.Contents, nil
}

func (p *config) Ping(ctx context.Context) error {
	client := p.service
	response, err := client.Regions.List(p.projectID).Context(ctx).Do()
	if err != nil {
		return err
	}
	if response.ServerResponse.HTTPStatusCode == http.StatusOK {
		return nil
	}
	return errors.New("unable to ping google")
}

// ReserveCapacity reserves capacity for a VM
func (p *config) ReserveCapacity(ctx context.Context, opts *types.InstanceCreateOpts) (*types.CapacityReservation, error) {
	machineType := p.size
	if opts.MachineType != "" {
		machineType = opts.MachineType
	}

	// Generate a unique reservation name
	reservationName := getInstanceName(opts.RunnerName, opts.PoolName)

	// Pick a single zone at random. The capacity reservation timeout is short,
	// so trying multiple zones here would eat into the budget that fallback
	// pools need to take over.
	var candidateZones []string
	if len(p.networkConfigs) > 0 {
		nc := p.nextNetworkConfig()
		candidateZones = nc.zones
	} else {
		candidateZones = p.zones
	}

	logr := logger.FromContext(ctx).
		WithField("cloud", types.Google).
		WithField("reservation", reservationName).
		WithField("machine_type", machineType).
		WithField("pool", opts.PoolName)

	if len(candidateZones) == 0 {
		logr.Errorln("google: no zones configured for capacity reservation")
		return nil, &itypes.ErrCapacityUnavailable{Driver: string(types.Google)}
	}

	zone := candidateZones[rand.Intn(len(candidateZones))] //nolint:gosec
	zoneLogr := logr.WithField("zone", zone)
	zoneLogr.Debugln("google: attempting capacity reservation in zone")

	reservation := &compute.Reservation{
		Name: reservationName,
		Zone: fmt.Sprintf("projects/%s/zones/%s", p.projectID, zone),
		SpecificReservation: &compute.AllocationSpecificSKUReservation{
			Count: 1, // Reserve capacity for 1 VM
			InstanceProperties: &compute.AllocationSpecificSKUAllocationReservedInstanceProperties{
				MachineType: machineType,
			},
		},
		SpecificReservationRequired: true, // Require specific reservation targeting
		Description:                 fmt.Sprintf("Capacity reservation for pool %s", opts.PoolName),
	}

	if opts.CapacityReservationTTL > 0 {
		reservation.DeleteAfterDuration = &compute.Duration{Seconds: opts.CapacityReservationTTL}
		zoneLogr.WithField("delete_after_seconds", opts.CapacityReservationTTL).
			Infoln("google: setting delete after duration on capacity reservation")
	}

	// Bound the entire reservation attempt (submit + wait for it to reach DONE) so
	// a zone stockout (op stuck PENDING) fails fast instead of consuming the caller's
	// full request deadline. Both calls share this budget; whichever deadline is
	// sooner (this cap or the caller's ctx) wins, so it only ever tightens ctx.
	opCtx, cancel := context.WithTimeout(ctx, time.Duration(opts.ReservationPerPoolTimeout)*time.Millisecond)
	defer cancel()

	op, err := p.service.Reservations.Insert(p.projectID, zone, reservation).Context(opCtx).Do()
	if err != nil {
		zoneLogr.WithError(err).Warnln("google: failed to create capacity reservation")
		return nil, &itypes.ErrCapacityUnavailable{Driver: string(types.Google)}
	}

	if err := p.waitZoneOperation(opCtx, op.Name, zone); err != nil {
		zoneLogr.WithError(err).Warnln("google: capacity reservation creation operation failed")
		return nil, &itypes.ErrCapacityUnavailable{Driver: string(types.Google)}
	}

	zoneLogr.Infoln("google: capacity reservation created successfully")
	return &types.CapacityReservation{
		StageID:       "", // Will be set by the caller
		PoolName:      opts.PoolName,
		InstanceID:    "", // Will be set when instance is created
		ReservationID: reservationName,
		CreatedAt:     time.Now().Unix(),
		Zone:          types.StringPtr(zone),
	}, nil
}

// findReservationZone finds the zone where a capacity reservation exists
func (p *config) findReservationZone(ctx context.Context, reservationID string) (zone string, err error) {
	logr := logger.FromContext(ctx)

	for _, z := range p.allZones() {
		_, err := p.service.Reservations.Get(p.projectID, z, reservationID).Context(ctx).Do()
		if err == nil {
			return z, nil
		}
		// If not found in this zone, continue to next zone
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == http.StatusNotFound {
			continue
		}
		// For other errors, log and continue
		logr.WithError(err).Warnf("google: error checking reservation in zone %s", z)
	}

	return "", fmt.Errorf("capacity reservation %s not found in any configured zone", reservationID)
}

// DestroyCapacity destroys capacity for a VM
func (p *config) DestroyCapacity(ctx context.Context, capacity *types.CapacityReservation) (err error) {
	if capacity == nil || capacity.ReservationID == "" {
		return fmt.Errorf("invalid capacity reservation: missing reservation ID")
	}

	logr := logger.FromContext(ctx).
		WithField("cloud", types.Google).
		WithField("reservation", capacity.ReservationID).
		WithField("pool", capacity.PoolName)

	logr.Debugln("google: deleting capacity reservation")

	// Use stored zone, fall back to API lookup
	zone := capacity.GetZone()
	if zone == "" {
		var findErr error
		zone, findErr = p.findReservationZone(ctx, capacity.ReservationID)
		if findErr != nil {
			logr.Warnln("google: capacity reservation not found in any zone")
			return nil
		}
	}

	logr.WithField("zone", zone).Debugln("google: deleting capacity reservation in zone")

	// Delete the reservation
	op, err := p.service.Reservations.Delete(p.projectID, zone, capacity.ReservationID).Context(ctx).Do()
	if err != nil {
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == http.StatusNotFound {
			logr.Warnln("google: capacity reservation already deleted")
			return nil
		}
		logr.WithError(err).Errorln("google: failed to delete capacity reservation")
		return fmt.Errorf("failed to delete capacity reservation: %w", err)
	}

	// Wait for the deletion to complete
	err = p.waitZoneOperation(ctx, op.Name, zone)
	if err != nil {
		logr.WithError(err).Errorln("google: capacity reservation deletion operation failed")
		return fmt.Errorf("capacity reservation deletion operation failed: %w", err)
	}

	logr.Debugln("google: capacity reservation deleted successfully")
	return nil
}

func (p *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	p.init.Do(func() {
		_ = p.setup(ctx)
	})

	var name = getInstanceName(opts.RunnerName, opts.PoolName)
	inst, err := p.create(ctx, opts, name)
	if err != nil {
		defer p.Destroy(context.Background(), []*types.Instance{{ID: name}}) //nolint:errcheck
		return nil, err
	}
	return inst, nil
}

//nolint:gocyclo
func (p *config) create(ctx context.Context, opts *types.InstanceCreateOpts, name string) (instance *types.Instance, err error) {
	// Step 1: Resolve capacity reservation zone (if any)
	var zone string
	if opts.CapacityReservation != nil && opts.CapacityReservation.ReservationID != "" {
		if opts.CapacityReservation.GetZone() != "" {
			// Use stored zone directly
			zone = opts.CapacityReservation.GetZone()
		} else {
			// Fallback: look up zone via API (for reservations created before zone was stored)
			reservationZone, reservationErr := p.findReservationZone(ctx, opts.CapacityReservation.ReservationID)
			if reservationErr != nil {
				logger.FromContext(ctx).
					WithError(reservationErr).
					WithField("reservation", opts.CapacityReservation.ReservationID).
					Warnln("google: capacity reservation lookup failed, proceeding without reservation")
				opts.CapacityReservation = nil
			} else {
				zone = reservationZone
			}
		}
	}

	// Step 2-3: Select network, resolve zone, capture per-network proxy_url
	zone, resolvedNetwork, resolvedSubnetwork, resolvedTags, networkProxyURL := p.resolveNetworkAndZoneWithProxy(zone, opts.Zones)

	machineType := p.size
	if opts.MachineType != "" {
		machineType = opts.MachineType
	}

	// getImage returns the image to use for this instance creation
	image, err := p.GetFullyQualifiedImage(ctx, &opts.VMImageConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get image: %w", err)
	}

	logr := logger.FromContext(ctx).
		WithField("cloud", types.Google).
		WithField("name", name).
		WithField("image", p.InstanceType()).
		WithField("pool", opts.PoolName).
		WithField("zone", zone).
		WithField("image", image).
		WithField("size", machineType)

	// create the instance

	startTime := time.Now()

	logr.Traceln("google: creating VM")

	networkConfig := []*compute.AccessConfig{}

	if !p.privateIP {
		networkConfig = []*compute.AccessConfig{
			{
				Name: "External NAT",
				Type: "ONE_TO_ONE_NAT",
			},
		}
	}

	enableNestedVirtualization := opts.NestedVirtualization
	if !enableNestedVirtualization && opts.Platform.OS == oshelp.OSLinux && opts.Platform.Arch == oshelp.ArchAMD64 {
		enableNestedVirtualization = p.enableNestedVirtualization
	}
	advancedMachineFeatures := &compute.AdvancedMachineFeatures{
		EnableNestedVirtualization: enableNestedVirtualization,
	}

	gpu := opts.GPU
	opts.EnableC4D = p.enableC4D
	opts.EgressControl = p.egressControl
	opts.EgressProxyURL = resolveEgressProxyURL(p.egressControl, opts.EgressProxyURL, networkProxyURL)

	userData, err := lehelper.GenerateUserdata(p.userData, opts)
	if err != nil {
		logr.WithError(err).
			Errorln("google: failed to generate user data")
		return nil, err
	}

	bootDiskSize := p.diskSize
	if opts.StorageOpts.BootDiskSize != "" {
		diskSize, diskSizeErr := strconv.ParseInt(opts.StorageOpts.BootDiskSize, 10, 64)
		if diskSizeErr != nil {
			logr.WithError(err).
				Errorln("google: failed to convert boot disk size string to int64")
			return nil, err
		}
		bootDiskSize = diskSize
	}
	bootDiskType := p.diskType
	if opts.StorageOpts.BootDiskType != "" {
		bootDiskType = opts.StorageOpts.BootDiskType
	}

	usesPersistentDisk := opts.StorageOpts.Identifier != ""
	usesReservation := opts.CapacityReservation != nil && opts.CapacityReservation.ReservationID != ""
	stockoutRetryEnabled := !usesPersistentDisk && !usesReservation

	candidates := []createCandidate{{
		zone:       zone,
		network:    resolvedNetwork,
		subnetwork: resolvedSubnetwork,
		tags:       resolvedTags,
	}}
	if stockoutRetryEnabled {
		candidates = p.buildCreateCandidates(candidates[0], machineType)
		p.logStockoutDeprioritization(logr, candidates, machineType)
	}

	// Build the zone-independent instance spec once. Per-attempt fields (zone,
	// machine type, disk type, network, tags) are set inside the retry loop.
	in := &compute.Instance{
		Name:           name,
		MinCpuPlatform: "Automatic",
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				{Key: p.userDataKey, Value: googleapi.String(userData)},
				{Key: "harness-account-id", Value: googleapi.String(opts.AccountID)},
				{Key: "harness-stage-execution-id", Value: googleapi.String(opts.StageRuntimeID)},
				{Key: "harness-pipeline-execution-id", Value: googleapi.String(opts.PipelineExecutionID)},
				{Key: "harness-pool-name", Value: googleapi.String(opts.PoolName)},
				{Key: "harness-runner-name", Value: googleapi.String(opts.RunnerName)},
				{Key: "harness-resource-class", Value: googleapi.String(opts.ResourceClass)},
				{Key: "harness-platform-os", Value: googleapi.String(opts.Platform.OS)},
				{Key: "harness-platform-arch", Value: googleapi.String(opts.Platform.Arch)},
			},
		},
		Disks: []*compute.AttachedDisk{
			{
				Type:       "PERSISTENT",
				Boot:       true,
				Mode:       "READ_WRITE",
				AutoDelete: true,
				DeviceName: opts.PoolName,
				InitializeParams: &compute.AttachedDiskInitializeParams{
					SourceImage: fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s", image),
					DiskSizeGb:  bootDiskSize,
				},
			},
		},
		AdvancedMachineFeatures: advancedMachineFeatures,
		CanIpForward:            false,
		NetworkInterfaces:       []*compute.NetworkInterface{{AccessConfigs: networkConfig}},
		Scheduling: &compute.Scheduling{
			Preemptible:       false,
			OnHostMaintenance: onHostMaintenance(gpu),
			AutomaticRestart:  googleapi.Bool(true),
		},
		DeletionProtection: false,
		Labels:             p.buildLabelsWithGitspace(opts),
	}

	// Add BYOI metadata for custom images
	if isByoiImage(image) {
		logr.Debugln("google: adding BYOI metadata items for custom image")
		in.Metadata.Items = append(in.Metadata.Items,
			&compute.MetadataItems{Key: "harness-byoi", Value: googleapi.String("true")})
	}

	if !p.noServiceAccount {
		in.ServiceAccounts = []*compute.ServiceAccount{{Scopes: p.scopes, Email: p.serviceAccountEmail}}
	}

	// Set reservation affinity if capacity reservation is provided
	if opts.CapacityReservation != nil && opts.CapacityReservation.ReservationID != "" {
		logr.WithField("reservation", opts.CapacityReservation.ReservationID).Debugln("google: using capacity reservation")
		in.ReservationAffinity = &compute.ReservationAffinity{
			ConsumeReservationType: "SPECIFIC_RESERVATION",
			Key:                    "compute.googleapis.com/reservation-name",
			Values:                 []string{opts.CapacityReservation.ReservationID},
		}
	}

	op, succeeded, err := p.insertWithStockoutRetry(ctx, in, candidates, opts, machineType, bootDiskType, stockoutRetryEnabled, usesReservation, logr)
	if err != nil {
		return nil, err
	}
	zone = succeeded.zone
	resolvedNetwork = succeeded.network

	// Reflect the zone that actually succeeded in subsequent log lines.
	logr = logr.WithField("zone", zone)

	logr.Debugln("instance insert operation completed")

	logr.
		WithField("ip", op.Id).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("google: [provision] VM provisioned")

	vm, err := p.getInstance(ctx, p.projectID, zone, name)
	if err != nil {
		logr.WithError(err).Errorln("google: failed to get VM")
		return nil, err
	}

	instanceMap, err := p.mapToInstance(vm, zone, opts, enableNestedVirtualization, gpu, image, machineType, resolvedNetwork)
	if err != nil {
		logr.WithError(err).Errorln("google: failed to map VM to instance")
		return nil, err
	}
	logr.
		WithField("ip", instanceMap.Address).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("google: [provision] complete")

	return &instanceMap, nil
}

// insertWithStockoutRetry creates the instance by walking the ordered candidates,
// applying each candidate's zone/network onto the shared spec. On a stockout it
// records the zone (unless a reservation was used) and, when retries are enabled,
// advances to the next candidate. It returns the successful operation and the
// candidate that provisioned the VM.
func (p *config) insertWithStockoutRetry(
	ctx context.Context,
	in *compute.Instance,
	candidates []createCandidate,
	opts *types.InstanceCreateOpts,
	machineType, bootDiskType string,
	stockoutRetryEnabled, usesReservation bool,
	logr logger.Logger,
) (*compute.Operation, createCandidate, error) {
	for attempt := 0; attempt < len(candidates); attempt++ {
		cand := candidates[attempt]
		zone := cand.zone
		attemptLogr := logr.WithField("zone", zone).WithField("attempt", attempt+1)

		// Apply the per-attempt zone/network selection onto the shared spec.
		in.Zone = fmt.Sprintf("projects/%s/zones/%s", p.projectID, zone)
		in.MachineType = fmt.Sprintf("projects/%s/zones/%s/machineTypes/%s", p.projectID, zone, machineType)
		in.Disks[0].InitializeParams.DiskType = fmt.Sprintf("projects/%s/zones/%s/diskTypes/%s", p.projectID, zone, bootDiskType)
		in.NetworkInterfaces[0].Network = cand.network
		in.NetworkInterfaces[0].Subnetwork = cand.subnetwork
		// Copy tags so appending name does not mutate the network config's backing slice.
		in.Tags = &compute.Tags{Items: append(append([]string{}, cand.tags...), in.Name)}

		if opts.StorageOpts.Identifier != "" {
			operations, attachDiskErr := p.attachPersistentDisk(ctx, opts, in, zone)
			if attachDiskErr != nil {
				attemptLogr.WithError(attachDiskErr).Errorln("google: failed to attach persistent disk")
				return nil, cand, attachDiskErr
			}
			for _, operation := range operations {
				if operation != nil {
					// Disk not present, wait for creation
					if diskErr := p.waitZoneOperation(ctx, operation.Name, zone); diskErr != nil {
						attemptLogr.WithError(diskErr).Errorln("google: persistent disk creation operation failed")
						return nil, cand, diskErr
					}
				}
			}
		}

		op, attemptErr := p.insertInstance(ctx, p.projectID, zone, uuid.New().String(), in)
		if attemptErr == nil {
			attemptErr = p.waitZoneOperation(ctx, op.Name, zone)
		}
		if attemptErr == nil {
			return op, cand, nil
		}

		// Retry alternate candidates only on stockout/capacity errors. All other
		// failures fail fast so pool fallback in HandleSetup can take over.
		if isStockoutError(attemptErr) {
			attemptLogr.WithError(attemptErr).
				WithField("machine_type", machineType).
				Warnln("google: stockout detected for zone")
			if !usesReservation {
				p.markStockout(zone, machineType)
			}
			if stockoutRetryEnabled && attempt < len(candidates)-1 {
				attemptLogr.WithError(attemptErr).Warnln("google: zone stockout, retrying alternate zone/network candidate")
				p.cleanupFailedInstance(ctx, zone, in.Name, attemptLogr)
				continue
			}
		}
		attemptLogr.WithError(attemptErr).Errorln("google: failed to provision VM")
		return nil, cand, attemptErr
	}
	// Unreachable in practice: candidates always holds at least the first selection.
	return nil, createCandidate{}, errors.New("google: no create candidates available")
}

func (p *config) cleanupFailedInstance(ctx context.Context, zone, name string, logr logger.Logger) {
	op, err := p.deleteInstance(ctx, p.projectID, zone, name, uuid.New().String())
	if err != nil {
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == http.StatusNotFound {
			return
		}
		logr.WithError(err).Warnln("google: failed to delete stocked-out instance before retry")
		return
	}
	if op != nil {
		if werr := p.waitZoneOperation(ctx, op.Name, zone); werr != nil {
			logr.WithError(werr).Warnln("google: delete of stocked-out instance did not complete before retry")
		}
	}
}

func (p *config) attachPersistentDisk(
	ctx context.Context,
	opts *types.InstanceCreateOpts,
	in *compute.Instance,
	diskZone string,
) ([]*compute.Operation, error) {
	storageIdentifiers := strings.Split(opts.StorageOpts.Identifier, ",")
	var operations []*compute.Operation
	for i, diskName := range storageIdentifiers {
		requestID := uuid.New().String()
		diskType := fmt.Sprintf("projects/%s/zones/%s/diskTypes/%s", p.projectID, diskZone, opts.StorageOpts.Type)
		diskSize, err := strconv.ParseInt(opts.StorageOpts.Size, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("error converting string to int64: %v", err)
		}
		persistentDisk := &compute.Disk{
			Name:   diskName,
			SizeGb: diskSize,
			Type:   diskType,
			Zone:   diskZone,
		}
		op, err := p.createPersistentDiskIfNotExists(ctx, p.projectID, diskZone, requestID, persistentDisk)
		if err != nil {
			return nil, err
		}
		if op != nil {
			// this means we have submitted disk creation request(s)
			operations = append(operations, op)
		}

		// attach to instance
		attachedDisk := &compute.AttachedDisk{
			DeviceName: fmt.Sprintf("disk-%d", i),
			Boot:       false,
			Type:       "PERSISTENT",
			Source:     "projects/" + p.projectID + "/zones/" + diskZone + "/disks/" + diskName,
			Mode:       "READ_WRITE",
		}
		in.Disks = append(in.Disks, attachedDisk)
	}
	return operations, nil
}

// Set the instance metadata (not network tags)
func (p *config) SetTags(ctx context.Context, instance *types.Instance, tags map[string]string) error {
	logr := logger.FromContext(ctx).
		WithField("id", instance.ID).
		WithField("cloud", types.Google)
	var err error
	for i := 0; i < tagRetries; i++ {
		err = p.setTags(ctx, instance, tags, logr)
		if err == nil {
			return nil
		}

		logr.WithError(err).Warnln("failed to set tags to the instance. retrying")
		time.Sleep(tagRetrySleepMs * time.Millisecond)
	}
	return err
}

func (p *config) setTags(ctx context.Context, instance *types.Instance,
	tags map[string]string, logr logger.Logger) error {
	vm, err := p.service.Instances.Get(p.projectID, instance.Zone,
		instance.ID).Context(ctx).Do()
	if err != nil {
		logr.WithError(err).Errorln("google: failed to get VM")
		return err
	}

	metadata := &compute.Metadata{
		Fingerprint: vm.Metadata.Fingerprint,
		Items:       vm.Metadata.Items,
	}
	for key, val := range tags {
		metadata.Items = append(metadata.Items, &compute.MetadataItems{
			Key:   key,
			Value: googleapi.String(val),
		})
	}
	_, err = p.service.Instances.SetMetadata(p.projectID, instance.Zone,
		instance.ID, metadata).Context(ctx).Do()
	return err
}

// SetLabels overlays the supplied labels onto the GCP VM's existing labels
// using Instances.setLabels. Existing label keys not present in `labels` are
// preserved; matching keys are overwritten. Retries on conflicting label
// fingerprint (concurrent label change races).
func (p *config) SetLabels(ctx context.Context, instance *types.Instance, labels map[string]string) error {
	if len(labels) == 0 {
		return nil
	}
	logr := logger.FromContext(ctx).
		WithField("id", instance.ID).
		WithField("cloud", types.Google)
	var err error
	for i := 0; i < tagRetries; i++ {
		err = p.setLabels(ctx, instance, labels, logr)
		if err == nil {
			return nil
		}
		logr.WithError(err).Warnln("failed to set labels on the instance. retrying")
		time.Sleep(tagRetrySleepMs * time.Millisecond)
	}
	return err
}

func (p *config) setLabels(ctx context.Context, instance *types.Instance,
	labels map[string]string, logr logger.Logger) error {
	vm, err := p.service.Instances.Get(p.projectID, instance.Zone, instance.ID).Context(ctx).Do()
	if err != nil {
		logr.WithError(err).Errorln("google: failed to get VM")
		return err
	}
	merged := make(map[string]string, len(vm.Labels)+len(labels))
	for k, v := range vm.Labels {
		merged[k] = v
	}
	for k, v := range labels {
		merged[k] = v
	}
	req := &compute.InstancesSetLabelsRequest{
		LabelFingerprint: vm.LabelFingerprint,
		Labels:           merged,
	}
	_, err = p.service.Instances.SetLabels(p.projectID, instance.Zone, instance.ID, req).Context(ctx).Do()
	return err
}

func (p *config) Destroy(ctx context.Context, instances []*types.Instance) ([]*types.Instance, error) {
	return p.DestroyInstanceAndStorage(ctx, instances, nil)
}

func (p *config) DestroyInstanceAndStorage(ctx context.Context, instances []*types.Instance, storageCleanupType *storage.CleanupType) ([]*types.Instance, error) {
	if len(instances) == 0 {
		return nil, errors.New("no instances provided")
	}

	var failedInstances []*types.Instance
	var lastErr error

	for _, instance := range instances {
		logr := logger.FromContext(ctx).
			WithField("id", instance.ID).
			WithField("cloud", types.Google)

		// Track per-instance failure
		var instanceFailed bool
		var instanceErr error

		zone, getZoneErr := p.getZone(ctx, instance)
		if getZoneErr != nil {
			// Instance not found is OK - it's already deleted, safe to skip
			if errors.Is(getZoneErr, ErrInstanceNotFound) {
				logr.Warnln("google: instance not found, skipping deletion")
				continue
			}
			// For other errors (rate limit, API errors), track for retry
			logr.WithError(getZoneErr).Errorln("google: failed to find instance zone")
			failedInstances = append(failedInstances, instance)
			lastErr = getZoneErr
			continue
		}

		var instanceDeleteOperation *compute.Operation
		if zone != "" {
			var deleteInstanceErr error
			instanceDeleteOperation, deleteInstanceErr = p.deleteInstance(ctx, p.projectID, zone, instance.ID, uuid.New().String())
			if deleteInstanceErr != nil {
				// https://github.com/googleapis/google-api-go-client/blob/master/googleapi/googleapi.go#L135
				if gerr, ok := deleteInstanceErr.(*googleapi.Error); ok &&
					gerr.Code == http.StatusNotFound {
					logr.WithError(deleteInstanceErr).Warnln("google: VM not found")
				} else {
					logr.WithError(deleteInstanceErr).Errorln("google: failed to delete the VM")
					instanceFailed = true
					instanceErr = deleteInstanceErr
				}
			}
			logr.Info("google: sent delete instance request")
		}

		if storageCleanupType != nil && *storageCleanupType != "" {
			if instanceDeleteOperation != nil {
				logr.Info("google: waiting for instance deletion")
				waitErr := p.waitZoneOperation(ctx, instanceDeleteOperation.Name, zone)
				if waitErr != nil {
					logr.WithError(waitErr).Errorln("google: could not delete instance. skipping disk deletion")
					instanceFailed = true
					instanceErr = waitErr
				}
			}

			// Only attempt disk deletion if instance deletion succeeded
			if !instanceFailed && *storageCleanupType == storage.Delete && instance.StorageIdentifier != "" {
				logr.Info("google: deleting persistent disk")
				storageIdentifiers := strings.Split(instance.StorageIdentifier, ",")
				for _, storageIdentifier := range storageIdentifiers {
					diskDeleteOperation, diskDeletionErr := p.deletePersistentDisk(
						ctx,
						p.projectID,
						zone,
						storageIdentifier,
						uuid.New().String(),
					)
					if diskDeletionErr != nil {
						var googleErr *googleapi.Error
						if errors.As(diskDeletionErr, &googleErr) &&
							googleErr.Code == http.StatusNotFound {
							logr.WithError(diskDeletionErr).
								Warnln("google: persistent disk %s not found", storageIdentifier)
						} else {
							logr.WithError(diskDeletionErr).
								Errorln("google: error deleting persistent disk %s", storageIdentifier)
							instanceFailed = true
							instanceErr = diskDeletionErr
							break
						}
					} else {
						waitErr := p.waitZoneOperation(ctx, diskDeleteOperation.Name, zone)
						if waitErr != nil {
							logr.WithError(waitErr).Errorln("google: could not delete persistent disk %s", storageIdentifier)
							instanceFailed = true
							instanceErr = waitErr
							break
						}
					}
				}
			}
		}

		// Track failed instance, but continue processing other instances
		if instanceFailed {
			failedInstances = append(failedInstances, instance)
			lastErr = instanceErr
		}
	}

	// Return failed instances so callers can handle them appropriately
	if len(failedInstances) > 0 {
		failedIDs := make([]string, len(failedInstances))
		for i, inst := range failedInstances {
			failedIDs[i] = inst.ID
		}
		return failedInstances, fmt.Errorf("google: failed to delete %d instance(s): %v: %w",
			len(failedInstances), failedIDs, lastErr)
	}

	return nil, nil
}

func (p *config) Hibernate(ctx context.Context, instanceID, _, zone string) error {
	logr := logger.FromContext(ctx).
		WithField("id", instanceID).
		WithField("cloud", types.Google)

	var err error
	if zone == "" {
		zone, err = p.findInstanceZone(ctx, instanceID)
		if err != nil {
			return err
		}
	}

	op, err := p.suspendInstance(ctx, p.projectID, zone, instanceID)
	if err != nil {
		logr.WithError(err).Errorln("google: failed to suspend VM")
		return err
	}

	err = p.waitZoneOperation(ctx, op.Name, zone)
	if err != nil {
		logr.WithError(err).Errorln("instance suspend operation failed")
		return err
	}
	return nil
}

func (p *config) Start(ctx context.Context, instance *types.Instance, _ string) (string, error) {
	logr := logger.FromContext(ctx).
		WithField("id", instance.ID).
		WithField("cloud", types.Google)

	zone := instance.Zone
	var err error
	if zone == "" {
		zone, err = p.findInstanceZone(ctx, instance.ID)
		if err != nil {
			return "", err
		}
	}

	vm, err := p.getInstance(ctx, p.projectID, zone, instance.ID)
	if err != nil {
		return "", err
	}
	if vm.Status != "SUSPENDED" {
		return p.getInstanceIP(vm), nil
	}

	op, err := p.resumeInstance(ctx, p.projectID, zone, instance.ID)
	if err != nil {
		logr.WithError(err).Errorln("google: failed to suspend VM")
		return "", err
	}

	err = p.waitZoneOperation(ctx, op.Name, zone)
	if err != nil {
		logr.WithError(err).Errorln("google: instance suspend operation failed")
		return "", err
	}

	vm, err = p.getInstance(ctx, p.projectID, zone, instance.ID)
	if err != nil {
		logr.WithError(err).Errorln("google: failed to retrieve instance data")
		return "", err
	}
	return p.getInstanceIP(vm), nil
}

func (p *config) getInstance(ctx context.Context, projectID, zone, name string) (*compute.Instance, error) {
	return retry(ctx, getRetries, secSleep, func() (*compute.Instance, error) {
		return p.service.Instances.Get(projectID, zone, name).Context(ctx).Do()
	})
}

func (p *config) getInstanceIP(i *compute.Instance) string {
	instanceIP := ""
	network := i.NetworkInterfaces[0]
	if p.privateIP {
		instanceIP = network.NetworkIP
	} else if len(network.AccessConfigs) > 0 {
		instanceIP = network.AccessConfigs[0].NatIP
	}
	return instanceIP
}

func (p *config) suspendInstance(ctx context.Context, projectID, zone, name string) (*compute.Operation, error) {
	return retry(ctx, getRetries, secSleep, func() (*compute.Operation, error) {
		return p.service.Instances.Suspend(projectID, zone, name).Context(ctx).Do()
	})
}

func (p *config) resumeInstance(ctx context.Context, projectID, zone, name string) (*compute.Operation, error) {
	return retry(ctx, getRetries, secSleep, func() (*compute.Operation, error) {
		return p.service.Instances.Resume(projectID, zone, name).Context(ctx).Do()
	})
}

func (p *config) insertInstance(ctx context.Context, projectID, zone, requestID string, in *compute.Instance) (*compute.Operation, error) {
	return retry(ctx, insertRetries, secSleep, func() (*compute.Operation, error) {
		return p.service.Instances.Insert(projectID, zone, in).RequestId(requestID).Context(ctx).Do()
	})
}

func (p *config) deleteInstance(ctx context.Context, projectID, zone, instanceID, requestID string) (*compute.Operation, error) {
	return retry(ctx, deleteRetries, secSleep, func() (*compute.Operation, error) {
		return p.service.Instances.Delete(projectID, zone, instanceID).RequestId(requestID).Context(ctx).Do()
	})
}

func (p *config) createPersistentDiskIfNotExists(ctx context.Context, projectID, zone, requestID string, disk *compute.Disk) (*compute.Operation, error) {
	// Check if the disk already exists
	_, err := retry(ctx, getRetries, secSleep, func() (*compute.Disk, error) {
		return p.service.Disks.Get(projectID, zone, disk.Name).Context(ctx).Do()
	})

	var getErr *googleapi.Error
	if errors.As(err, &getErr) && getErr.Code == 404 {
		// Disk doesn't exist, create it
		return retry(ctx, insertRetries, secSleep, func() (*compute.Operation, error) {
			return p.service.Disks.Insert(projectID, zone, disk).RequestId(requestID).Context(ctx).Do()
		})
	} else if err != nil {
		return nil, fmt.Errorf("failed to check disk existence: %w", err)
	}

	// Disk already exists
	return nil, nil
}

func (p *config) deletePersistentDisk(ctx context.Context, projectID, zone, diskName, requestID string) (*compute.Operation, error) {
	return retry(ctx, deleteRetries, secSleep, func() (*compute.Operation, error) {
		return p.service.Disks.Delete(projectID, zone, diskName).RequestId(requestID).Context(ctx).Do()
	})
}

func (p *config) mapToInstance(
	vm *compute.Instance, zone string, opts *types.InstanceCreateOpts,
	enableNestedVitualization, gpu bool, image, machineType, resolvedNetwork string,
) (types.Instance, error) {
	network := vm.NetworkInterfaces[0]
	instanceIP := ""
	if p.privateIP {
		instanceIP = network.NetworkIP
	} else {
		instanceIP = network.AccessConfigs[0].NatIP
	}

	labelsBytes, marshalErr := json.Marshal(opts.InternalLabels)
	if marshalErr != nil {
		return types.Instance{}, fmt.Errorf("scheduler: could not marshal labels: %v, err: %w", opts.InternalLabels, marshalErr)
	}

	started, _ := time.Parse(time.RFC3339, vm.CreationTimestamp)
	gitspacePortMappings := make(map[int]int)
	for _, port := range opts.GitspaceOpts.Ports {
		gitspacePortMappings[port] = port
	}
	return types.Instance{
		ID:                         strconv.FormatUint(vm.Id, 10),
		Name:                       vm.Name,
		Provider:                   types.Google, // this is driver, though its the old legacy name of provider
		State:                      types.StateProvisioning,
		Pool:                       opts.PoolName,
		Image:                      image,
		Zone:                       zone,
		Size:                       machineType,
		Platform:                   opts.Platform,
		Address:                    instanceIP,
		CACert:                     opts.CACert,
		CAKey:                      opts.CAKey,
		TLSCert:                    opts.TLSCert,
		TLSKey:                     opts.TLSKey,
		Started:                    started.Unix(),
		Updated:                    time.Now().Unix(),
		IsHibernated:               false,
		Port:                       lehelper.LiteEnginePort,
		EnableNestedVirtualization: enableNestedVitualization,
		GPU:                        gpu,
		StorageIdentifier:          opts.StorageOpts.Identifier,
		Labels:                     labelsBytes,
		GitspacePortMappings:       gitspacePortMappings,
		Network:                    resolvedNetwork,
		ProxyURL:                   opts.EgressProxyURL,
	}, nil
}

func (p *config) findInstanceZone(ctx context.Context, instanceID string) (
	string, error) {
	var lastErr error
	allNotFound := true

	for _, zone := range p.allZones() {
		_, err := p.getInstance(ctx, p.projectID, zone, instanceID)
		if err == nil {
			return zone, nil
		}

		if gerr, ok := err.(*googleapi.Error); ok &&
			gerr.Code == http.StatusNotFound {
			continue
		}

		// Non-404 error (rate limit, API error, etc.) - track it
		allNotFound = false
		lastErr = err
		logger.FromContext(ctx).
			WithField("instance", instanceID).
			WithField("zone", zone).
			WithError(err).
			Errorln("google: failed to fetch the VM")
	}

	// If all zones returned 404, the instance doesn't exist
	if allNotFound {
		return "", ErrInstanceNotFound
	}

	// At least one zone had a non-404 error - return it for retry
	return "", fmt.Errorf("failed to find instance zone due to API error: %w", lastErr)
}

func (p *config) waitZoneOperation(ctx context.Context, name, zone string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		ctxGcp, cancel := context.WithTimeout(ctx, operationGetTimeout*time.Second)
		client := p.service
		op, err := client.ZoneOperations.Get(p.projectID, zone, name).Context(ctxGcp).Do()
		cancel()
		if err != nil {
			if gerr, ok := err.(*googleapi.Error); ok &&
				gerr.Code == http.StatusNotFound {
				return errors.New("not Found")
			}
			if shouldRetry(err) {
				logger.FromContext(ctx).
					WithField("name", name).
					WithField("zone", zone).
					Warnf("google: wait operation failed with retryable error: %s. retrying\n", err)
				time.Sleep(time.Second)
				continue
			}
			return err
		}
		if op.Error != nil {
			return errors.New(op.Error.Errors[0].Message)
		}
		if op.Status == "DONE" {
			return nil
		}
		time.Sleep(time.Second)
	}
}

func (p *config) setup(ctx context.Context) error {
	if reflect.DeepEqual(p.tags, defaultTags) && len(p.networkConfigs) == 0 {
		return p.setupFirewall(ctx)
	}
	return nil
}

func (p *config) setupFirewall(ctx context.Context) error {
	logr := logger.FromContext(ctx)

	logr.Debugln("finding default firewall rules")

	_, err := p.service.Firewalls.Get(p.projectID, "default-allow-docker").Context(ctx).Do()
	if err == nil {
		logr.Debugln("found default firewall rule")
		return nil
	}

	rule := &compute.Firewall{
		Allowed: []*compute.FirewallAllowed{
			{
				IPProtocol: "tcp",
				Ports:      []string{"2376", fmt.Sprint(lehelper.LiteEnginePort)},
			},
		},
		Direction:    "INGRESS",
		Name:         "default-allow-docker",
		Network:      p.network,
		Priority:     1000,
		SourceRanges: []string{"0.0.0.0/0"},
		TargetTags:   []string{"allow-docker"},
	}

	op, err := p.service.Firewalls.Insert(p.projectID, rule).Context(ctx).Do()
	if err != nil {
		logr.WithError(err).
			Errorln("cannot create firewall operation")
		return err
	}

	err = p.waitGlobalOperation(ctx, op.Name)
	if err != nil {
		logr.WithError(err).
			Errorln("cannot create firewall rule")
	}

	return err
}

func (p *config) waitGlobalOperation(ctx context.Context, name string) error {
	for {
		op, err := p.service.GlobalOperations.Get(p.projectID, name).Context(ctx).Do()
		if err != nil {
			if shouldRetry(err) {
				time.Sleep(time.Second)
				continue
			}
			return err
		}
		if op.Error != nil {
			return errors.New(op.Error.Errors[0].Message)
		}
		if op.Status == "DONE" {
			return nil
		}
		time.Sleep(time.Second)
	}
}

func (p *config) getZone(ctx context.Context, instance *types.Instance) (string, error) {
	if instance.Zone == "" {
		zone, findInstanceZoneErr := p.findInstanceZone(ctx, instance.ID)
		if findInstanceZoneErr != nil {
			// Preserve ErrInstanceNotFound sentinel for caller to handle
			if errors.Is(findInstanceZoneErr, ErrInstanceNotFound) {
				return "", ErrInstanceNotFound
			}
			return "", fmt.Errorf("google: failed to find instance in all zones: %w", findInstanceZoneErr)
		}
		return zone, nil
	}

	// validate if instance is present
	_, findInstanceErr := p.getInstance(ctx, p.projectID, instance.Zone, instance.ID)
	if findInstanceErr != nil {
		var googleErr *googleapi.Error
		if errors.As(findInstanceErr, &googleErr) && googleErr.Code == http.StatusNotFound {
			return "", ErrInstanceNotFound
		}
		return "", fmt.Errorf(
			"google: failed to find instance in zone %s, error: %w",
			instance.Zone,
			findInstanceErr,
		)
	}
	return instance.Zone, nil
}

// getImage returns the appropriate image path based on the provided options
// If no image is specified in the options, it returns the default image from p.image
func (p *config) GetFullyQualifiedImage(ctx context.Context, config *types.VMImageConfig) (string, error) {
	// If no image name is provided, return the default image
	if config.ImageName == "" {
		return normalizeImagePath(p.image), nil
	}

	// config.ImageName can be of different formats.
	// we can receive image in following 2 formats:
	// Format #1: harness/vmimage: hosted-vm-ubuntu-2204-jammy-v20250508
	// Format #2: projects/debian-cloud/global/images/debian-11-bullseye-v2025070
	//            OR: debian-cloud/global/images/debian-11-bullseye-v2025070
	// isFullImagePath() method checks if given image in config.ImageName is of Format #2 which can be
	// directly used, else we convert Format #1 to Format #2 in buildImagePathFromTag() method.
	if isFullImagePath(config.ImageName) {
		// Normalize to 4-segment format (strip "projects/" prefix if present)
		// This prevents double "projects/" in SourceImage URL
		return normalizeImagePath(config.ImageName), nil
	}

	return buildImagePathFromTag(config.ImageName, p.projectID), nil
}

// instance name must be 1-63 characters long and match the regular expression
// [a-z]([-a-z0-9]*[a-z0-9])?
func getInstanceName(runner, pool string) string {
	namePrefix := strings.ReplaceAll(runner, " ", "")
	randStr, _ := randStringRunes(randStrLen)
	name := strings.ToLower(fmt.Sprintf("%s-%s-%s-%s", namePrefix, pool, uniuri.NewLen(8), randStr)) //nolint:mnd
	trimmedName := substrSuffix(name, maxInstanceNameLen)
	if trimmedName[0] == '-' {
		trimmedName = "d" + trimmedName[1:]
	}
	return trimmedName
}

var stockoutMarkers = []string{
	"ZONE_RESOURCE_POOL_EXHAUSTED",
	"POOL_CAPACITY_INSUFFICIENT",
	"does not have enough resources available",
	"STOCKOUT",
}

func isStockoutError(err error) bool {
	if err == nil {
		return false
	}
	candidates := []string{err.Error()}
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		for _, e := range gerr.Errors {
			candidates = append(candidates, e.Reason, e.Message)
		}
	}
	for _, s := range candidates {
		for _, marker := range stockoutMarkers {
			if strings.Contains(s, marker) {
				return true
			}
		}
	}
	return false
}

// createCandidate holds the resolved zone and network details for a single VM create attempt.
type createCandidate struct {
	zone       string
	network    string
	subnetwork string
	tags       []string
}

// stockoutKey is the cache key for a recently-exhausted (project, zone, machineType).
func (p *config) stockoutKey(zone, machineType string) string {
	return p.projectID + ":" + zone + ":" + machineType
}

// markStockout records that (zone, machineType) just returned a GCP stockout so
// future create candidate ordering deprioritizes it until the entry expires.
func (p *config) markStockout(zone, machineType string) {
	if p.stockoutCache == nil || zone == "" {
		return
	}
	p.stockoutCache.Add(p.stockoutKey(zone, machineType), struct{}{})
}

// isStockoutZone reports whether (zone, machineType) is currently remembered as
// recently exhausted.
func (p *config) isStockoutZone(zone, machineType string) bool {
	if p.stockoutCache == nil {
		return false
	}
	_, found := p.stockoutCache.Get(p.stockoutKey(zone, machineType))
	return found
}

// logStockoutDeprioritization emits a debug line when the candidate ordering was
// influenced by recently stocked-out zones, so the cache's effect on VM init time
// is observable in production logs.
func (p *config) logStockoutDeprioritization(logr logger.Logger, candidates []createCandidate, machineType string) {
	if p.stockoutCache == nil {
		return
	}
	var flagged []string
	order := make([]string, 0, len(candidates))
	for _, c := range candidates {
		order = append(order, c.zone)
		if p.isStockoutZone(c.zone, machineType) {
			flagged = append(flagged, c.zone)
		}
	}
	if len(flagged) == 0 {
		return
	}
	logr.WithField("deprioritized_zones", flagged).
		WithField("candidate_order", order).
		Debugln("google: deprioritized recently stocked-out zones in create candidate order")
}

// buildCreateCandidates returns an ordered, capped list of create candidates:
// network configs in declared order, zones shuffled within each, recently
// stocked-out (zone, machineType) deprioritized to the back, then capped at
// maxStockoutAttempts.
func (p *config) buildCreateCandidates(first createCandidate, machineType string) []createCandidate {
	candidates := []createCandidate{first}
	seen := map[string]bool{}
	if first.zone != "" {
		seen[first.zone] = true
	}

	add := func(c createCandidate) {
		if c.zone == "" || seen[c.zone] {
			return
		}
		seen[c.zone] = true
		candidates = append(candidates, c)
	}

	enumerate := func(nc *networkConfig) {
		// Randomize zone order within a network config so multiple runner pods
		// hitting the same stockout do not all retry the same next zone. Network
		// configs themselves are still walked in declared order.
		zones := append([]string{}, nc.zones...)
		rand.Shuffle(len(zones), func(i, j int) { zones[i], zones[j] = zones[j], zones[i] }) //nolint:gosec
		for _, z := range zones {
			network, subnetwork, zone, tags := nc.resolve(p.projectID, z, p.GetRegion)
			add(createCandidate{zone: zone, network: network, subnetwork: subnetwork, tags: tags})
		}
	}

	if len(p.networkConfigs) > 0 {
		for i := range p.networkConfigs {
			enumerate(&p.networkConfigs[i])
		}
	} else {
		single := &networkConfig{network: p.network, subnetwork: p.subnetwork, tags: p.tags, zones: p.zones}
		enumerate(single)
	}

	candidates = p.deprioritizeStockoutZones(candidates, machineType)

	if len(candidates) > maxStockoutAttempts {
		candidates = candidates[:maxStockoutAttempts]
	}
	return candidates
}

// deprioritizeStockoutZones stable-partitions candidates so that zones not
// recently stocked out come first (in their existing order) and recently
// exhausted zones move to the back. Nothing is removed, so the candidate set is
// never emptied.
func (p *config) deprioritizeStockoutZones(candidates []createCandidate, machineType string) []createCandidate {
	if p.stockoutCache == nil {
		return candidates
	}
	healthy := make([]createCandidate, 0, len(candidates))
	exhausted := make([]createCandidate, 0)
	for _, c := range candidates {
		if p.isStockoutZone(c.zone, machineType) {
			exhausted = append(exhausted, c)
		} else {
			healthy = append(healthy, c)
		}
	}
	return append(healthy, exhausted...)
}

func shouldRetry(err error) bool {
	switch e := err.(type) {
	case *googleapi.Error:
		// Retry on 429 and 5xx, according to
		// https://cloud.google.com/storage/docs/exponential-backoff.
		return e.Code == 429 || (e.Code >= 500 && e.Code < 600)
	case interface{ Temporary() bool }:
		return e.Temporary()
	default:
		return false
	}
}

func retry[T any](ctx context.Context, attempts, sleepSecs int, f func() (T, error)) (result T, err error) {
	for i := 0; i < attempts; i++ {
		if i > 0 {
			logger.FromContext(ctx).Warnf("retrying after error: %s\n", err)
			time.Sleep(time.Duration(sleepSecs) * time.Second)
		}
		result, err = f()
		if err == nil {
			return result, nil
		}

		if !shouldRetry(err) {
			return result, err
		}
	}
	return result, err
}

// buildLabelsWithGitspace creates a copy of the instance labels and adds
// the GitspaceConfigIdentifier if present in the options.
func (p *config) buildLabelsWithGitspace(opts *types.InstanceCreateOpts) map[string]string {
	// Start with the default labels
	labels := p.labels

	// Check if we need to modify labels (GitspaceConfigIdentifier or VMLabels present)
	if opts.GitspaceOpts.GitspaceConfigIdentifier != "" || len(opts.VMLabels) > 0 {
		// Create a copy of labels to avoid modifying the original
		labels = make(map[string]string)
		for k, v := range p.labels {
			labels[k] = v
		}
		// Add GitspaceConfigIdentifier to labels if present
		if opts.GitspaceOpts.GitspaceConfigIdentifier != "" {
			labels["name"] = opts.GitspaceOpts.GitspaceConfigIdentifier
		}
		// Add VM labels from createOptions
		for k, v := range opts.VMLabels {
			labels[k] = v
		}
	}

	return labels
}
