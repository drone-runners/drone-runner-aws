package amazon

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone/runner-go/logger"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

const (
	cloud = "amazon"
)

func (p *provider) GetProviderName() string {
	return cloud
}

func (p *provider) GetName() string {
	return p.name
}

func (p *provider) GetInstanceType() string {
	return p.image
}

func (p *provider) GetOS() string {
	return p.os
}

func (p *provider) GetRootDir() string {
	return p.rootDir
}

func (p *provider) GetMaxSize() int {
	return p.limit
}

func (p *provider) GetMinSize() int {
	return p.pool
}

// CheckProvider checks that we can log into EC2, and the regions respond
func (p *provider) CheckProvider(ctx context.Context) error {
	client := p.service

	allRegions := true
	input := &ec2.DescribeRegionsInput{
		AllRegions: &allRegions,
	}
	_, err := client.DescribeRegionsWithContext(ctx, input)

	return err
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

func (p *provider) getLaunchTime(amazonInstance *ec2.Instance) time.Time {
	if amazonInstance.LaunchTime == nil {
		return time.Now()
	}
	return *amazonInstance.LaunchTime
}

func (p *provider) getTags(amazonInstance *ec2.Instance) map[string]string {
	if len(amazonInstance.Tags) == 0 {
		return nil
	}

	tags := make(map[string]string, len(amazonInstance.Tags))

	for _, awsTag := range amazonInstance.Tags {
		if awsTag == nil || awsTag.Key == nil || awsTag.Value == nil {
			continue
		}

		tags[*awsTag.Key] = *awsTag.Value
	}

	return tags
}

// Create an AWS instance for the pool, it will not perform build specific setup.
func (p *provider) Create(ctx context.Context, tagAsInUse bool) (instance *drivers.Instance, err error) {
	client := p.service

	logr := logger.FromContext(ctx).
		WithField("provider", cloud).
		WithField("ami", p.GetInstanceType()).
		WithField("pool", p.name).
		WithField("region", p.region).
		WithField("image", p.image).
		WithField("size", p.size)

	tags := createCopy(p.tags)
	tags[drivers.TagRunner] = drivers.RunnerName
	tags[drivers.TagPool] = p.name
	tags[drivers.TagCreator] = p.runnerName
	if tagAsInUse {
		tags[drivers.TagStatus] = drivers.TagStatusValue
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
				[]byte(p.userData),
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

	attempt := 0
	intervals := []time.Duration{0, 15 * time.Second, 30 * time.Second, 45 * time.Second, time.Minute}

	for {
		var interval time.Duration
		if attempt >= len(intervals) {
			interval = intervals[len(intervals)-1]
		} else {
			interval = intervals[attempt]
		}

		const attemptCount = 20
		attempt++

		if attempt == attemptCount {
			logr.Errorln("amazon: [provision] failed to obtain IP; terminating it")

			input := &ec2.TerminateInstancesInput{
				InstanceIds: []*string{awsInstanceID},
			}
			_, _ = client.TerminateInstancesWithContext(ctx, input)

			err = errors.New("failed to obtain IP address")
			return
		}

		select {
		case <-ctx.Done():
			logr.Warnln("amazon: [provision] instance network deadline exceeded")

			err = ctx.Err()
			return

		case <-time.After(interval):
			logr.Traceln("amazon: [provision] check instance network")

			desc, descrErr := client.DescribeInstancesWithContext(ctx,
				&ec2.DescribeInstancesInput{
					InstanceIds: []*string{awsInstanceID},
				},
			)
			if descrErr != nil {
				logr.WithError(err).Warnln("amazon: [provision] instance details failed")
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

			amazonInstance := desc.Reservations[0].Instances[0]
			instanceID := *amazonInstance.InstanceId
			instanceIP := p.getIP(amazonInstance)
			instanceTags := p.getTags(amazonInstance)
			launchTime := p.getLaunchTime(amazonInstance)

			if instanceIP == "" {
				logr.Traceln("amazon: [provision] instance has no IP yet")
				continue
			}

			instance = &drivers.Instance{
				ID:        instanceID,
				IP:        instanceIP,
				Tags:      instanceTags,
				StartedAt: launchTime,
			}

			logr.
				WithField("ip", instanceIP).
				WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
				Debugln("amazon: [provision] complete")

			return
		}
	}
}

func (p *provider) List(ctx context.Context) (busy, free []drivers.Instance, err error) {
	client := p.service

	logr := logger.FromContext(ctx).
		WithField("provider", cloud).
		WithField("pool", p.name)

	params := &ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("instance-state-name"),
				Values: []*string{aws.String("pending"), aws.String("running")},
			},
			{
				Name:   aws.String("tag:" + drivers.TagCreator),
				Values: []*string{aws.String(p.runnerName)},
			},
			{
				Name:   aws.String("tag:" + drivers.TagRunner),
				Values: []*string{aws.String(drivers.RunnerName)},
			},
			{
				Name:   aws.String("tag:" + drivers.TagPool),
				Values: []*string{aws.String(p.name)},
			},
		},
	}

	describeRes, err := client.DescribeInstancesWithContext(ctx, params)
	if err != nil {
		logr.WithError(err).
			Errorln("amazon: failed to list VMs")
		return
	}

	for _, awsReservation := range describeRes.Reservations {
		for _, awsInstance := range awsReservation.Instances {
			id := *awsInstance.InstanceId
			ip := p.getIP(awsInstance)
			tags := p.getTags(awsInstance)
			launchTime := p.getLaunchTime(awsInstance)

			inst := drivers.Instance{
				ID:        id,
				IP:        ip,
				Tags:      tags,
				StartedAt: launchTime,
			}

			var isBusy bool
			for _, keys := range awsInstance.Tags {
				if *keys.Key == drivers.TagStatus {
					isBusy = *keys.Value == drivers.TagStatusValue
					break
				}
			}
			if isBusy {
				busy = append(busy, inst)
			} else {
				free = append(free, inst)
			}
		}
	}

	logr.
		WithField("free", len(free)).
		WithField("busy", len(busy)).
		Traceln("amazon: list VMs")

	return
}

