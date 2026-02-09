package amazon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/drone/runner-go/logger"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	cf "github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	drtypes "github.com/drone-runners/drone-runner-aws/types"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/cenkalti/backoff/v4"
	"github.com/dchest/uniuri"

	ierrors "github.com/drone-runners/drone-runner-aws/app/types"
)

const (
	// instanceWaitTimeout is the maximum time to wait for instance state changes
	instanceWaitTimeout = 5 * time.Minute
)

var _ drivers.Driver = (*amazonConfig)(nil)

// ec2ClientAPI is an interface for EC2 client operations
type ec2ClientAPI interface {
	DescribeRegions(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error)
	RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	TerminateInstances(ctx context.Context, params *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	StopInstances(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error)
	StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
	DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
	CreateSecurityGroup(ctx context.Context, params *ec2.CreateSecurityGroupInput, optFns ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error)
	DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	AuthorizeSecurityGroupIngress(ctx context.Context, params *ec2.AuthorizeSecurityGroupIngressInput, optFns ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error)
	CreateVolume(ctx context.Context, params *ec2.CreateVolumeInput, optFns ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error)
	DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
	AttachVolume(ctx context.Context, params *ec2.AttachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.AttachVolumeOutput, error)
	DeleteVolume(ctx context.Context, params *ec2.DeleteVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error)
	CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
	GetConsoleOutput(ctx context.Context, params *ec2.GetConsoleOutputInput, optFns ...func(*ec2.Options)) (*ec2.GetConsoleOutputOutput, error)
	CreateCapacityReservation(ctx context.Context, params *ec2.CreateCapacityReservationInput, optFns ...func(*ec2.Options)) (*ec2.CreateCapacityReservationOutput, error)
	CancelCapacityReservation(ctx context.Context, params *ec2.CancelCapacityReservationInput, optFns ...func(*ec2.Options)) (*ec2.CancelCapacityReservationOutput, error)
}

// amazonConfig is a struct that implements drivers.Pool interface
type amazonConfig struct {
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
	zoneIndex     uint64 // counter for round-robin zone selection

	service ec2ClientAPI

	// AMI cache
	amiCache  *AMICache
	enableC4D bool
}

const (
	tagRetries               = 3
	tagRetrySleepMs          = 1000
	defaultSecurityGroupName = "harness-runner"
)

// requestConfig holds request-specific configuration derived from InstanceCreateOpts
// without mutating the shared pool configuration
type requestConfig struct {
	availabilityZone string
	subnet           string
	size             string
	volumeSize       int64
	volumeType       string
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(amazonConfig)
	for _, opt := range opts {
		opt(p)
	}
	// setup service
	if p.service == nil {
		ctx := context.Background()

		var cfg aws.Config
		var err error

		// Prioritize static credentials if provided
		if p.accessKeyID != "" && p.secretAccessKey != "" {
			cfg, err = config.LoadDefaultConfig(ctx,
				config.WithRegion(p.region),
				config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
					p.accessKeyID,
					p.secretAccessKey,
					p.sessionToken,
				)),
			)
		} else {
			// Load default config (Pod Identity, IRSA, instance profile, etc.)
			cfg, err = config.LoadDefaultConfig(ctx, config.WithRegion(p.region))
		}
		if err != nil {
			return nil, fmt.Errorf("failed to load AWS config: %w", err)
		}

		// Note: SDK v2 handles retries differently via retry modes
		// The retries field may need to be configured via config.WithRetryMaxAttempts
		if p.retries > 0 {
			cfg.RetryMaxAttempts = p.retries
		}

		p.service = ec2.NewFromConfig(cfg)
	}
	// Initialize AMI cache
	p.amiCache = NewAMICache()
	return p, nil
}

func (p *amazonConfig) DriverName() string {
	return string(drtypes.Amazon)
}

func (p *amazonConfig) InstanceType() string {
	return p.image
}

func (p *amazonConfig) RootDir() string {
	return p.rootDir
}

