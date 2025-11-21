package amazon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	cf "github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/cenkalti/backoff/v4"
	"github.com/dchest/uniuri"
	ierrors "github.com/drone-runners/drone-runner-aws/app/types"
)

var _ drivers.Driver = (*config)(nil)

// config is a struct that implements drivers.Pool interface
type config struct {
	spotInstance     bool
	region           string
	availabilityZone string
	retries          int

	accessKeyID     string
	secretAccessKey string
	sessionToken    string
	keyPairName     string

	rootDir string

	image         string
	size          string
	sizeAlt       string
	user          string
	userData      string
	subnet        string
	vpc           string
	groups        []string
	allocPublicIP bool
	volumeType    string
	volumeSize    int64
	volumeTags    map[string]string
	volumeIops    int64
	kmsKeyID      string
	deviceName    string
	iamProfileArn string
	tags          map[string]string // user defined tags
	hibernate     bool
	zoneDetails   []cf.ZoneInfo

	service *ec2.EC2

	// AMI cache
	amiCache  *AMICache
	enableC4D bool
}

const (
	tagRetries               = 3
	tagRetrySleepMs          = 1000
	defaultSecurityGroupName = "harness-runner"
)

func New(opts ...Option) (drivers.Driver, error) {
	p := new(config)
	for _, opt := range opts {
		opt(p)
	}
	// setup service
	if p.service == nil {
		config := &aws.Config{
			Region:     aws.String(p.region),
			MaxRetries: aws.Int(p.retries),
		}
		if p.accessKeyID != "" && p.secretAccessKey != "" {
			if p.sessionToken != "" {
				config.Credentials = credentials.NewStaticCredentials(p.accessKeyID, p.secretAccessKey, p.sessionToken)
			} else {
				config.Credentials = credentials.NewStaticCredentials(p.accessKeyID, p.secretAccessKey, "")
			}
		}
		mySession := session.Must(session.NewSession())
		p.service = ec2.New(mySession, config)
	}
	// Initialize AMI cache
	p.amiCache = NewAMICache()
	return p, nil
}

func (p *config) DriverName() string {
	return string(types.Amazon)
}

func (p *config) InstanceType() string {
	return p.image
}

func (p *config) RootDir() string {
	return p.rootDir
}

func (p *config) GetFullyQualifiedImage(ctx context.Context, config *types.VMImageConfig) (string, error) {
	imageName := p.image
	// If no image name is provided, return the default image
	if config.ImageName != "" {
		imageName = config.ImageName
	}

	// If the image name is already an AMI ID (starts with "ami-"), return it as is
	if isAMIID(imageName) {
		return imageName, nil
	}

	// Otherwise, resolve the image name to an AMI ID
	resolvedAMI, err := p.resolveImageNameToAMI(ctx, imageName)
	if err != nil {
		return "", fmt.Errorf("failed to resolve image name to AMI: %w", err)
	}
	logger.FromContext(ctx).WithField("image_name", imageName).WithField("resolved_ami", resolvedAMI).Infof("resolved image name to AMI ID")
	return resolvedAMI, nil
}

func (p *config) CanHibernate() bool {
	return p.hibernate
}

// Ping checks that we can log into EC2, and the regions respond
func (p *config) Ping(ctx context.Context) error {
	client := p.service

	allRegions := true
	input := &ec2.DescribeRegionsInput{
		AllRegions: &allRegions,
	}
	_, err := client.DescribeRegionsWithContext(ctx, input)

	return err
}

func lookupCreateSecurityGroupID(ctx context.Context, client *ec2.EC2, vpc string) (string, error) {
	input := &ec2.DescribeSecurityGroupsInput{
		GroupNames: []*string{aws.String(defaultSecurityGroupName)},
	}
	securityGroupResponse, lookupErr := client.DescribeSecurityGroupsWithContext(ctx, input)
	if lookupErr != nil || len(securityGroupResponse.SecurityGroups) == 0 {
		// create the security group
		inputGroup := &ec2.CreateSecurityGroupInput{
			GroupName:   aws.String(defaultSecurityGroupName),
			Description: aws.String("Harnress Runner Security Group"),
		}
		// if we have a vpc, we need to use it
		if vpc != "" {
			inputGroup.VpcId = aws.String(vpc)
		}
		createdGroup, createGroupErr := client.CreateSecurityGroupWithContext(ctx, inputGroup)
		if createGroupErr != nil {
			return "", fmt.Errorf("failed to create security group: %s. %s", defaultSecurityGroupName, createGroupErr)
		}
		ingress := &ec2.AuthorizeSecurityGroupIngressInput{
			GroupId: aws.String(*createdGroup.GroupId),
			IpPermissions: []*ec2.IpPermission{
				{
					IpProtocol: aws.String("tcp"),
					FromPort:   aws.Int64(lehelper.LiteEnginePort),
					ToPort:     aws.Int64(lehelper.LiteEnginePort),
					IpRanges: []*ec2.IpRange{
						{
							CidrIp: aws.String("0.0.0.0/0"),
						},
					},
				},
			},
		}
		_, ingressErr := client.AuthorizeSecurityGroupIngressWithContext(ctx, ingress)
		if ingressErr != nil {
			return "", fmt.Errorf("failed to create ingress rules for security group: %s. %s", defaultSecurityGroupName, ingressErr)
		}
		return *createdGroup.GroupId, nil
	}
	return *securityGroupResponse.SecurityGroups[0].GroupId, nil
}