func (p *provider) GetUsedInstanceByTag(ctx context.Context, tag, value string) (inst *drivers.Instance, err error) {
	client := p.service

	logr := logger.FromContext(ctx).
		WithField("provider", cloud).
		WithField("pool", p.name).
		WithField("tag", tag).
		WithField("tag-value", value)

	params := &ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("instance-state-name"),
				Values: []*string{aws.String("running")},
			},
			{
				Name:   aws.String("tag:" + drivers.TagCreator),
				Values: []*string{aws.String(p.runnerName)},
			},
			{
				Name:   aws.String("tag:" + drivers.TagRunner),
				Values: []*string{aws.String(drivers.RunnerName)},
			},
			{
				Name:   aws.String("tag:" + drivers.TagPool),
				Values: []*string{aws.String(p.name)},
			},
			{
				Name:   aws.String("tag:" + drivers.TagStatus),
				Values: []*string{aws.String(drivers.TagStatusValue)},
			},
			{
				Name:   aws.String("tag:" + tag),
				Values: []*string{aws.String(value)},
			},
		},
	}

	describeRes, err := client.DescribeInstancesWithContext(ctx, params)
	if err != nil {
		logr.WithError(err).
			Errorln("amazon: failed to get VM by tag")
		return
	}

	for _, awsReservation := range describeRes.Reservations {
		for _, awsInstance := range awsReservation.Instances {
			id := *awsInstance.InstanceId
			ip := p.getIP(awsInstance)
			tags := p.getTags(awsInstance)
			launchTime := p.getLaunchTime(awsInstance)

			inst = &drivers.Instance{
				ID:        id,
				IP:        ip,
				Tags:      tags,
				StartedAt: launchTime,
			}

			logr.
				WithField("id", inst.ID).
				WithField("ip", inst.IP).
				Traceln("amazon: found VM by tag")

			return
		}
	}

	logr.Traceln("amazon: didn't found VM by tag")

	return
}

func (p *provider) Tag(ctx context.Context, instanceID string, tags map[string]string) (err error) {
	client := p.service

	logr := logger.FromContext(ctx).
		WithField("id", instanceID).
		WithField("provider", cloud)

	awsTags := make([]*ec2.Tag, 0, len(tags))
	for key, value := range tags {
		awsTags = append(awsTags, &ec2.Tag{
			Key:   aws.String(key),
			Value: aws.String(value),
		})
	}

	input := &ec2.CreateTagsInput{
		Resources: []*string{aws.String(instanceID)},
		Tags:      awsTags,
	}

	_, err = client.CreateTagsWithContext(ctx, input)
	if err != nil {
		logr.WithError(err).
			Errorln("amazon: failed to tag VM")
		return
	}

	logr.Traceln("amazon: VM tagged")
	return
}

func (p *provider) TagAsInUse(ctx context.Context, instanceID string) error {
	return p.Tag(ctx, instanceID, map[string]string{drivers.TagStatus: drivers.TagStatusValue})
}

// Destroy destroys the server AWS EC2 instances.
func (p *provider) Destroy(ctx context.Context, instanceIDs ...string) (err error) {
	if len(instanceIDs) == 0 {
		return
	}

	client := p.service

	logr := logger.FromContext(ctx).
		WithField("id", instanceIDs).
		WithField("provider", cloud)

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

	logr.Traceln("amazon: VMs terminated")
	return
}