func (p *amazonConfig) GetFullyQualifiedImage(ctx context.Context, vmConfig *drtypes.VMImageConfig) (string, error) {
	imageName := p.image
	// If no image name is provided, return the default image
	if vmConfig.ImageName != "" {
		imageName = vmConfig.ImageName
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

func (p *amazonConfig) CanHibernate() bool {
	return p.hibernate
}

// Ping checks that we can log into EC2, and the regions respond
func (p *amazonConfig) Ping(ctx context.Context) error {
	client := p.service

	allRegions := true
	input := &ec2.DescribeRegionsInput{
		AllRegions: &allRegions,
	}
	_, err := client.DescribeRegions(ctx, input)

	return err
}

func lookupCreateSecurityGroupID(ctx context.Context, client ec2ClientAPI, vpc string) (string, error) {
	input := &ec2.DescribeSecurityGroupsInput{
		GroupNames: []string{defaultSecurityGroupName},
	}
	securityGroupResponse, lookupErr := client.DescribeSecurityGroups(ctx, input)
	if lookupErr != nil || len(securityGroupResponse.SecurityGroups) == 0 {
		// create the security group
		inputGroup := &ec2.CreateSecurityGroupInput{
			GroupName:   aws.String(defaultSecurityGroupName),
			Description: aws.String("Harness Runner Security Group"),
		}
		// if we have a vpc, we need to use it
		if vpc != "" {
			inputGroup.VpcId = aws.String(vpc)
		}
		createdGroup, createGroupErr := client.CreateSecurityGroup(ctx, inputGroup)
		if createGroupErr != nil {
			return "", fmt.Errorf("failed to create security group: %s. %s", defaultSecurityGroupName, createGroupErr)
		}
		if createdGroup.GroupId == nil {
			return "", fmt.Errorf("created security group has nil GroupId")
		}
		ingress := &ec2.AuthorizeSecurityGroupIngressInput{
			GroupId: createdGroup.GroupId,
			IpPermissions: []types.IpPermission{
				{
					IpProtocol: aws.String("tcp"),
					FromPort:   aws.Int32(int32(lehelper.LiteEnginePort)),
					ToPort:     aws.Int32(int32(lehelper.LiteEnginePort)),
					IpRanges: []types.IpRange{
						{
							CidrIp: aws.String("0.0.0.0/0"),
						},
					},
				},
			},
		}
		_, ingressErr := client.AuthorizeSecurityGroupIngress(ctx, ingress)
		if ingressErr != nil {
			return "", fmt.Errorf("failed to create ingress rules for security group: %s. %s", defaultSecurityGroupName, ingressErr)
		}
		return *createdGroup.GroupId, nil
	}
	sg := &securityGroupResponse.SecurityGroups[0]
	if sg.GroupId == nil {
		return "", fmt.Errorf("security group has nil GroupId")
	}
	return *sg.GroupId, nil
}

// lookup Security Group ID and check it has the correct ingress rules
func checkIngressRules(ctx context.Context, client ec2ClientAPI, groupID string) error {
	input := &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{groupID},
	}
	securityGroupResponse, lookupErr := client.DescribeSecurityGroups(ctx, input)
	if lookupErr != nil {
		return fmt.Errorf("failed to lookup security group: %s. %s", groupID, lookupErr)
	}
	if securityGroupResponse == nil || len(securityGroupResponse.SecurityGroups) == 0 {
		return fmt.Errorf("security group not found: %s", groupID)
	}
	securityGroup := securityGroupResponse.SecurityGroups[0]
	found := false
	for i := range securityGroup.IpPermissions {
		permission := &securityGroup.IpPermissions[i]
		if permission.IpProtocol != nil && *permission.IpProtocol == "tcp" &&
			permission.FromPort != nil && *permission.FromPort == int32(lehelper.LiteEnginePort) &&
			permission.ToPort != nil && *permission.ToPort == int32(lehelper.LiteEnginePort) {
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
func (p *amazonConfig) ReserveCapacity(ctx context.Context, opts *drtypes.InstanceCreateOpts) (*drtypes.CapacityReservation, error) {
	client := p.service

	// Get request-specific configuration without mutating shared state
	reqCfg, err := p.getDynamicConfig(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get dynamic config: %w", err)
	}

	logr := logger.FromContext(ctx).
		WithField("driver", drtypes.Amazon).
		WithField("instance_type", reqCfg.size).
		WithField("availability_zone", reqCfg.availabilityZone).
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
		InstanceType:          aws.String(reqCfg.size),
		InstancePlatform:      types.CapacityReservationInstancePlatform(instancePlatform),
		AvailabilityZone:      aws.String(reqCfg.availabilityZone),
		InstanceCount:         aws.Int32(1),
		EndDateType:           types.EndDateTypeUnlimited,          // No end date
		InstanceMatchCriteria: types.InstanceMatchCriteriaTargeted, // Instances must explicitly target this reservation
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeCapacityReservation,
				Tags:         convertTags(buildHarnessTags(opts)),
			},
		},
	}

	result, err := client.CreateCapacityReservation(ctx, input)
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "InsufficientInstanceCapacity" {
				logr.WithError(err).Errorln("amazon: insufficient capacity available")
				return nil, &ierrors.ErrCapacityUnavailable{Driver: p.DriverName()}
			}
		}
		logr.WithError(err).Errorln("amazon: failed to create capacity reservation")
		return nil, &ierrors.ErrCapacityUnavailable{Driver: p.DriverName()}
	}

	if result.CapacityReservation == nil || result.CapacityReservation.CapacityReservationId == nil {
		logr.Errorln("amazon: capacity reservation created but missing ID")
		return nil, &ierrors.ErrCapacityUnavailable{Driver: p.DriverName()}
	}

	reservationID := aws.ToString(result.CapacityReservation.CapacityReservationId)

	logr.WithField("reservation_id", reservationID).Infoln("amazon: capacity reservation created successfully")

	return &drtypes.CapacityReservation{
		StageID:       "", // Will be set by the caller
		PoolName:      opts.PoolName,
		InstanceID:    "",
		ReservationID: reservationID,
		CreatedAt:     time.Now().Unix(),
	}, nil
}