// lookup Security Group ID and check it has the correct ingress rules
func checkIngressRules(ctx context.Context, client *ec2.EC2, groupID string) error {
	input := &ec2.DescribeSecurityGroupsInput{
		GroupIds: []*string{aws.String(groupID)},
	}
	securityGroupResponse, lookupErr := client.DescribeSecurityGroupsWithContext(ctx, input)
	if lookupErr != nil || len(securityGroupResponse.SecurityGroups) == 0 {
		return fmt.Errorf("failed to lookup security group: %s. %s", groupID, lookupErr)
	}
	securityGroup := securityGroupResponse.SecurityGroups[0]
	found := false
	for _, permission := range securityGroup.IpPermissions {
		if *permission.IpProtocol == "tcp" && *permission.FromPort == lehelper.LiteEnginePort && *permission.ToPort == lehelper.LiteEnginePort {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("security group %s does not have the correct ingress rules. There is no rule for port %d", *securityGroup.GroupName, lehelper.LiteEnginePort)
	}
	return nil
}

// ReserveCapacity reserves capacity for a VM
func (p *config) ReserveCapacity(ctx context.Context, opts *types.InstanceCreateOpts) (*types.CapacityReservation, error) {
	client := p.service

	// Configure dynamic fields (zone, machine type, etc.)
	if err := p.configureDynamicFields(opts); err != nil {
		return nil, fmt.Errorf("failed to configure dynamic fields: %w", err)
	}

	logr := logger.FromContext(ctx).
		WithField("driver", types.Amazon).
		WithField("instance_type", p.size).
		WithField("availability_zone", p.availabilityZone).
		WithField("pool", opts.PoolName)

	logr.Debugln("amazon: creating capacity reservation")

	// Determine instance platform based on OS
	// Note: The most accurate method would be to query the AMI's PlatformDetails field via DescribeImages API,
	// which provides exact platform strings like "Red Hat Enterprise Linux", "SUSE Linux", "Ubuntu Pro", etc.
	// However, we use opts.Platform.OS directly to avoid an extra API call. This works for Linux/UNIX and Windows.
	// If we start supporting other platforms like Red Hat Enterprise Linux or SUSE Linux, we should query the AMI.
	// See supported platforms: https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-capacity-reservations.html
	var instancePlatform string
	switch opts.Platform.OS {
	case oshelp.OSWindows:
		instancePlatform = "Windows"
	case oshelp.OSLinux:
		instancePlatform = "Linux/UNIX"
	case oshelp.OSMac:
		logr.Errorln("amazon: capacity reservations are not supported for macOS instances")
		return nil, fmt.Errorf("capacity reservations are not supported for macOS instances on AWS")
	default:
		instancePlatform = "Linux/UNIX"
	}

	logr.WithField("platform", instancePlatform).Debugln("amazon: using platform for capacity reservation")

	// Create the capacity reservation
	input := &ec2.CreateCapacityReservationInput{
		InstanceType:          aws.String(p.size),
		InstancePlatform:      aws.String(instancePlatform),
		AvailabilityZone:      aws.String(p.availabilityZone),
		InstanceCount:         aws.Int64(1),
		EndDateType:           aws.String("unlimited"), // No end date
		InstanceMatchCriteria: aws.String("targeted"),  // Instances must explicitly target this reservation
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("capacity-reservation"),
				Tags:         convertTags(buildHarnessTags(opts)),
			},
		},
	}

	result, err := client.CreateCapacityReservationWithContext(ctx, input)
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == "InsufficientInstanceCapacity" {
				logr.WithError(err).Errorln("amazon: insufficient capacity available")
				return nil, &ierrors.ErrCapacityUnavailable{Driver: p.DriverName()}
			}
		}
		logr.WithError(err).Errorln("amazon: failed to create capacity reservation")
		return nil, &ierrors.ErrCapacityUnavailable{Driver: p.DriverName()}
	}

	reservationID := aws.StringValue(result.CapacityReservation.CapacityReservationId)

	logr.WithField("reservation_id", reservationID).Infoln("amazon: capacity reservation created successfully")

	return &types.CapacityReservation{
		StageID:       "", // Will be set by the caller
		PoolName:      opts.PoolName,
		InstanceID:    "",
		ReservationID: reservationID,
		CreatedAt:     time.Now().Unix(),
	}, nil
}

