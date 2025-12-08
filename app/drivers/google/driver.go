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
	"time"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	itypes "github.com/drone-runners/drone-runner-aws/app/types"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"

	"github.com/dchest/uniuri"
	"github.com/google/uuid"
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
)

var (
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
	service                    *compute.Service
	labels                     map[string]string
	enableNestedVirtualization bool
	enableC4D                  bool
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(config)
	for _, opt := range opts {
		opt(p)
	}

	ctx := context.Background()
	var err error
	if p.service == nil {
		if p.JSONPath != "" {
			p.service, err = compute.NewService(ctx, option.WithCredentialsFile(p.JSONPath))
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

	// Determine zones to try
	var zonesToTry []string
	if opts.Zone != "" {
		// If specific zone requested, only try that zone
		zonesToTry = []string{opts.Zone}
	} else {
		// Randomize zone order to distribute load
		zonesToTry = make([]string, len(p.zones))
		copy(zonesToTry, p.zones)
		rand.Shuffle(len(zonesToTry), func(i, j int) {
			zonesToTry[i], zonesToTry[j] = zonesToTry[j], zonesToTry[i]
		})
	}

	logr := logger.FromContext(ctx).
		WithField("cloud", types.Google).
		WithField("reservation", reservationName).
		WithField("machine_type", machineType).
		WithField("pool", opts.PoolName)

	logr.Debugln("google: creating capacity reservation")

	var lastErr error

	// Try each zone until we successfully create a reservation
	for _, zone := range zonesToTry {
		zoneLogr := logr.WithField("zone", zone)
		zoneLogr.Debugln("google: attempting capacity reservation in zone")

		// Create the reservation
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

		// Insert the reservation
		op, err := p.service.Reservations.Insert(p.projectID, zone, reservation).Context(ctx).Do()
		if err != nil {
			lastErr = err
			zoneLogr.WithError(err).Warnln("google: failed to create capacity reservation in zone, trying next zone")
			continue
		}

		// Wait for the reservation to be created
		err = p.waitZoneOperation(ctx, op.Name, zone)
		if err != nil {
			lastErr = err
			zoneLogr.WithError(err).Warnln("google: capacity reservation creation operation failed in zone, trying next zone")
			continue
		}

		// Success!
		zoneLogr.Infoln("google: capacity reservation created successfully")
		return &types.CapacityReservation{
			StageID:       "", // Will be set by the caller
			PoolName:      opts.PoolName,
			InstanceID:    "", // Will be set when instance is created
			ReservationID: reservationName,
			CreatedAt:     time.Now().Unix(),
		}, nil
	}

	// All zones failed - treat as capacity unavailable
	logr.WithError(lastErr).Errorln("google: capacity unavailable in all zones")
	return nil, &itypes.ErrCapacityUnavailable{Driver: string(types.Google)}
}

// findReservationZone finds the zone where a capacity reservation exists
func (p *config) findReservationZone(ctx context.Context, reservationID string) (zone string, err error) {
	logr := logger.FromContext(ctx)

	for _, z := range p.zones {
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

	// Find the zone for this reservation
	zone, err := p.findReservationZone(ctx, capacity.ReservationID)
	if err != nil {
		logr.Warnln("google: capacity reservation not found in any zone")
		return nil
	}

	logr.WithField("zone", zone).Debugln("google: found capacity reservation")

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
	// opts.Zone has highest priority
	zone := opts.Zone
	// If capacity reservation is provided, verify it's in the zone we're using
	if opts.CapacityReservation != nil && opts.CapacityReservation.ReservationID != "" {
		reservationZone, reservationErr := p.findReservationZone(ctx, opts.CapacityReservation.ReservationID)
		if reservationErr != nil {
			// Error finding reservation - disable it
			logger.FromContext(ctx).
				WithError(reservationErr).
				WithField("reservation", opts.CapacityReservation.ReservationID).
				Warnln("google: capacity reservation lookup failed, proceeding without reservation")
			opts.CapacityReservation = nil
		} else if zone != "" && reservationZone != zone {
			// Reservation is in different zone - disable it
			logger.FromContext(ctx).
				WithField("reservation", opts.CapacityReservation.ReservationID).
				WithField("reservation_zone", reservationZone).
				WithField("requested_zone", zone).
				Warnln("google: capacity reservation zone mismatch, proceeding without reservation")
			opts.CapacityReservation = nil
		} else if reservationZone != "" {
			zone = reservationZone
		}
	}

	if zone == "" {
		zone = p.RandomZone()
	}
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
	network := ""
	if p.network != "" {
		slash := strings.LastIndex(p.network, "/")
		if slash == -1 {
			network = fmt.Sprintf("projects/%s/global/networks/%s",
				p.projectID, p.network)
		} else {
			network = p.network
		}
	}
	subnet := ""
	if p.subnetwork != "" {
		slash := strings.LastIndex(p.subnetwork, "/")
		if slash == -1 {
			subnet = fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s",
				p.projectID, p.GetRegion(zone), p.subnetwork)
		} else {
			subnet = p.subnetwork
		}
	}

	enableNestedVirtualization := opts.NestedVirtualization
	if !enableNestedVirtualization && opts.Platform.OS == oshelp.OSLinux && opts.Platform.Arch == oshelp.ArchAMD64 {
		enableNestedVirtualization = p.enableNestedVirtualization
	}
	advancedMachineFeatures := &compute.AdvancedMachineFeatures{
		EnableNestedVirtualization: enableNestedVirtualization,
	}

	opts.EnableC4D = p.enableC4D

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

	in := &compute.Instance{
		Name:           name,
		Zone:           fmt.Sprintf("projects/%s/zones/%s", p.projectID, zone),
		MinCpuPlatform: "Automatic",
		MachineType:    fmt.Sprintf("projects/%s/zones/%s/machineTypes/%s", p.projectID, zone, machineType),
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				{
					Key:   p.userDataKey,
					Value: googleapi.String(userData),
				},
				{
					Key:   "harness-account-id",
					Value: googleapi.String(opts.AccountID),
				},
				{
					Key:   "harness-pool-name",
					Value: googleapi.String(opts.PoolName),
				},
				{
					Key:   "harness-runner-name",
					Value: googleapi.String(opts.RunnerName),
				},
				{
					Key:   "harness-resource-class",
					Value: googleapi.String(opts.ResourceClass),
				},
				{
					Key:   "harness-platform-os",
					Value: googleapi.String(opts.Platform.OS),
				},
				{
					Key:   "harness-platform-arch",
					Value: googleapi.String(opts.Platform.Arch),
				},
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
					DiskType:    fmt.Sprintf("projects/%s/zones/%s/diskTypes/%s", p.projectID, zone, bootDiskType),
					DiskSizeGb:  bootDiskSize,
				},
			},
		},
		AdvancedMachineFeatures: advancedMachineFeatures,
		CanIpForward:            false,
		NetworkInterfaces: []*compute.NetworkInterface{
			{
				Network:       network,
				Subnetwork:    subnet,
				AccessConfigs: networkConfig,
			},
		},
		Scheduling: &compute.Scheduling{
			Preemptible:       false,
			OnHostMaintenance: "MIGRATE",
			AutomaticRestart:  googleapi.Bool(true),
		},
		DeletionProtection: false,
		Tags: &compute.Tags{
			Items: p.tags,
		},
		Labels: p.labels,
	}

	// Apply GitspaceConfigIdentifier to labels if present
	in.Labels = p.buildLabelsWithGitspace(opts)

	if !p.noServiceAccount {
		in.ServiceAccounts = []*compute.ServiceAccount{
			{
				Scopes: p.scopes,
				Email:  p.serviceAccountEmail,
			},
		}
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

	if opts.StorageOpts.Identifier != "" {
		operations, attachDiskErr := p.attachPersistentDisk(ctx, opts, in, zone)
		if attachDiskErr != nil {
			logr.WithError(attachDiskErr).Errorln("google: failed to attach persistent disk")
			return nil, attachDiskErr
		}
		for _, operation := range operations {
			if operation != nil {
				// Disk not present, wait for creation
				err = p.waitZoneOperation(ctx, operation.Name, zone)
				if err != nil {
					logr.WithError(err).Errorln("google: persistent disk creation operation failed")
					return nil, err
				}
			}
		}
	}

	op, err := p.insertInstance(ctx, p.projectID, zone, uuid.New().String(), in)
	if err != nil {
		logr.WithError(err).Errorln("google: failed to provision VM")
		return nil, err
	}
	err = p.waitZoneOperation(ctx, op.Name, zone)
	if err != nil {
		logr.WithError(err).Errorln("instance insert operation failed")
		return nil, err
	}

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

	instanceMap, err := p.mapToInstance(vm, zone, opts, enableNestedVirtualization, image, machineType)
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

func (p *config) Destroy(ctx context.Context, instances []*types.Instance) (err error) {
	return p.DestroyInstanceAndStorage(ctx, instances, nil)
}

func (p *config) DestroyInstanceAndStorage(ctx context.Context, instances []*types.Instance, storageCleanupType *storage.CleanupType) error {
	var err error
	if len(instances) == 0 {
		return errors.New("no instances provided")
	}

	for _, instance := range instances {
		logr := logger.FromContext(ctx).
			WithField("id", instance.ID).
			WithField("cloud", types.Google)
		zone, getZoneErr := p.getZone(ctx, instance)
		if getZoneErr != nil {
			logr.WithError(getZoneErr).Errorln(
				"google: failed to find instance zone",
				instance.Zone,
			)
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
					err = deleteInstanceErr
				}
			}
			logr.Info("google: sent delete instance request")
		}

		if storageCleanupType != nil && *storageCleanupType != "" {
			if instanceDeleteOperation != nil {
				logr.Info("google: waiting for instance deletion")
				err = p.waitZoneOperation(ctx, instanceDeleteOperation.Name, zone)
			}
			if err != nil {
				logr.WithError(err).Errorln("google: could not delete instance. skipping disk deletion")
				return err
			}

			if *storageCleanupType == storage.Delete && instance.StorageIdentifier != "" {
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
								Errorln("google: error finding persistent disk %s", storageIdentifier)
							return err
						}
					} else {
						err = p.waitZoneOperation(ctx, diskDeleteOperation.Name, zone)
						if err != nil {
							logr.WithError(err).Errorln("google: could not delete persistent disk %s", storageIdentifier)
							return err
						}
					}
				}
			}
		}
	}
	return err
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