// DestroyCapacity destroys capacity for a VM
func (p *amazonConfig) DestroyCapacity(ctx context.Context, capacity *drtypes.CapacityReservation) (err error) {
	if capacity == nil || capacity.ReservationID == "" {
		return fmt.Errorf("invalid capacity reservation: missing reservation ID")
	}

	client := p.service

	logr := logger.FromContext(ctx).
		WithField("driver", drtypes.Amazon).
		WithField("reservation", capacity.ReservationID).
		WithField("pool", capacity.PoolName)

	logr.Debugln("amazon: deleting capacity reservation")

	// Cancel the capacity reservation
	input := &ec2.CancelCapacityReservationInput{
		CapacityReservationId: aws.String(capacity.ReservationID),
	}

	_, err = client.CancelCapacityReservation(ctx, input)
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			// If the reservation doesn't exist, consider it already deleted
			if apiErr.ErrorCode() == "InvalidCapacityReservationId.NotFound" {
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

// buildRunInstancesInput constructs the EC2 RunInstancesInput with all configuration
func (p *amazonConfig) buildRunInstancesInput(
	resolvedAMI string,
	userData string,
	reqCfg *requestConfig,
	tags map[string]string,
	volumeTags map[string]string,
	opts *drtypes.InstanceCreateOpts,
) *ec2.RunInstancesInput {
	var iamProfile *types.IamInstanceProfileSpecification
	if p.iamProfileArn != "" {
		iamProfile = &types.IamInstanceProfileSpecification{
			Arn: aws.String(p.iamProfileArn),
		}
	}

	in := &ec2.RunInstancesInput{
		ImageId:            aws.String(resolvedAMI),
		InstanceType:       types.InstanceType(reqCfg.size),
		Placement:          &types.Placement{AvailabilityZone: aws.String(reqCfg.availabilityZone)},
		MinCount:           aws.Int32(1),
		MaxCount:           aws.Int32(1),
		IamInstanceProfile: iamProfile,
		UserData: aws.String(
			base64.StdEncoding.EncodeToString(
				[]byte(userData),
			),
		),
		NetworkInterfaces: []types.InstanceNetworkInterfaceSpecification{
			{
				AssociatePublicIpAddress: aws.Bool(p.allocPublicIP),
				DeviceIndex:              aws.Int32(0),
				SubnetId:                 aws.String(reqCfg.subnet),
				Groups:                   p.groups,
			},
		},
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeInstance,
				Tags:         convertTags(tags),
			},
		},
		BlockDeviceMappings: []types.BlockDeviceMapping{
			{
				DeviceName: aws.String(p.deviceName),
				Ebs: &types.EbsBlockDevice{
					VolumeSize:          aws.Int32(int32(reqCfg.volumeSize)),
					VolumeType:          types.VolumeType(reqCfg.volumeType),
					DeleteOnTermination: aws.Bool(true),
				},
			},
		},
	}

	if len(volumeTags) != 0 {
		in.TagSpecifications = append(in.TagSpecifications, types.TagSpecification{
			ResourceType: types.ResourceTypeVolume,
			Tags:         convertTags(volumeTags),
		})
	}

	if p.keyPairName != "" {
		in.KeyName = aws.String(p.keyPairName)
	}

	if p.volumeType == "io1" {
		for i := range in.BlockDeviceMappings {
			if in.BlockDeviceMappings[i].Ebs != nil {
				in.BlockDeviceMappings[i].Ebs.Iops = aws.Int32(int32(p.volumeIops))
			}
		}
	}

	if p.kmsKeyID != "" {
		for i := range in.BlockDeviceMappings {
			if in.BlockDeviceMappings[i].Ebs != nil {
				in.BlockDeviceMappings[i].Ebs.Encrypted = aws.Bool(true)
				in.BlockDeviceMappings[i].Ebs.KmsKeyId = aws.String(p.kmsKeyID)
			}
		}
	}

	if p.CanHibernate() {
		for i := range in.BlockDeviceMappings {
			if in.BlockDeviceMappings[i].Ebs != nil {
				in.BlockDeviceMappings[i].Ebs.Encrypted = aws.Bool(true)
				if p.kmsKeyID != "" {
					in.BlockDeviceMappings[i].Ebs.KmsKeyId = aws.String(p.kmsKeyID)
				}
			}
		}

		in.HibernationOptions = &types.HibernationOptionsRequest{
			Configured: aws.Bool(true),
		}
	}

	// Use capacity reservation if provided
	if opts.CapacityReservation != nil && opts.CapacityReservation.ReservationID != "" {
		in.CapacityReservationSpecification = &types.CapacityReservationSpecification{
			CapacityReservationTarget: &types.CapacityReservationTarget{
				CapacityReservationId: aws.String(opts.CapacityReservation.ReservationID),
			},
		}
	}

	return in
}