// DestroyCapacity destroys capacity for a VM
func (p *config) DestroyCapacity(ctx context.Context, capacity *types.CapacityReservation) (err error) {
	if capacity == nil || capacity.ReservationID == "" {
		return fmt.Errorf("invalid capacity reservation: missing reservation ID")
	}

	client := p.service

	logr := logger.FromContext(ctx).
		WithField("driver", types.Amazon).
		WithField("reservation", capacity.ReservationID).
		WithField("pool", capacity.PoolName)

	logr.Debugln("amazon: deleting capacity reservation")

	// Cancel the capacity reservation
	input := &ec2.CancelCapacityReservationInput{
		CapacityReservationId: aws.String(capacity.ReservationID),
	}

	_, err = client.CancelCapacityReservationWithContext(ctx, input)
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			// If the reservation doesn't exist, consider it already deleted
			if awsErr.Code() == "InvalidCapacityReservationId.NotFound" {
				logr.Warnln("amazon: capacity reservation already deleted or not found")
				return nil
			}
		}
		logr.WithError(err).Errorln("amazon: failed to delete capacity reservation")
		return fmt.Errorf("failed to delete capacity reservation: %w", err)
	}

	logr.Infoln("amazon: capacity reservation deleted successfully")
	return nil
}

// Create an AWS instance for the pool, it will not perform build specific setup.
//
//nolint:gocyclo
func (p *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	client := p.service
	startTime := time.Now()
	if err = p.configureDynamicFields(opts); err != nil {
		return nil, fmt.Errorf("failed to configure dynamic fields: %s", err)
	}

	resolvedAMI, err := p.GetFullyQualifiedImage(ctx, &opts.VMImageConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get image: %w", err)
	}

	logr := logger.FromContext(ctx).
		WithField("driver", types.Amazon).
		WithField("ami", p.InstanceType()).
		WithField("pool", opts.PoolName).
		WithField("region", p.region).
		WithField("ami", resolvedAMI).
		WithField("size", p.size).
		WithField("zone", p.availabilityZone).
		WithField("hibernate", p.CanHibernate())

	var name = getInstanceName(opts.RunnerName, opts.PoolName, opts.GitspaceOpts.GitspaceConfigIdentifier)

	// Start with common harness tags
	tags := buildHarnessTags(opts)
	// Add instance-specific Name tag
	tags["Name"] = name

	var volumeTags = map[string]string{}
	// add user defined tags
	for k, v := range p.tags {
		tags[k] = v
	}
	// add volume tags
	for k, v := range p.volumeTags {
		volumeTags[k] = v
	}
	if p.vpc == "" {
		logr.Traceln("amazon: using default vpc, checking security groups")
	} else {
		logr.Tracef("amazon: using vpc %s, checking security groups", p.vpc)
	}
	// check security group exists
	if p.groups == nil || len(p.groups) == 0 {
		logr.Warnf("aws: no security group specified assuming '%s'", defaultSecurityGroupName)
		// lookup/create group
		returnedGroupID, lookupErr := lookupCreateSecurityGroupID(ctx, client, p.vpc)
		if lookupErr != nil {
			return nil, lookupErr
		}
		p.groups = append(p.groups, returnedGroupID)
	}
	// check the security group ingress rules
	rulesErr := checkIngressRules(ctx, client, p.groups[0])
	if rulesErr != nil {
		return nil, rulesErr
	}

	logr.Traceln("amazon: provisioning VM")

	var iamProfile *ec2.IamInstanceProfileSpecification
	if p.iamProfileArn != "" {
		iamProfile = &ec2.IamInstanceProfileSpecification{
			Arn: aws.String(p.iamProfileArn),
		}
	}
	opts.EnableC4D = p.enableC4D

	userData, err := lehelper.GenerateUserdata(p.userData, opts)
	if err != nil {
		logr.WithError(err).
			Errorln("amazon: [provision] failed to generate user data")
		return nil, err
	}

	in := &ec2.RunInstancesInput{
		ImageId:            aws.String(resolvedAMI),
		InstanceType:       aws.String(p.size),
		Placement:          &ec2.Placement{AvailabilityZone: aws.String(p.availabilityZone)},
		MinCount:           aws.Int64(1),
		MaxCount:           aws.Int64(1),
		IamInstanceProfile: iamProfile,
		UserData: aws.String(
			base64.StdEncoding.EncodeToString(
				[]byte(userData),
			),
		),
		NetworkInterfaces: []*ec2.InstanceNetworkInterfaceSpecification{
			{
				AssociatePublicIpAddress: aws.Bool(p.allocPublicIP),
				DeviceIndex:              aws.Int64(0),
				SubnetId:                 aws.String(p.subnet),
				Groups:                   aws.StringSlice(p.groups),
			},
		},
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("instance"),
				Tags:         convertTags(tags),
			},
		},
		BlockDeviceMappings: []*ec2.BlockDeviceMapping{
			{
				DeviceName: aws.String(p.deviceName),
				Ebs: &ec2.EbsBlockDevice{
					VolumeSize:          aws.Int64(p.volumeSize),
					VolumeType:          aws.String(p.volumeType),
					DeleteOnTermination: aws.Bool(true),
				},
			},
		},
	}
	if len(volumeTags) != 0 {
		in.TagSpecifications = append(in.TagSpecifications, &ec2.TagSpecification{
			ResourceType: aws.String("volume"),
			Tags:         convertTags(volumeTags),
		})
	}
	if p.keyPairName != "" {
		in.KeyName = aws.String(p.keyPairName)
	}

	if p.volumeType == "io1" {
		for _, blockDeviceMapping := range in.BlockDeviceMappings {
			blockDeviceMapping.Ebs.Iops = aws.Int64(p.volumeIops)
		}
	}

	if p.kmsKeyID != "" {
		for _, blockDeviceMapping := range in.BlockDeviceMappings {
			blockDeviceMapping.Ebs.Encrypted = aws.Bool(true)
			blockDeviceMapping.Ebs.KmsKeyId = aws.String(p.kmsKeyID)
		}
	}

	if p.CanHibernate() {
		for _, blockDeviceMapping := range in.BlockDeviceMappings {
			blockDeviceMapping.Ebs.Encrypted = aws.Bool(true)
			if p.kmsKeyID != "" {
				blockDeviceMapping.Ebs.KmsKeyId = aws.String(p.kmsKeyID)
			}
		}

		in.HibernationOptions = &ec2.HibernationOptionsRequest{
			Configured: aws.Bool(true),
		}
	}

	// Use capacity reservation if provided
	if opts.CapacityReservation != nil && opts.CapacityReservation.ReservationID != "" {
		logr.WithField("reservation_id", opts.CapacityReservation.ReservationID).
			Debugln("amazon: using capacity reservation")
		in.CapacityReservationSpecification = &ec2.CapacityReservationSpecification{
			CapacityReservationTarget: &ec2.CapacityReservationTarget{
				CapacityReservationId: aws.String(opts.CapacityReservation.ReservationID),
			},
		}
	}

	// Create persistent disks first
	volumes, err := p.createPersistentDisks(ctx, opts)
	if err != nil {
		logr.WithError(err).Errorln("amazon: failed to create persistent disks")
		return nil, err
	}

	runResult, err := client.RunInstancesWithContext(ctx, in)
	if err != nil {
		logr.WithError(err).
			Errorln("amazon: [provision] failed to create VMs")
		return nil, err
	}

	if len(runResult.Instances) == 0 {
		err = fmt.Errorf("failed to create an AWS EC2 instance")
		return nil, err
	}

	awsInstanceID := runResult.Instances[0].InstanceId

	logr = logr.
		WithField("id", *awsInstanceID)

	logr.Debugln("amazon: [provision] created instance")

	// poll the amazon endpoint for server updates and exit when a network address is allocated.
	var amazonInstance *ec2.Instance
	amazonInstance, err = p.pollInstanceIPAddr(ctx, *awsInstanceID, logr)
	if err != nil {
		return nil, err
	}

	// Now that instance is running, attach volumes if any
	if len(volumes) > 0 {
		logr.Debugln("amazon: [provision] attaching volumes to the instance")
		if attachVolumesErr := p.attachVolumes(ctx, awsInstanceID, volumes, logr); attachVolumesErr != nil {
			return nil, err
		}
	}

	instanceID := *amazonInstance.InstanceId
	instanceIP := p.getIP(amazonInstance)
	launchTime := p.getLaunchTime(amazonInstance)

	labelsBytes, marshalErr := json.Marshal(opts.Labels)
	if marshalErr != nil {
		return &types.Instance{}, fmt.Errorf("scheduler: could not marshal labels: %v, err: %w", opts.Labels, marshalErr)
	}
	gitspacePortMappings := make(map[int]int)
	for _, port := range opts.GitspaceOpts.Ports {
		gitspacePortMappings[port] = port
	}

	instance = &types.Instance{
		ID:                   instanceID,
		Name:                 instanceID,
		Provider:             types.Amazon, // this is driver, though its the old legacy name of provider
		State:                types.StateCreated,
		Pool:                 opts.PoolName,
		Image:                resolvedAMI,
		Zone:                 p.availabilityZone,
		Region:               p.region,
		Size:                 p.size,
		Platform:             opts.Platform,
		Address:              instanceIP,
		CACert:               opts.CACert,
		CAKey:                opts.CAKey,
		TLSCert:              opts.TLSCert,
		TLSKey:               opts.TLSKey,
		Started:              launchTime.Unix(),
		Updated:              time.Now().Unix(),
		IsHibernated:         false,
		Port:                 lehelper.LiteEnginePort,
		StorageIdentifier:    opts.StorageOpts.Identifier,
		Labels:               labelsBytes,
		GitspacePortMappings: gitspacePortMappings,
	}
	logr.
		WithField("ip", instanceIP).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("amazon: [provision] complete")

	return instance, nil
}

