package amazon

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/lehelper"
	itypes "github.com/drone-runners/drone-runner-aws/internal/types"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/cenkalti/backoff/v4"
)

// config is a struct that implements drivers.Pool interface
type config struct {
	spotInstance     bool
	region           string
	availabilityZone string
	retries          int

	accessKeyID     string
	secretAccessKey string
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
	volumeIops    int64
	deviceName    string
	iamProfileArn string
	tags          map[string]string // user defined tags
	hibernate     bool

	service *ec2.EC2
}

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
			config.Credentials = credentials.NewStaticCredentials(p.accessKeyID, p.secretAccessKey, "")
		}
		mySession := session.Must(session.NewSession())
		p.service = ec2.New(mySession, config)
	}
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

func (p *config) CanHibernate() bool {
	return p.hibernate
}

const (
	defaultSecurityGroupName = "harness-runner"
)

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

// Create an AWS instance for the pool, it will not perform build specific setup.
func (p *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	client := p.service
	startTime := time.Now()
	logr := logger.FromContext(ctx).
		WithField("driver", types.Amazon).
		WithField("ami", p.InstanceType()).
		WithField("pool", opts.PoolName).
		WithField("region", p.region).
		WithField("image", p.image).
		WithField("size", p.size).
		WithField("hibernate", p.CanHibernate())
	var name = fmt.Sprintf(opts.RunnerName+"-"+opts.PoolName+"-%d", startTime.Unix())
	var tags = map[string]string{
		"Name": name,
	}
	// add user defined tags
	for k, v := range p.tags {
		tags[k] = v
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

	in := &ec2.RunInstancesInput{
		ImageId:            aws.String(p.image),
		InstanceType:       aws.String(p.size),
		Placement:          &ec2.Placement{AvailabilityZone: aws.String(p.availabilityZone)},
		MinCount:           aws.Int64(1),
		MaxCount:           aws.Int64(1),
		IamInstanceProfile: iamProfile,
		UserData: aws.String(
			base64.StdEncoding.EncodeToString(
				[]byte(lehelper.GenerateUserdata(p.userData, opts)),
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
	if p.keyPairName != "" {
		in.KeyName = aws.String(p.keyPairName)
	}

	if p.volumeType == "io1" {
		for _, blockDeviceMapping := range in.BlockDeviceMappings {
			blockDeviceMapping.Ebs.Iops = aws.Int64(p.volumeIops)
		}
	}

	if p.CanHibernate() {
		for _, blockDeviceMapping := range in.BlockDeviceMappings {
			blockDeviceMapping.Ebs.Encrypted = aws.Bool(true)
		}

		in.HibernationOptions = &ec2.HibernationOptionsRequest{
			Configured: aws.Bool(true),
		}
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

	instanceID := *amazonInstance.InstanceId
	instanceIP := p.getIP(amazonInstance)
	launchTime := p.getLaunchTime(amazonInstance)

	instance = &types.Instance{
		ID:           instanceID,
		Name:         instanceID,
		Provider:     types.Amazon, // this is driver, though its the old legacy name of provider
		State:        types.StateCreated,
		Pool:         opts.PoolName,
		Image:        p.image,
		Zone:         p.availabilityZone,
		Region:       p.region,
		Size:         p.size,
		Platform:     opts.Platform,
		Address:      instanceIP,
		CACert:       opts.CACert,
		CAKey:        opts.CAKey,
		TLSCert:      opts.TLSCert,
		TLSKey:       opts.TLSKey,
		Started:      launchTime.Unix(),
		Updated:      time.Now().Unix(),
		IsHibernated: false,
		Port:         lehelper.LiteEnginePort,
	}
	logr.
		WithField("ip", instanceIP).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("amazon: [provision] complete")

	return instance, nil
}

// Destroy destroys the server AWS EC2 instances.
func (p *config) Destroy(ctx context.Context, instanceIDs ...string) (err error) {
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

	_, err = client.TerminateInstances(&ec2.TerminateInstancesInput{InstanceIds: awsIDs})
	if err != nil {
		err = fmt.Errorf("failed to terminate instances: %v", err)
		logr.Error(err)
		return err
	}

	logr.Traceln("amazon: VM terminated")
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

func (p *config) Hibernate(ctx context.Context, instanceID, poolName string) error {
	logr := logger.FromContext(ctx).
		WithField("driver", types.Amazon).
		WithField("pool", poolName).
		WithField("instanceID", instanceID)

	client := p.service
	_, err := client.StopInstancesWithContext(ctx, &ec2.StopInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
		Hibernate:   aws.Bool(true),
	})
	if err != nil {
		logr.WithError(err).
			Errorln("aws: failed to hibernate the VM")
		if isHibernateRetryable(err) {
			return &itypes.RetryableError{Msg: err.Error()}
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

func (p *config) Start(ctx context.Context, instanceID, poolName string) (string, error) {
	client := p.service

	logr := logger.FromContext(ctx).
		WithField("driver", types.Amazon).
		WithField("pool", poolName).
		WithField("instanceID", instanceID)

	amazonInstance, err := p.getInstance(ctx, instanceID)
	if err != nil {
		logr.WithError(err).Errorln("aws: failed to find instance status")
	}

	state := p.getState(amazonInstance)
	if state == ec2.InstanceStateNameRunning {
		return p.getIP(amazonInstance), nil
	} else if state == ec2.InstanceStateNameStopping {
		logr.Traceln("aws: waiting for instance to stop")
		waitErr := client.WaitUntilInstanceStoppedWithContext(ctx, &ec2.DescribeInstancesInput{InstanceIds: []*string{aws.String(instanceID)}})
		if waitErr != nil {
			logr.WithError(waitErr).Warnln("aws: instance failed to stop. Proceeding with starting the instance")
		}
	}

	_, err = client.StartInstancesWithContext(ctx,
		&ec2.StartInstancesInput{InstanceIds: []*string{aws.String(instanceID)}})
	if err != nil {
		logr.WithError(err).
			Errorln("aws: failed to start VMs")
		return "", err
	}
	logr.Traceln("amazon: VM started")

	awsInstance, err := p.pollInstanceIPAddr(ctx, instanceID, logr)
	if err != nil {
		logr.WithError(err).
			Errorln("aws: failed to retrieve IP address of the VM")
		return "", err
	}
	return p.getIP(awsInstance), nil
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