// Create an AWS instance for the pool, it will not perform build specific setup.
//
//nolint:gocyclo
func (p *amazonConfig) Create(ctx context.Context, opts *drtypes.InstanceCreateOpts) (instance *drtypes.Instance, err error) {
	client := p.service
	startTime := time.Now()

	// Get request-specific configuration without mutating shared state
	reqCfg, err := p.getDynamicConfig(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get dynamic config: %w", err)
	}

	resolvedAMI, err := p.GetFullyQualifiedImage(ctx, &opts.VMImageConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get image: %w", err)
	}

	logr := logger.FromContext(ctx).
		WithField("driver", drtypes.Amazon).
		WithField("ami", p.InstanceType()).
		WithField("pool", opts.PoolName).
		WithField("region", p.region).
		WithField("ami", resolvedAMI).
		WithField("size", reqCfg.size).
		WithField("zone", reqCfg.availabilityZone).
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
	if len(p.groups) == 0 {
		return nil, fmt.Errorf("no security groups configured")
	}
	rulesErr := checkIngressRules(ctx, client, p.groups[0])
	if rulesErr != nil {
		return nil, rulesErr
	}

	logr.Traceln("amazon: provisioning VM")

	opts.EnableC4D = p.enableC4D

	userData, err := lehelper.GenerateUserdata(p.userData, opts)
	if err != nil {
		logr.WithError(err).
			Errorln("amazon: [provision] failed to generate user data")
		return nil, err
	}

	// Build the RunInstances input
	in := p.buildRunInstancesInput(resolvedAMI, userData, reqCfg, tags, volumeTags, opts)

	if opts.CapacityReservation != nil && opts.CapacityReservation.ReservationID != "" {
		logr.WithField("reservation_id", opts.CapacityReservation.ReservationID).
			Debugln("amazon: using capacity reservation")
	}

	// Create persistent disks first
	volumes, err := p.createPersistentDisks(ctx, opts, reqCfg)
	if err != nil {
		logr.WithError(err).Errorln("amazon: failed to create persistent disks")
		return nil, err
	}

	runResult, err := client.RunInstances(ctx, in)
	if err != nil {
		logr.WithError(err).Errorln("amazon: [provision] failed to create VMs")
		// Cleanup created volumes before returning
		if len(volumes) > 0 {
			for i := range volumes {
				if volumes[i].VolumeId != nil {
					_, delErr := client.DeleteVolume(ctx, &ec2.DeleteVolumeInput{
						VolumeId: volumes[i].VolumeId,
					})
					if delErr != nil {
						logr.WithError(delErr).WithField("volume_id", *volumes[i].VolumeId).
							Warnln("amazon: failed to cleanup volume after instance creation failure")
					}
				}
			}
		}
		return nil, err
	}

	if runResult == nil || len(runResult.Instances) == 0 {
		return nil, fmt.Errorf("failed to create an AWS EC2 instance")
	}

	awsInstanceID := runResult.Instances[0].InstanceId
	if awsInstanceID == nil {
		return nil, fmt.Errorf("created instance has nil InstanceId")
	}

	logr = logr.
		WithField("id", *awsInstanceID)

	logr.Debugln("amazon: [provision] created instance")

	// poll the amazon endpoint for server updates and exit when a network address is allocated.
	var amazonInstance *types.Instance
	amazonInstance, err = p.pollInstanceIPAddr(ctx, *awsInstanceID, logr)
	if err != nil {
		return nil, err
	}

	// Now that instance is running, attach volumes if any
	if len(volumes) > 0 {
		logr.Debugln("amazon: [provision] attaching volumes to the instance")
		if attachVolumesErr := p.attachVolumes(ctx, *awsInstanceID, volumes, logr); attachVolumesErr != nil {
			logr.WithError(attachVolumesErr).Errorln("amazon: failed to attach volumes, terminating instance")
			// Terminate the instance to avoid resource leak
			_, termErr := client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{*awsInstanceID},
			})
			if termErr != nil {
				logr.WithError(termErr).WithField("instance_id", *awsInstanceID).
					Warnln("amazon: failed to terminate instance after volume attachment failure")
			}
			return nil, attachVolumesErr
		}
	}

	if amazonInstance.InstanceId == nil {
		return nil, fmt.Errorf("instance has nil InstanceId")
	}
	instanceID := *amazonInstance.InstanceId
	instanceIP := p.getIP(amazonInstance)
	launchTime := p.getLaunchTime(amazonInstance)

	labelsBytes, marshalErr := json.Marshal(opts.Labels)
	if marshalErr != nil {
		return &drtypes.Instance{}, fmt.Errorf("scheduler: could not marshal labels: %v, err: %w", opts.Labels, marshalErr)
	}
	gitspacePortMappings := make(map[int]int)
	for _, port := range opts.GitspaceOpts.Ports {
		gitspacePortMappings[port] = port
	}

	instance = &drtypes.Instance{
		ID:                   instanceID,
		Name:                 instanceID,
		Provider:             drtypes.Amazon, // this is driver, though its the old legacy name of provider
		State:                drtypes.StateCreated,
		Pool:                 opts.PoolName,
		Image:                resolvedAMI,
		Zone:                 reqCfg.availabilityZone,
		Region:               p.region,
		Size:                 reqCfg.size,
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

func (p *amazonConfig) Destroy(ctx context.Context, instances []*drtypes.Instance) (err error) {
	return p.DestroyInstanceAndStorage(ctx, instances, nil)
}

// DestroyInstanceAndStorage destroys the server AWS EC2 instances.
func (p *amazonConfig) DestroyInstanceAndStorage(
	ctx context.Context,
	instances []*drtypes.Instance,
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
		WithField("driver", drtypes.Amazon)

	awsIDs := instanceIDs

	var volumesToDelete []string
	if storageCleanupType != nil && *storageCleanupType == storage.Delete {
		vols, findVolumesErr := p.findPersistentVolumes(ctx, instances, logr)
		if findVolumesErr != nil {
			return findVolumesErr
		}
		volumesToDelete = vols
	}

	_, err = client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: awsIDs})
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