func (p *config) Destroy(ctx context.Context, instances []*types.Instance) (err error) {
	return p.DestroyInstanceAndStorage(ctx, instances, nil)
}

// DestroyInstanceAndStorage destroys the server AWS EC2 instances.
func (p *config) DestroyInstanceAndStorage(
	ctx context.Context,
	instances []*types.Instance,
	storageCleanupType *storage.CleanupType,
) (err error) {
	var instanceIDs []string
	for _, instance := range instances {
		instanceIDs = append(instanceIDs, instance.ID)
	}
	if len(instanceIDs) == 0 {
		return fmt.Errorf("no instance IDs provided")
	}

	client := p.service

	logr := logger.FromContext(ctx).
		WithField("id", instanceIDs).
		WithField("driver", types.Amazon)

	awsIDs := make([]*string, len(instanceIDs))
	for i, instanceID := range instanceIDs {
		awsIDs[i] = aws.String(instanceID)
	}

	var volumesToDelete []*string
	if storageCleanupType != nil && *storageCleanupType == storage.Delete {
		vols, findVolumesErr := p.findPersistentVolumes(ctx, instances, logr)
		if findVolumesErr != nil {
			return findVolumesErr
		}
		volumesToDelete = vols
	}

	_, err = client.TerminateInstances(&ec2.TerminateInstancesInput{InstanceIds: awsIDs})
	if err != nil {
		err = fmt.Errorf("failed to terminate instances: %v", err)
		logr.Error(err)
		return err
	}

	logr.Traceln("amazon: VM terminated")

	if storageCleanupType != nil {
		return p.cleanupVolumes(ctx, awsIDs, *storageCleanupType, volumesToDelete)
	}

	return nil
}

