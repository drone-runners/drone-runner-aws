// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

// Package platform contains code to provision and destroy server
// instances on the Digital Ocean cloud platform.
package platform

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/drone/runner-go/logger"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
)

type (
	// Credentials provides platform credentials.
	Credentials struct {
		Client string
		Secret string
		Region string
	}

	// ProvisionArgs provides arguments to provision instances.
	ProvisionArgs struct {
		Key           string
		Image         string
		Name          string
		Region        string
		Size          string
		Subnet        string
		Groups        []string
		Device        string
		PrivateIP     bool
		VolumeType    string
		VolumeSize    int64
		VolumeIops    int64
		IamProfileArn string
		Userdata      string
		Tags          map[string]string
	}

	// Instance represents a provisioned server instance.
	Instance struct {
		ID string
		IP string
	}
)

// Provision provisions the server instance.
func Create(ctx context.Context, creds Credentials, args ProvisionArgs) (*Instance, error) {
	client := getClient(ctx, creds.Region, creds.Client, creds.Secret)

	var iamProfile *ec2.IamInstanceProfileSpecification

	if args.IamProfileArn != "" {
		iamProfile = &ec2.IamInstanceProfileSpecification{
			Arn: aws.String(args.IamProfileArn),
		}
	}

	tags := createCopy(args.Tags)
	tags["Name"] = args.Name

	in := &ec2.RunInstancesInput{
		KeyName:            aws.String(args.Key),
		ImageId:            aws.String(args.Image),
		InstanceType:       aws.String(args.Size),
		MinCount:           aws.Int64(1),
		MaxCount:           aws.Int64(1),
		IamInstanceProfile: iamProfile,
		UserData: aws.String(
			base64.StdEncoding.EncodeToString(
				[]byte(args.Userdata),
			),
		),
		NetworkInterfaces: []*ec2.InstanceNetworkInterfaceSpecification{
			{
				AssociatePublicIpAddress: aws.Bool(!args.PrivateIP),
				DeviceIndex:              aws.Int64(0),
				SubnetId:                 aws.String(args.Subnet),
				Groups:                   aws.StringSlice(args.Groups),
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
				DeviceName: aws.String(args.Device),
				Ebs: &ec2.EbsBlockDevice{
					VolumeSize:          aws.Int64(args.VolumeSize),
					VolumeType:          aws.String(args.VolumeType),
					DeleteOnTermination: aws.Bool(true),
				},
			},
		},
	}

	if args.VolumeType == "io1" {
		for _, blockDeviceMapping := range in.BlockDeviceMappings {
			blockDeviceMapping.Ebs.Iops = aws.Int64(args.VolumeIops)
		}
	}

	logger := logger.FromContext(ctx).
		WithField("region", args.Region).
		WithField("image", args.Image).
		WithField("size", args.Size).
		WithField("name", args.Name)

	logger.Debug("instance create")

	results, err := client.RunInstances(in)
	if err != nil {
		logger.WithError(err).
			Error("instance create failed")
		return nil, err
	}

	amazonInstance := results.Instances[0]

	instance := &Instance{
		ID: *amazonInstance.InstanceId,
	}

	logger.WithField("id", instance.ID).
		Infoln("instance create success")

	// poll the amazon endpoint for server updates
	// and exit when a network address is allocated.
	interval := time.Duration(0)
poller:
	for {
		select {
		case <-ctx.Done():
			logger.WithField("name", instance.ID).
				Debugln("instance network deadline exceeded")

			return instance, ctx.Err()
		case <-time.After(interval):
			interval = time.Minute

			logger.WithField("name", instance.ID).
				Debugln("check instance network")

			desc, err := client.DescribeInstances(
				&ec2.DescribeInstancesInput{
					InstanceIds: []*string{
						amazonInstance.InstanceId,
					},
				},
			)
			if err != nil {
				logger.WithError(err).
					Warnln("instance details failed")
				continue
			}

			if len(desc.Reservations) == 0 {
				logger.Warnln("empty reservations in details")
				continue
			}
			if len(desc.Reservations[0].Instances) == 0 {
				logger.Warnln("empty instances in reservations")
				continue
			}

			amazonInstance = desc.Reservations[0].Instances[0]

			if args.PrivateIP {
				if amazonInstance.PrivateIpAddress != nil {
					instance.IP = *amazonInstance.PrivateIpAddress
					break poller
				}
			}

			if amazonInstance.PublicIpAddress != nil {
				instance.IP = *amazonInstance.PublicIpAddress
				break poller
			}
		}
	}

	logger.
		WithField("id", instance.ID).
		WithField("ip", instance.IP).
		Debugln("instance network ready")

	return instance, nil
}

// Destroy destroys the server instance.
func Destroy(ctx context.Context, creds Credentials, instance *Instance) error {
	client := getClient(ctx, creds.Region, creds.Client, creds.Secret)

	logger := logger.FromContext(ctx).
		WithField("id", instance.ID).
		WithField("ip", instance.IP)

	logger.Debugln("terminate instance")

	input := &ec2.TerminateInstancesInput{
		InstanceIds: []*string{
			aws.String(instance.ID),
		},
	}
	_, err := client.TerminateInstances(input)
	if err != nil {
		logger.WithError(err).
			Errorln("cannot terminate instance")
		return err
	}

	logger.Debugln("terminated")
	return nil
}

// checks that we can log into EC2, and the regions respond
func Ping(ctx context.Context, creds Credentials) error {
	client := getClient(ctx, creds.Region, creds.Client, creds.Secret)

	allRegions := true
	input := &ec2.DescribeRegionsInput{
		AllRegions: &allRegions,
	}
	_, err := client.DescribeRegions(input)

	return err
}

func getClient(ctx context.Context, region, client, secret string) *ec2.EC2 {
	config := aws.NewConfig()
	config = config.WithRegion(region)
	config = config.WithMaxRetries(10)
	config = config.WithCredentials(
		credentials.NewStaticCredentials(client, secret, ""),
	)
	return ec2.New(session.New(config))
}