func (p *amazonConfig) Logs(ctx context.Context, instanceID string) (string, error) {
	client := p.service

	output, err := client.GetConsoleOutput(ctx, &ec2.GetConsoleOutputInput{
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

func (p *amazonConfig) SetTags(ctx context.Context, instance *drtypes.Instance,
	tags map[string]string) error {
	in := &ec2.CreateTagsInput{
		Resources: []string{instance.ID},
	}
	logr := logger.FromContext(ctx).
		WithField("id", instance.ID).
		WithField("driver", drtypes.Amazon)
	for key, value := range tags {
		in.Tags = append(in.Tags, types.Tag{
			Key:   aws.String(key),
			Value: aws.String(value),
		})
	}
	var err error
	for i := 0; i < tagRetries; i++ {
		_, err = p.service.CreateTags(ctx, in)
		if err == nil {
			return nil
		}
		logr.WithError(err).Warnln("failed to set tags to the instance. retrying")
		time.Sleep(tagRetrySleepMs)
	}

	return err
}

func (p *amazonConfig) Hibernate(ctx context.Context, instanceID, poolName, _ string) error {
	logr := logger.FromContext(ctx).
		WithField("driver", drtypes.Amazon).
		WithField("pool", poolName).
		WithField("instanceID", instanceID).
		WithField("hibernate", p.hibernate)

	client := p.service
	_, err := client.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{instanceID},
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
	// Check if it's a Smithy API error
	var apiErr smithy.APIError
	if errors.As(origErr, &apiErr) {
		// Amazon linux 2 instance return error message on first try:
		// UnsupportedOperation: Instance is not ready to hibernate yet, retry in a few minutes
		if apiErr.ErrorCode() == "UnsupportedOperation" {
			return true
		}
	}

	return false
}

func (p *amazonConfig) Start(ctx context.Context, instance *drtypes.Instance, poolName string) (string, error) {
	client := p.service

	logr := logger.FromContext(ctx).
		WithField("driver", drtypes.Amazon).
		WithField("pool", poolName).
		WithField("instanceID", instance.ID)

	amazonInstance, err := p.getInstance(ctx, instance.ID)
	if err != nil {
		logr.WithError(err).Errorln("aws: failed to find instance status")
		return "", fmt.Errorf("failed to get instance: %w", err)
	}

	state := p.getState(amazonInstance)
	if state == string(types.InstanceStateNameRunning) {
		return p.getIP(amazonInstance), nil
	} else if state == string(types.InstanceStateNameStopping) {
		logr.Traceln("aws: waiting for instance to stop")
		waiter := ec2.NewInstanceStoppedWaiter(client)
		waitErr := waiter.Wait(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instance.ID}}, instanceWaitTimeout)
		if waitErr != nil {
			logr.WithError(waitErr).Warnln("aws: instance failed to stop. Proceeding with starting the instance")
		}
	}

	_, err = client.StartInstances(ctx,
		&ec2.StartInstancesInput{InstanceIds: []string{instance.ID}})
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

// getDynamicConfig extracts request-specific configuration from opts without mutating shared state.
// This prevents race conditions when multiple requests use the same pool concurrently.
func (p *amazonConfig) getDynamicConfig(opts *drtypes.InstanceCreateOpts) (*requestConfig, error) {
	cfg := &requestConfig{
		// Start with pool defaults
		availabilityZone: p.availabilityZone,
		subnet:           p.subnet,
		size:             p.size,
		volumeSize:       p.volumeSize,
		volumeType:       p.volumeType,
	}

	// Override with request-specific values
	if len(opts.Zones) > 0 {
		cfg.availabilityZone = opts.Zones[0]
		// Find matching subnet for the zone
		for _, zoneDetail := range p.zoneDetails {
			if zoneDetail.AvailabilityZone == opts.Zones[0] {
				cfg.subnet = zoneDetail.SubnetID
				break
			}
		}
	} else if len(p.zoneDetails) > 0 {
		// Round-robin selection of availability zone and subnet
		numZones := uint64(len(p.zoneDetails))
		start := time.Now()
		for {
			current := atomic.LoadUint64(&p.zoneIndex)
			next := (current + 1) % numZones
			if atomic.CompareAndSwapUint64(&p.zoneIndex, current, next) {
				zoneDetail := p.zoneDetails[current]
				cfg.availabilityZone = zoneDetail.AvailabilityZone
				cfg.subnet = zoneDetail.SubnetID
				break
			}
			// Fallback to random selection if CAS loop takes too long
			if time.Since(start) > 10*time.Second {
				zoneDetail := p.zoneDetails[rand.Intn(len(p.zoneDetails))] //nolint:gosec
				cfg.availabilityZone = zoneDetail.AvailabilityZone
				cfg.subnet = zoneDetail.SubnetID
				break
			}
		}
	}

	if opts.MachineType != "" {
		cfg.size = opts.MachineType
	}

	if opts.StorageOpts.BootDiskSize != "" {
		diskSize, err := strconv.ParseInt(opts.StorageOpts.BootDiskSize, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse volume size: %w", err)
		}
		cfg.volumeSize = diskSize
	}

	if opts.StorageOpts.BootDiskType != "" {
		cfg.volumeType = opts.StorageOpts.BootDiskType
	}

	return cfg, nil
}

func (p *amazonConfig) getIP(amazonInstance *types.Instance) string {
	if amazonInstance == nil {
		return ""
	}
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

func (p *amazonConfig) getState(amazonInstance *types.Instance) string {
	if amazonInstance == nil || amazonInstance.State == nil {
		return ""
	}

	return string(amazonInstance.State.Name)
}

func (p *amazonConfig) getLaunchTime(amazonInstance *types.Instance) time.Time {
	if amazonInstance == nil || amazonInstance.LaunchTime == nil {
		return time.Now()
	}
	return *amazonInstance.LaunchTime
}

func (p *amazonConfig) pollInstanceIPAddr(ctx context.Context, instanceID string, logr logger.Logger) (*types.Instance, error) {
	client := p.service
	b := backoff.NewExponentialBackOff()
	for {
		duration := b.NextBackOff()
		if duration == b.Stop {
			logr.Errorln("amazon: [provision] failed to obtain IP; terminating it")

			input := &ec2.TerminateInstancesInput{
				InstanceIds: []string{instanceID},
			}
			_, _ = client.TerminateInstances(ctx, input)
			return nil, errors.New("failed to obtain IP address")
		}

		select {
		case <-ctx.Done():
			logr.Warnln("amazon: [provision] instance network deadline exceeded")
			return nil, ctx.Err()

		case <-time.After(duration):
			logr.Traceln("amazon: [provision] checking instance IP address")

			desc, descrErr := client.DescribeInstances(ctx,
				&ec2.DescribeInstancesInput{
					InstanceIds: []string{instanceID},
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
			instanceIP := p.getIP(&instance)

			if instanceIP == "" {
				logr.Traceln("amazon: [provision] instance has no IP yet")
				continue
			}

			return &instance, nil
		}
	}
}

func (p *amazonConfig) getInstance(ctx context.Context, instanceID string) (*types.Instance, error) {
	client := p.service
	response, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return nil, err
	}

	if len(response.Reservations) == 0 {
		return nil, errors.New("amazon: empty reservations in details")
	}

	if len(response.Reservations[0].Instances) == 0 {
		return nil, errors.New("amazon: [provision] empty instances in reservations")
	}

	instance := response.Reservations[0].Instances[0]
	return &instance, nil
}

// getNextDeviceName returns the next available device name starting from /dev/sde.
// We start from 'e' because:
// This ensures we avoid conflicts with built-in device names.
func (p *amazonConfig) getNextDeviceName(index int) string {
	// For HVM instances, AWS recommends /dev/sd[b-z] for EBS data volumes
	deviceLetter := rune('e' + index)
	if deviceLetter > 'z' {
		// If we somehow exceed 'z', wrap around
		deviceLetter = 'e'
	}
	return fmt.Sprintf("/dev/sd%c", deviceLetter)
}

func (p *amazonConfig) waitForVolumeAvailable(ctx context.Context, volumeID string) error {
	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = instanceWaitTimeout

	return backoff.Retry(func() error {
		desc, err := p.service.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
			VolumeIds: []string{volumeID},
		})
		if err != nil {
			return err
		}
		if len(desc.Volumes) == 0 {
			return fmt.Errorf("volume not found")
		}
		if desc.Volumes[0].State != types.VolumeStateAvailable {
			return fmt.Errorf("volume not available yet")
		}
		return nil
	}, b)
}

// cleanupVolumes waits for instances to terminate and handles volume cleanup based on cleanup type
func (p *amazonConfig) cleanupVolumes(ctx context.Context, instanceIDs []string, cleanupType storage.CleanupType, volumeIDs []string) error {
	logr := logger.FromContext(ctx)
	if cleanupType != storage.Delete || len(volumeIDs) == 0 {
		return nil
	}

	// Wait for instances to terminate
	logr.Infoln("aws: waiting for instance termination")
	waiter := ec2.NewInstanceTerminatedWaiter(p.service)
	if err := waiter.Wait(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: instanceIDs,
	}, instanceWaitTimeout); err != nil {
		logr.WithError(err).Errorln("aws: failed waiting for instance termination")
		return err
	}

	// Delete the persistent volumes
	logr.Infoln("aws: deleting persistent volumes")
	for _, volumeID := range volumeIDs {
		_, err := p.service.DeleteVolume(ctx, &ec2.DeleteVolumeInput{
			VolumeId: aws.String(volumeID),
		})
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidVolume.NotFound" {
				logr.WithError(err).Warnf("aws: volume %s not found", volumeID)
				continue
			}
			logr.WithError(err).Errorf("aws: failed to delete volume %s", volumeID)
			return err
		}
	}

	return nil
}