func (p *config) Logs(ctx context.Context, instanceID string) (string, error) {
	client := p.service

	output, err := client.GetConsoleOutputWithContext(ctx, &ec2.GetConsoleOutputInput{
		InstanceId: aws.String(instanceID),
	})
	if err != nil {
		return "", fmt.Errorf("amazon: failed to get console output: %s", err)
	}
	if output.Output == nil {
		return "'console output is empty'", nil
	}
	decoded, _ := base64.StdEncoding.DecodeString(*output.Output)
	return string(decoded), nil
}

func (p *config) SetTags(ctx context.Context, instance *types.Instance,
	tags map[string]string) error {
	in := &ec2.CreateTagsInput{
		Resources: []*string{aws.String(instance.ID)},
	}
	logr := logger.FromContext(ctx).
		WithField("id", instance.ID).
		WithField("driver", types.Amazon)
	for key, value := range tags {
		in.Tags = append(in.Tags, &ec2.Tag{
			Key:   aws.String(key),
			Value: aws.String(value),
		})
	}
	var err error
	for i := 0; i < tagRetries; i++ {
		_, err = p.service.CreateTagsWithContext(ctx, in)
		if err == nil {
			return nil
		}
		logr.WithError(err).Warnln("failed to set tags to the instance. retrying")
		time.Sleep(tagRetrySleepMs)
	}

	return err
}

func (p *config) Hibernate(ctx context.Context, instanceID, poolName, _ string) error {
	logr := logger.FromContext(ctx).
		WithField("driver", types.Amazon).
		WithField("pool", poolName).
		WithField("instanceID", instanceID).
		WithField("hibernate", p.hibernate)

	client := p.service
	_, err := client.StopInstancesWithContext(ctx, &ec2.StopInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
		Hibernate:   aws.Bool(p.hibernate),
	})
	if err != nil {
		logr.WithError(err).
			Errorln("aws: failed to hibernate the VM")
		if isHibernateRetryable(err) {
			return &ierrors.RetryableError{Msg: err.Error()}
		}
		return err
	}

	logr.Traceln("amazon: VM hibernated")
	return nil
}

