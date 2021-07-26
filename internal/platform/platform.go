// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

// Package platform contains code to provision and destroy server
// instances on the Digital Ocean cloud platform.
package platform

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/drone/runner-go/logger"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
)

const maxRetries = 10
const poolString = "pool"
const statusString = "status"

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
		ID     string
		IP     string
		Status string
	}

	AwsPools struct {
		Pools map[string]Pool
	}

	Pool struct {
		Instances []Instance
	}
)

// Provision provisions the server instance.
func Create(ctx context.Context, creds Credentials, args *ProvisionArgs) (*Instance, error) { //nolint:funlen // its complex but standard
	client := getClient(creds.Region, creds.Client, creds.Secret)

	var iamProfile *ec2.IamInstanceProfileSpecification

	if args.IamProfileArn != "" {
		iamProfile = &ec2.IamInstanceProfileSpecification{
			Arn: aws.String(args.IamProfileArn),
		}
	}

	tags := createCopy(args.Tags)
	tags["name"] = args.Name

	in := &ec2.RunInstancesInput{
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

	logr := logger.FromContext(ctx).
		WithField("region", args.Region).
		WithField("image", args.Image).
		WithField("size", args.Size)
	logr.Debug("instance create")

	results, err := client.RunInstances(in)
	if err != nil {
		logr.WithError(err).
			Error("instance create failed")
		return nil, err
	}

	amazonInstance := results.Instances[0]

	instance := &Instance{
		ID: *amazonInstance.InstanceId,
	}

	logr.WithField("id", instance.ID).
		Infoln("instance create success")

	// poll the amazon endpoint for server updates
	// and exit when a network address is allocated.
	interval := time.Duration(0)
poller:
	for {
		select {
		case <-ctx.Done():
			logr.WithField("name", instance.ID).
				Debugln("instance network deadline exceeded")

			return instance, ctx.Err()
		case <-time.After(interval):
			interval = time.Minute

			logr.WithField("name", instance.ID).
				Debugln("check instance network")

			desc, err := client.DescribeInstances(
				&ec2.DescribeInstancesInput{
					InstanceIds: []*string{
						amazonInstance.InstanceId,
					},
				},
			)
			if err != nil {
				logr.WithError(err).
					Warnln("instance details failed")
				continue
			}

			if len(desc.Reservations) == 0 {
				logr.Warnln("empty reservations in details")
				continue
			}
			if len(desc.Reservations[0].Instances) == 0 {
				logr.Warnln("empty instances in reservations")
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

	logr.
		WithField("id", instance.ID).
		WithField("ip", instance.IP).
		Debugln("instance network ready")

	return instance, nil
}

// Destroy destroys the server instance.
func Destroy(ctx context.Context, creds Credentials, instance *Instance) error {
	client := getClient(creds.Region, creds.Client, creds.Secret)

	logr := logger.FromContext(ctx).
		WithField("id", instance.ID).
		WithField("ip", instance.IP)

	logr.Debugln("terminate instance")

	input := &ec2.TerminateInstancesInput{
		InstanceIds: []*string{
			aws.String(instance.ID),
		},
	}
	_, err := client.TerminateInstances(input)
	if err != nil {
		logr.WithError(err).
			Errorln("cannot terminate instance")
		return err
	}

	logr.Debugln("terminated")
	return nil
}

func GetPools(ctx context.Context, creds Credentials) (awspools *ec2.DescribeInstancesOutput, err error) {
	client := getClient(creds.Region, creds.Client, creds.Secret)
	params := &ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			{
				Name: aws.String("instance-state-name"),
				Values: []*string{
					aws.String("running"),
				},
			},
		},
	}
	return client.DescribeInstances(params)
}

func TagInstance(ctx context.Context, creds Credentials, instance, key, value string) (err error) {
	client := getClient(creds.Region, creds.Client, creds.Secret)
	input := &ec2.CreateTagsInput{
		Resources: []*string{
			aws.String(instance),
		},
		Tags: []*ec2.Tag{{Key: aws.String(key), Value: aws.String(value)}},
	}

	_, tagErr := client.CreateTags(input)
	if tagErr != nil {
		return tagErr
	}
	return nil
}

func CleanPools(ctx context.Context, creds Credentials) (err error) {
	poolFullyCleaned := true
	logr := logger.FromContext(ctx)
	logr.Debugln("clean pools")
	resp, err := GetPools(ctx, creds)
	if err != nil {
		logr.WithError(err).
			Errorln("cannot get pools from aws")
		return err
	}
	// does any of the machines have the tags we want
	for idx := range resp.Reservations {
		for _, inst := range resp.Reservations[idx].Instances {
			instanceFound := false
			for _, keys := range inst.Tags {
				if *keys.Key == "drone" {
					if *keys.Value == "drone-runner-aws" {
						instanceFound = true
					}
				}
			}
			if instanceFound {
				destInstance := Instance{
					ID: *inst.InstanceId,
					IP: *inst.PublicIpAddress,
				}
				destErr := Destroy(ctx, creds, &destInstance)
				if destErr != nil {
					poolFullyCleaned = false
					logr.WithError(err).
						WithField("ID", inst.InstanceId).
						Errorln("unable to terminate instance")
				}
			}
		}
	}
	if poolFullyCleaned {
		return nil
	}
	return fmt.Errorf("unable to fully clean the pool, check the logs")
}

func PoolCountFree(ctx context.Context, creds Credentials, poolName string, awsMutex *sync.Mutex) (free int, err error) {
	logr := logger.FromContext(ctx).
		WithField("pool", poolName)

	logr.Debugln("check pool")
	awsMutex.Lock()
	defer awsMutex.Unlock()
	resp, err := GetPools(ctx, creds)
	if err != nil {
		logr.WithError(err).
			Errorln("cannot get pools from aws")
		return 0, err
	}
	// does any of the machines have the tags we want
	for idx := range resp.Reservations {
		for _, inst := range resp.Reservations[idx].Instances {
			poolFound := false
			instanceFree := true
			for _, keys := range inst.Tags {
				if *keys.Key == poolString {
					if *keys.Value == poolName {
						poolFound = true
					}
				}
				if *keys.Key == statusString {
					instanceFree = false
				}
			}
			if poolFound && instanceFree {
				free++
			}
		}
	}
	return free, nil
}

// TryPool will look for an instance in the pool, returning its is and ip. otherwise it return an error
func TryPool(ctx context.Context, creds Credentials, poolName string, awsMutex *sync.Mutex) (found bool, instanceID, instanceIP string, err error) {
	logr := logger.FromContext(ctx).
		WithField("pool", poolName)

	logr.Debugln("try pool")
	awsMutex.Lock()
	defer awsMutex.Unlock()
	resp, poolErr := GetPools(ctx, creds)

	if poolErr != nil {
		logr.WithError(poolErr).
			Errorln("cannot get pools from aws")
		return false, "", "", poolErr
	}

	// do any of the machines have the tags we want
	for idx := range resp.Reservations {
		for _, inst := range resp.Reservations[idx].Instances {
			poolFound := false
			instanceFree := true
			for _, keys := range inst.Tags {
				if *keys.Key == poolString {
					if *keys.Value == poolName {
						poolFound = true
					}
				}
				if *keys.Key == statusString {
					instanceFree = false
				}
			}
			if poolFound && instanceFree {
				found = true
				instanceID = *inst.InstanceId
				instanceIP = *inst.PublicIpAddress
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		logr.Debugln("no free instances")
		return false, "", "", nil
	}

	logr.Debugln("found an instance")
	tagErr := TagInstance(ctx, creds, instanceID, "status", "build in progress")
	if tagErr != nil {
		logr.WithError(tagErr).
			WithField("instance", instanceID).
			Errorln("cannot tag instance")
		return false, "", "", tagErr
	}
	return true, instanceID, instanceIP, nil
}

// checks that we can log into EC2, and the regions respond
func Ping(ctx context.Context, creds Credentials) error {
	client := getClient(creds.Region, creds.Client, creds.Secret)

	allRegions := true
	input := &ec2.DescribeRegionsInput{
		AllRegions: &allRegions,
	}
	_, err := client.DescribeRegions(input)

	return err
}

func getClient(region, client, secret string) *ec2.EC2 {
	config := aws.NewConfig()
	config = config.WithRegion(region)
	config = config.WithMaxRetries(maxRetries)
	config = config.WithCredentials(
		credentials.NewStaticCredentials(client, secret, ""),
	)
	mySession := session.Must(session.NewSession())

	// Create a EC2 client from just a session.
	_ = ec2.New(mySession)
	return ec2.New(mySession, config)
}