// findPersistentVolumes finds all EBS volumes attached to the given instances that have DeleteOnTermination=false
func (p *amazonConfig) findPersistentVolumes(ctx context.Context, instances []*drtypes.Instance, logr logger.Logger) ([]string, error) {
	// Get all volumes with Name tag matching any of the storage identifiers and DeleteOnTermination=false
	var filters []types.Filter
	// Add filter for non-delete-on-termination volumes
	filters = append(filters, types.Filter{
		Name:   aws.String("attachment.delete-on-termination"),
		Values: []string{"false"},
	})

	// Collect all disk names from all instances
	var diskNames []string
	for _, instance := range instances {
		if instance.StorageIdentifier != "" {
			for _, diskName := range strings.Split(instance.StorageIdentifier, ",") {
				diskName = strings.TrimSpace(diskName)
				if diskName != "" {
					diskNames = append(diskNames, diskName)
				}
			}
		}
	}

	// Add a single filter with all disk names (OR condition)
	if len(diskNames) > 0 {
		filters = append(filters, types.Filter{
			Name:   aws.String("tag:Name"),
			Values: diskNames,
		})
	}

	if len(filters) == 1 {
		// Only have delete-on-termination filter, no disk names to search for
		return nil, nil
	}

	describe, err := p.service.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		Filters: filters,
	})
	if err != nil {
		logr.WithError(err).Errorln("aws: failed to describe volumes")
		return nil, err
	}

	var volumeIDs []string
	for i := range describe.Volumes {
		if describe.Volumes[i].VolumeId != nil {
			volumeIDs = append(volumeIDs, *describe.Volumes[i].VolumeId)
		}
	}

	return volumeIDs, nil
}