func isHibernateRetryable(origErr error) bool {
	if request.IsErrorRetryable(origErr) {
		return true
	}

	if awsErr, ok := origErr.(awserr.Error); ok {
		// Amazon linux 2 instance return error message on first try:
		// UnsupportedOperation: Instance is not ready to hibernate yet, retry in a few minutes
		if awsErr.Code() == "UnsupportedOperation" {
			return true
		}
	}

	return false
}

func (p *config) Start(ctx context.Context, instance *types.Instance, poolName string) (string, error) {
	client := p.service

	logr := logger.FromContext(ctx).
		WithField("driver", types.Amazon).
		WithField("pool", poolName).
		WithField("instanceID", instance.ID)

	amazonInstance, err := p.getInstance(ctx, instance.ID)
	if err != nil {
		logr.WithError(err).Errorln("aws: failed to find instance status")
	}

	state := p.getState(amazonInstance)
	if state == ec2.InstanceStateNameRunning {
		return p.getIP(amazonInstance), nil
	} else if state == ec2.InstanceStateNameStopping {
		logr.Traceln("aws: waiting for instance to stop")
		waitErr := client.WaitUntilInstanceStoppedWithContext(ctx, &ec2.DescribeInstancesInput{InstanceIds: []*string{aws.String(instance.ID)}})
		if waitErr != nil {
			logr.WithError(waitErr).Warnln("aws: instance failed to stop. Proceeding with starting the instance")
		}
	}

	_, err = client.StartInstancesWithContext(ctx,
		&ec2.StartInstancesInput{InstanceIds: []*string{aws.String(instance.ID)}})
	if err != nil {
		logr.WithError(err).
			Errorln("aws: failed to start VMs")
		return "", err
	}
	logr.Traceln("amazon: VM started")

	awsInstance, err := p.pollInstanceIPAddr(ctx, instance.ID, logr)
	if err != nil {
		logr.WithError(err).
			Errorln("aws: failed to retrieve IP address of the VM")
		return "", err
	}
	return p.getIP(awsInstance), nil
}

func (p *config) configureDynamicFields(opts *types.InstanceCreateOpts) error {
	if opts.Zone != "" {
		p.availabilityZone = opts.Zone
		for _, zoneDetail := range p.zoneDetails {
			if zoneDetail.AvailabilityZone == opts.Zone {
				p.subnet = zoneDetail.SubnetID
			}
		}
	}
	if opts.MachineType != "" {
		p.size = opts.MachineType
	}
	if opts.StorageOpts.BootDiskSize != "" {
		diskSize, diskSizeErr := strconv.ParseInt(opts.StorageOpts.BootDiskSize, 10, 64)
		if diskSizeErr != nil {
			return fmt.Errorf("failed to parse volume size: %w", diskSizeErr)
		}
		p.volumeSize = diskSize
	}
	if opts.StorageOpts.BootDiskType != "" {
		p.volumeType = opts.StorageOpts.BootDiskType
	}
	return nil
}

func (p *config) getIP(amazonInstance *ec2.Instance) string {
	if p.allocPublicIP {
		if amazonInstance.PublicIpAddress == nil {
			return ""
		}
		return *amazonInstance.PublicIpAddress
	}

	if amazonInstance.PrivateIpAddress == nil {
		return ""
	}
	return *amazonInstance.PrivateIpAddress
}

func (p *config) getState(amazonInstance *ec2.Instance) string {
	if amazonInstance.State == nil {
		return ""
	}

	if amazonInstance.State.Name == nil {
		return ""
	}

	return *amazonInstance.State.Name
}

func (p *config) getLaunchTime(amazonInstance *ec2.Instance) time.Time {
	if amazonInstance.LaunchTime == nil {
		return time.Now()
	}
	return *amazonInstance.LaunchTime
}

func (p *config) pollInstanceIPAddr(ctx context.Context, instanceID string, logr logger.Logger) (*ec2.Instance, error) {
	client := p.service
	b := backoff.NewExponentialBackOff()
	for {
		duration := b.NextBackOff()
		if duration == b.Stop {
			logr.Errorln("amazon: [provision] failed to obtain IP; terminating it")

			input := &ec2.TerminateInstancesInput{
				InstanceIds: []*string{aws.String(instanceID)},
			}
			_, _ = client.TerminateInstancesWithContext(ctx, input)
			return nil, errors.New("failed to obtain IP address")
		}

		select {
		case <-ctx.Done():
			logr.Warnln("amazon: [provision] instance network deadline exceeded")
			return nil, ctx.Err()

		case <-time.After(duration):
			logr.Traceln("amazon: [provision] checking instance IP address")

			desc, descrErr := client.DescribeInstancesWithContext(ctx,
				&ec2.DescribeInstancesInput{
					InstanceIds: []*string{aws.String(instanceID)},
				},
			)
			if descrErr != nil {
				logr.WithError(descrErr).Warnln("amazon: [provision] instance details failed")
				continue
			}

			if len(desc.Reservations) == 0 {
				logr.Warnln("amazon: [provision] empty reservations in details")
				continue
			}

			if len(desc.Reservations[0].Instances) == 0 {
				logr.Warnln("amazon: [provision] empty instances in reservations")
				continue
			}

			instance := desc.Reservations[0].Instances[0]
			instanceIP := p.getIP(instance)

			if instanceIP == "" {
				logr.Traceln("amazon: [provision] instance has no IP yet")
				continue
			}

			return instance, nil
		}
	}
}

