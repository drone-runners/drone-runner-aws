package amazon

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/userdata"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/cenkalti/backoff/v4"
)

func (p *provider) ProviderName() string {
	return string(types.ProviderAmazon)
}

func (p *provider) InstanceType() string {
	return p.image
}

func (p *provider) RootDir() string {
	return p.rootDir
}

func (p *provider) CanHibernate() bool {
	return p.hibernate
}

// Ping checks that we can log into EC2, and the regions respond
func (p *provider) Ping(ctx context.Context) error {
	client := p.service

	allRegions := true
	input := &ec2.DescribeRegionsInput{
		AllRegions: &allRegions,
	}
	_, err := client.DescribeRegionsWithContext(ctx, input)

	return err
}

// Create an AWS instance for the pool, it will not perform build specific setup.
func (p *provider) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	client := p.service

	logr := logger.FromContext(ctx).
		WithField("provider", types.ProviderAmazon).
		WithField("ami", p.InstanceType()).
		WithField("pool", opts.PoolName).
		WithField("region", p.region).
		WithField("image", p.image).
		WithField("size", p.size).
		WithField("hibernate", p.CanHibernate())
	var name = fmt.Sprintf(opts.RunnerName+"-"+opts.PoolName+"-%d", time.Now().Unix())

	var tags = map[string]string{
		"Name": name,
	}
	// create the instance
	startTime := time.Now()

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
				[]byte(userdata.Generate(p.userData, opts)),
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
			Errorln("amazon: [provision] failed to list VMs")
		return
	}

	if len(runResult.Instances) == 0 {
		err = fmt.Errorf("failed to create an AWS EC2 instance")
		return
	}

	awsInstanceID := runResult.Instances[0].InstanceId

	logr = logr.
		WithField("id", *awsInstanceID)

	logr.Debugln("amazon: [provision] created instance")

	// poll the amazon endpoint for server updates
	// and exit when a network address is allocated.
	var amazonInstance *ec2.Instance
	amazonInstance, err = p.pollInstanceIPAddr(ctx, *awsInstanceID, logr)
	if err != nil {
		return
	}

	instanceID := *amazonInstance.InstanceId
	instanceIP := p.getIP(amazonInstance)
	launchTime := p.getLaunchTime(amazonInstance)

	instance = &types.Instance{
		ID:           instanceID,
		Name:         instanceID,
		Provider:     types.ProviderAmazon,
		State:        types.StateCreated,
		Pool:         opts.PoolName,
		Image:        p.image,
		Zone:         p.availabilityZone,
		Region:       p.region,
		Size:         p.size,
		Platform:     opts.OS,
		Arch:         opts.Arch,
		Address:      instanceIP,
		CACert:       opts.CACert,
		CAKey:        opts.CAKey,
		TLSCert:      opts.TLSCert,
		TLSKey:       opts.TLSKey,
		Started:      launchTime.Unix(),
		Updated:      time.Now().Unix(),
		IsHibernated: false,
	}
	logr.
		WithField("ip", instanceIP).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("amazon: [provision] complete")

	return // nolint:nakedret
}

// Destroy destroys the server AWS EC2 instances.
func (p *provider) Destroy(ctx context.Context, instanceIDs ...string) (err error) {
	if len(instanceIDs) == 0 {
		return
	}

	client := p.service

	logr := logger.FromContext(ctx).
		WithField("id", instanceIDs).
		WithField("provider", types.ProviderAmazon)

	awsIDs := make([]*string, len(instanceIDs))
	for i, instanceID := range instanceIDs {
		awsIDs[i] = aws.String(instanceID)
	}

	_, err = client.TerminateInstances(&ec2.TerminateInstancesInput{InstanceIds: awsIDs})
	if err != nil {
		logr.WithError(err).
			Errorln("amazon: failed to terminate VMs")
		return
	}

	logr.Traceln("amazon: VM terminated")
	return
}

func (p *provider) Hibernate(ctx context.Context, instanceID, poolName string) error {
	logr := logger.FromContext(ctx).
		WithField("provider", types.ProviderAmazon).
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
		return err
	}

	logr.Traceln("amazon: VM hibernated")
	return nil
}

func (p *provider) Start(ctx context.Context, instanceID, poolName string) (string, error) {
	client := p.service

	logr := logger.FromContext(ctx).
		WithField("provider", types.ProviderAmazon).
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

func (p *provider) getIP(amazonInstance *ec2.Instance) string {
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

func (p *provider) getState(amazonInstance *ec2.Instance) string {
	if amazonInstance.State == nil {
		return ""
	}

	if amazonInstance.State.Name == nil {
		return ""
	}

	return *amazonInstance.State.Name
}

func (p *provider) getLaunchTime(amazonInstance *ec2.Instance) time.Time {
	if amazonInstance.LaunchTime == nil {
		return time.Now()
	}
	return *amazonInstance.LaunchTime
}

func (p *provider) pollInstanceIPAddr(ctx context.Context, instanceID string, logr logger.Logger) (*ec2.Instance, error) {
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

func (p *provider) getInstance(ctx context.Context, instanceID string) (*ec2.Instance, error) {
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