func (p *amazonConfig) attachVolumes(ctx context.Context, instanceID string, volumes []types.Volume, logr logger.Logger) error {
	// Attach volumes with retry
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 2 * time.Second // First retry after 2s
	b.Multiplier = 2                    // Next retry after 4s (total 6s)
	b.MaxElapsedTime = instanceWaitTimeout

	for i := range volumes {
		volume := &volumes[i]
		retryCount := 0
		err := backoff.Retry(func() error {
			retryCount++
			logr.Debugf("amazon: attempting to attach volume %s (attempt %d)", *volume.VolumeId, retryCount)
			// Check if instance is running
			desc, err := p.service.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
				InstanceIds: []string{instanceID},
			})
			if err != nil {
				logr.Debugf("amazon: failed to describe instance on attempt %d: %v", retryCount, err)
				return err
			}
			if len(desc.Reservations) == 0 || len(desc.Reservations[0].Instances) == 0 {
				logr.Debugf("amazon: instance not found on attempt %d", retryCount)
				return fmt.Errorf("instance not found")
			}

			instance := &desc.Reservations[0].Instances[0]
			if instance.State == nil {
				logr.Debugf("amazon: instance has nil state on attempt %d", retryCount)
				return fmt.Errorf("instance state is nil")
			}
			state := instance.State.Name
			if state != types.InstanceStateNameRunning {
				logr.Debugf("amazon: instance state is %s on attempt %d", state, retryCount)
				return fmt.Errorf("instance not running yet")
			}

			// Try to attach volume
			deviceName := p.getNextDeviceName(i)
			logr.Debugf("amazon: attempting to attach volume %s to device %s on attempt %d", *volume.VolumeId, deviceName, retryCount)

			_, err = p.service.AttachVolume(ctx, &ec2.AttachVolumeInput{
				Device:     aws.String(deviceName),
				InstanceId: aws.String(instanceID),
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

func (p *amazonConfig) createPersistentDisks(
	ctx context.Context,
	opts *drtypes.InstanceCreateOpts,
	reqCfg *requestConfig,
) ([]types.Volume, error) {
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
	} else {
		return nil, fmt.Errorf("storage size is required when creating persistent disks")
	}

	if volumeSize <= 0 {
		return nil, fmt.Errorf("invalid volume size: %d, must be positive", volumeSize)
	}

	var volumes []types.Volume
	for _, diskName := range storageIdentifiers {
		diskName = strings.TrimSpace(diskName)

		// Create volume
		createVolumeInput := &ec2.CreateVolumeInput{
			AvailabilityZone: aws.String(reqCfg.availabilityZone),
			VolumeType:       types.VolumeType(opts.StorageOpts.Type),
			Size:             aws.Int32(int32(volumeSize)),
			Encrypted:        aws.Bool(true),
			TagSpecifications: []types.TagSpecification{
				{
					ResourceType: types.ResourceTypeVolume,
					Tags: []types.Tag{
						{
							Key:   aws.String("Name"),
							Value: aws.String(diskName),
						},
					},
				},
			},
		}

		// Create the volume
		volumeOutput, err := p.service.CreateVolume(ctx, createVolumeInput)
		if err != nil {
			// Cleanup already created volumes
			for i := range volumes {
				if volumes[i].VolumeId != nil {
					_, _ = p.service.DeleteVolume(ctx, &ec2.DeleteVolumeInput{
						VolumeId: volumes[i].VolumeId,
					})
				}
			}
			return nil, fmt.Errorf("failed to create volume: %w", err)
		}

		if volumeOutput == nil || volumeOutput.VolumeId == nil {
			// Cleanup already created volumes
			for i := range volumes {
				if volumes[i].VolumeId != nil {
					_, _ = p.service.DeleteVolume(ctx, &ec2.DeleteVolumeInput{
						VolumeId: volumes[i].VolumeId,
					})
				}
			}
			return nil, fmt.Errorf("CreateVolume returned nil VolumeId")
		}

		// Wait for volume to be available
		if err := p.waitForVolumeAvailable(ctx, *volumeOutput.VolumeId); err != nil {
			return nil, fmt.Errorf("error waiting for volume to become available: %w", err)
		}

		volume := types.Volume{
			VolumeId:         volumeOutput.VolumeId,
			Size:             volumeOutput.Size,
			VolumeType:       volumeOutput.VolumeType,
			State:            volumeOutput.State,
			AvailabilityZone: volumeOutput.AvailabilityZone,
			CreateTime:       volumeOutput.CreateTime,
			Encrypted:        volumeOutput.Encrypted,
			KmsKeyId:         volumeOutput.KmsKeyId,
			Tags:             volumeOutput.Tags,
		}
		volumes = append(volumes, volume)
	}

	return volumes, nil
}

func getInstanceName(runner, pool, gitspaceConfigIdentifier string) string {
	if gitspaceConfigIdentifier != "" {
		return gitspaceConfigIdentifier
	}
	return fmt.Sprintf("%s-%s-%s", runner, pool, uniuri.NewLen(8)) //nolint:mnd
}