func (p *config) getInstance(ctx context.Context, instanceID string) (*ec2.Instance, error) {
	client := p.service
	response, err := client.DescribeInstancesWithContext(ctx, &ec2.DescribeInstancesInput{InstanceIds: []*string{aws.String(instanceID)}})
	if err != nil {
		return nil, err
	}

	if len(response.Reservations) == 0 {
		return nil, errors.New("amazon: empty reservations in details")
	}

	if len(response.Reservations[0].Instances) == 0 {
		return nil, errors.New("amazon: [provision] empty instances in reservations")
	}

	return response.Reservations[0].Instances[0], nil
}

// getNextDeviceName returns the next available device name starting from /dev/sde.
// We start from 'e' because:
// This ensures we avoid conflicts with built-in device names.
func (p *config) getNextDeviceName(index int) string {
	// For HVM instances, AWS recommends /dev/sd[b-z] for EBS data volumes
	deviceLetter := rune('e' + index)
	if deviceLetter > 'z' {
		// If we somehow exceed 'z', wrap around
		deviceLetter = 'e'
	}
	return fmt.Sprintf("/dev/sd%c", deviceLetter)
}

func (p *config) waitForVolumeAvailable(ctx context.Context, volumeID *string) error {
	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = 5 * time.Minute

	return backoff.Retry(func() error {
		desc, err := p.service.DescribeVolumesWithContext(ctx, &ec2.DescribeVolumesInput{
			VolumeIds: []*string{volumeID},
		})
		if err != nil {
			return err
		}
		if len(desc.Volumes) == 0 {
			return fmt.Errorf("volume not found")
		}
		if *desc.Volumes[0].State != "available" {
			return fmt.Errorf("volume not available yet")
		}
		return nil
	}, b)
}

// cleanupVolumes waits for instances to terminate and handles volume cleanup based on cleanup type
func (p *config) cleanupVolumes(ctx context.Context, instanceIDs []*string, cleanupType storage.CleanupType, volumeIDs []*string) error {
	logr := logger.FromContext(ctx)
	if cleanupType != storage.Delete || len(volumeIDs) == 0 {
		return nil
	}

	// Wait for instances to terminate
	logr.Infoln("aws: waiting for instance termination")
	if err := p.service.WaitUntilInstanceTerminatedWithContext(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: instanceIDs,
	}); err != nil {
		logr.WithError(err).Errorln("aws: failed waiting for instance termination")
		return err
	}

	// Delete the persistent volumes
	logr.Infoln("aws: deleting persistent volumes")
	for _, volumeID := range volumeIDs {
		_, err := p.service.DeleteVolumeWithContext(ctx, &ec2.DeleteVolumeInput{
			VolumeId: volumeID,
		})
		if err != nil {
			var awsErr awserr.Error
			if errors.As(err, &awsErr) && awsErr.Code() == "InvalidVolume.NotFound" {
				logr.WithError(err).Warnf("aws: volume %s not found", aws.StringValue(volumeID))
				continue
			}
			logr.WithError(err).Errorf("aws: failed to delete volume %s", aws.StringValue(volumeID))
			return err
		}
	}

	return nil
}

// findPersistentVolumes finds all EBS volumes attached to the given instances that have DeleteOnTermination=false
func (p *config) findPersistentVolumes(ctx context.Context, instances []*types.Instance, logr logger.Logger) ([]*string, error) {
	// Get all volumes with Name tag matching any of the storage identifiers and DeleteOnTermination=false
	var filters []*ec2.Filter
	// Add filter for non-delete-on-termination volumes
	filters = append(filters, &ec2.Filter{
		Name:   aws.String("attachment.delete-on-termination"),
		Values: []*string{aws.String("false")},
	})

	// Add filters for volume names
	for _, instance := range instances {
		if instance.StorageIdentifier != "" {
			for _, diskName := range strings.Split(instance.StorageIdentifier, ",") {
				diskName = strings.TrimSpace(diskName)
				filters = append(filters, &ec2.Filter{
					Name:   aws.String("tag:Name"),
					Values: []*string{aws.String(diskName)},
				})
			}
		}
	}

	if len(filters) == 0 {
		return nil, nil
	}

	describe, err := p.service.DescribeVolumesWithContext(ctx, &ec2.DescribeVolumesInput{
		Filters: filters,
	})
	if err != nil {
		logr.WithError(err).Errorln("aws: failed to describe volumes")
		return nil, err
	}

	var volumeIDs []*string
	for _, volume := range describe.Volumes {
		volumeIDs = append(volumeIDs, volume.VolumeId)
	}

	return volumeIDs, nil
}