func (p *config) mapToInstance(vm *compute.Instance, zone string, opts *types.InstanceCreateOpts, enableNestedVitualization bool, image, machineType string) (types.Instance, error) {
	network := vm.NetworkInterfaces[0]
	instanceIP := ""
	if p.privateIP {
		instanceIP = network.NetworkIP
	} else {
		instanceIP = network.AccessConfigs[0].NatIP
	}

	labelsBytes, marshalErr := json.Marshal(opts.Labels)
	if marshalErr != nil {
		return types.Instance{}, fmt.Errorf("scheduler: could not marshal labels: %v, err: %w", opts.Labels, marshalErr)
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
		State:                      types.StateCreated,
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
		StorageIdentifier:          opts.StorageOpts.Identifier,
		Labels:                     labelsBytes,
		GitspacePortMappings:       gitspacePortMappings,
	}, nil
}

func (p *config) findInstanceZone(ctx context.Context, instanceID string) (
	string, error) {
	for _, zone := range p.zones {
		_, err := p.getInstance(ctx, p.projectID, zone, instanceID)
		if err == nil {
			return zone, nil
		}

		if gerr, ok := err.(*googleapi.Error); ok &&
			gerr.Code == http.StatusNotFound {
			continue
		}
		logger.FromContext(ctx).
			WithField("instance", instanceID).
			WithField("zone", zone).
			Errorln("google: failed to fetch the VM")
	}
	return "", fmt.Errorf("failed to find vm")
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
	if reflect.DeepEqual(p.tags, defaultTags) {
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
			return "", fmt.Errorf("google: failed to find instance in all zones: %w", findInstanceZoneErr)
		}
		return zone, nil
	}

	// validate if instance is present
	_, findInstanceErr := p.getInstance(ctx, p.projectID, instance.Zone, instance.ID)
	if findInstanceErr != nil {
		var googleErr *googleapi.Error
		if errors.As(findInstanceErr, &googleErr) && googleErr.Code == http.StatusNotFound {
			return "", nil
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
		return p.image, nil
	}

	// config.ImageName can be of different formats.
	// we can receive image in following 2 formats:
	// Format #1: harness/vmimage: hosted-vm-ubuntu-2204-jammy-v20250508
	// Format #2: projects/debian-cloud/global/images/debian-11-bullseye-v2025070
	// isFullImagePath() method checks if given image in config.ImageName is of Format #2 which can be
	// directly used, else we convert Format #1 to Format #2 in buildImagePathFromTag() method.
	if isFullImagePath(config.ImageName) {
		return config.ImageName, nil
	}

	return buildImagePathFromTag(config.ImageName, p.projectID), nil
}

// instance name must be 1-63 characters long and match the regular expression
// [a-z]([-a-z0-9]*[a-z0-9])?
func getInstanceName(runner, pool string) string {
	namePrefix := strings.ReplaceAll(runner, " ", "")
	randStr, _ := randStringRunes(randStrLen)
	name := strings.ToLower(fmt.Sprintf("%s-%s-%s-%s", namePrefix, pool, uniuri.NewLen(8), randStr)) //nolint:gomnd
	trimmedName := substrSuffix(name, maxInstanceNameLen)
	if trimmedName[0] == '-' {
		trimmedName = "d" + trimmedName[1:]
	}
	return trimmedName
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

	// Add GitspaceConfigIdentifier to labels if present
	if opts.GitspaceOpts.GitspaceConfigIdentifier != "" {
		// Create a copy of labels to avoid modifying the original
		labels = make(map[string]string)
		for k, v := range p.labels {
			labels[k] = v
		}
		labels["name"] = opts.GitspaceOpts.GitspaceConfigIdentifier
	}

	return labels
}