func (p *config) attachVolumes(ctx context.Context, instanceID *string, volumes []*ec2.Volume, logr logger.Logger) error {
	// Attach volumes with retry
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 2 * time.Second // First retry after 2s
	b.Multiplier = 2                    // Next retry after 4s (total 6s)
	b.MaxElapsedTime = 5 * time.Minute

	for i, volume := range volumes {
		retryCount := 0
		err := backoff.Retry(func() error {
			retryCount++
			logr.Debugf("amazon: attempting to attach volume %s (attempt %d)", *volume.VolumeId, retryCount)
			// Check if instance is running
			desc, err := p.service.DescribeInstancesWithContext(ctx, &ec2.DescribeInstancesInput{
				InstanceIds: []*string{instanceID},
			})
			if err != nil {
				logr.Debugf("amazon: failed to describe instance on attempt %d: %v", retryCount, err)
				return err
			}
			if len(desc.Reservations) == 0 || len(desc.Reservations[0].Instances) == 0 {
				logr.Debugf("amazon: instance not found on attempt %d", retryCount)
				return fmt.Errorf("instance not found")
			}

			state := aws.StringValue(desc.Reservations[0].Instances[0].State.Name)
			if state != "running" {
				logr.Debugf("amazon: instance state is %s on attempt %d", state, retryCount)
				return fmt.Errorf("instance not running yet")
			}

			// Try to attach volume
			deviceName := p.getNextDeviceName(i)
			logr.Debugf("amazon: attempting to attach volume %s to device %s on attempt %d", *volume.VolumeId, deviceName, retryCount)

			_, err = p.service.AttachVolumeWithContext(ctx, &ec2.AttachVolumeInput{
				Device:     aws.String(deviceName),
				InstanceId: instanceID,
				VolumeId:   volume.VolumeId,
			})
			if err != nil {
				logr.Debugf("amazon: failed to attach volume on attempt %d: %v", retryCount, err)
			}
			return err
		}, b)

		if err != nil {
			logr.WithError(err).Errorln("amazon: failed to attach volume")
			return err
		}
	}

	return nil
}

func (p *config) createPersistentDisks(
	ctx context.Context,
	opts *types.InstanceCreateOpts,
) ([]*ec2.Volume, error) {
	if opts.StorageOpts.Identifier == "" {
		return nil, nil
	}

	storageIdentifiers := strings.Split(opts.StorageOpts.Identifier, ",")

	var volumeSize int64
	if opts.StorageOpts.Size != "" {
		size, err := strconv.ParseInt(opts.StorageOpts.Size, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse volume size: %w", err)
		}
		volumeSize = size
	}

	var volumes []*ec2.Volume
	for _, diskName := range storageIdentifiers {
		diskName = strings.TrimSpace(diskName)

		// Create volume
		createVolumeInput := &ec2.CreateVolumeInput{
			AvailabilityZone: aws.String(p.availabilityZone),
			VolumeType:       aws.String(opts.StorageOpts.Type),
			Size:             aws.Int64(volumeSize),
			Encrypted:        aws.Bool(true),
			TagSpecifications: []*ec2.TagSpecification{
				{
					ResourceType: aws.String("volume"),
					Tags: []*ec2.Tag{
						{
							Key:   aws.String("Name"),
							Value: aws.String(diskName),
						},
					},
				},
			},
		}

		// Create the volume
		volume, err := p.service.CreateVolumeWithContext(ctx, createVolumeInput)
		if err != nil {
			return nil, fmt.Errorf("failed to create volume: %w", err)
		}

		// Wait for volume to be available
		if err := p.waitForVolumeAvailable(ctx, volume.VolumeId); err != nil {
			return nil, fmt.Errorf("error waiting for volume to become available: %w", err)
		}

		volumes = append(volumes, volume)
	}

	return volumes, nil
}

func getInstanceName(runner, pool, gitspaceConfigIdentifier string) string {
	if gitspaceConfigIdentifier != "" {
		return gitspaceConfigIdentifier
	}
	return fmt.Sprintf("%s-%s-%s", runner, pool, uniuri.NewLen(8)) //nolint:gomnd
}
