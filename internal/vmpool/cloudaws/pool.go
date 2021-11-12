package cloudaws

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/ssh"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool"

	"github.com/drone/runner-go/logger"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/pkg/sftp"
)

// awsMutex is a global mutex for synchronizing API calls to AWS EC2
var awsMutex *sync.Mutex

// awsPool is a struct that implements vmpool.Pool interface
type awsPool struct {
	name        string
	runnerName  string
	credentials Credentials

	privateKey    string
	iamProfileArn string

	region  string
	os      string
	rootDir string

	// vm instance data
	image         string
	instanceType  string
	user          string
	subnet        string
	groups        []string
	allocPublicIP bool
	device        string
	volumeType    string
	volumeSize    int64
	volumeIops    int64
	defaultTags   map[string]string

	// pool size data
	sizeMin int
	sizeMax int

	// awsMutex is used to synchronize calls to AWS EC2 API
	awsMutex *sync.Mutex
}

const (
	poolString   = "pool"
	statusString = "status"
)

func (p *awsPool) GetName() string {
	return p.name
}

func (p *awsPool) GetInstanceType() string {
	return p.image
}

func (p *awsPool) GetOS() string {
	return p.os
}

func (p *awsPool) GetUser() string {
	return p.user
}

func (p *awsPool) GetPrivateKey() string {
	return p.privateKey
}

func (p *awsPool) GetRootDir() string {
	return p.rootDir
}

func (p *awsPool) GetMinSize() int {
	return p.sizeMin
}

func (p *awsPool) GetMaxSize() int {
	return p.sizeMax
}

// Create creates the server instance.
func (p *awsPool) Create(ctx context.Context) (*vmpool.Instance, error) { //nolint:funlen // its complex but standard
	client := p.credentials.getClient()

	var iamProfile *ec2.IamInstanceProfileSpecification
	if p.iamProfileArn != "" {
		iamProfile = &ec2.IamInstanceProfileSpecification{
			Arn: aws.String(p.iamProfileArn),
		}
	}

	tags := createCopy(p.defaultTags)
	tags["name"] = p.name
	in := &ec2.RunInstancesInput{
		ImageId:            aws.String(p.image),
		InstanceType:       aws.String(p.instanceType),
		MinCount:           aws.Int64(1),
		MaxCount:           aws.Int64(1),
		IamInstanceProfile: iamProfile,
		UserData: aws.String(
			base64.StdEncoding.EncodeToString(
				[]byte(p.user),
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
				DeviceName: aws.String(p.device),
				Ebs: &ec2.EbsBlockDevice{
					VolumeSize:          aws.Int64(p.volumeSize),
					VolumeType:          aws.String(p.volumeType),
					DeleteOnTermination: aws.Bool(true),
				},
			},
		},
	}

	if p.volumeType == "io1" {
		for _, blockDeviceMapping := range in.BlockDeviceMappings {
			blockDeviceMapping.Ebs.Iops = aws.Int64(p.volumeIops)
		}
	}

	logr := logger.FromContext(ctx).
		WithField("region", p.region).
		WithField("image", p.image).
		WithField("size", p.instanceType).
		WithField("provider", "aws")
	logr.Debug("instance create")

	results, err := client.RunInstances(in)
	if err != nil {
		logr.WithError(err).
			Error("instance create failed")
		return nil, err
	}

	amazonInstance := results.Instances[0]

	instance := &vmpool.Instance{
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

			if !p.allocPublicIP {
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
func (p *awsPool) Destroy(ctx context.Context, instance *vmpool.Instance) error {
	client := p.credentials.getClient()

	logr := logger.FromContext(ctx).
		WithField("id", instance.ID).
		WithField("ip", instance.IP).
		WithField("provider", "aws")

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

func (p *awsPool) getPools(ctx context.Context) (awspools *ec2.DescribeInstancesOutput, err error) {
	client := p.credentials.getClient()
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
	return client.DescribeInstancesWithContext(ctx, params)
}

func (p *awsPool) TagInstance(ctx context.Context, instanceID, key, value string) (err error) {
	client := p.credentials.getClient()
	input := &ec2.CreateTagsInput{
		Resources: []*string{
			aws.String(instanceID),
		},
		Tags: []*ec2.Tag{{Key: aws.String(key), Value: aws.String(value)}},
	}

	_, tagErr := client.CreateTagsWithContext(ctx, input)
	if tagErr != nil {
		return tagErr
	}
	return nil
}

func (p *awsPool) CleanPools(ctx context.Context) (err error) {
	logr := logger.FromContext(ctx).
		WithField("provider", "aws")
	logr.Debugln("clean pools")

	resp, err := p.getPools(ctx)
	if err != nil {
		logr.WithError(err).
			Errorln("cannot get pools from aws")
		return err
	}

	poolFullyCleaned := true

	// does any of the machines have the tags we want
	for idx := range resp.Reservations {
		for _, inst := range resp.Reservations[idx].Instances {
			droneTagFound := false
			runnerNameTagFound := false
			for _, keys := range inst.Tags {
				if *keys.Key == "drone" {
					if *keys.Value == "drone-runner-aws" {
						droneTagFound = true
					}
				}
				if *keys.Key == "creator" {
					if *keys.Value == p.runnerName {
						runnerNameTagFound = true
					}
				}
			}
			if droneTagFound && runnerNameTagFound {
				destInstance := vmpool.Instance{
					ID: *inst.InstanceId,
					IP: *inst.PublicIpAddress,
				}
				destErr := p.Destroy(ctx, &destInstance)
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

func (p *awsPool) PoolCountFree(ctx context.Context) (free int, err error) {
	poolName := p.name

	logr := logger.FromContext(ctx).
		WithField("provider", "aws").
		WithField("pool", poolName)

	logr.Debugln("check pool")

	p.awsMutex.Lock()
	defer p.awsMutex.Unlock()

	resp, err := p.getPools(ctx)
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
//nolint:nakedret
func (p *awsPool) TryPool(ctx context.Context) (instance *vmpool.Instance, err error) {
	poolName := p.name

	logr := logger.FromContext(ctx).
		WithField("provider", "aws").
		WithField("pool", poolName)

	logr.Debugln("try pool")

	p.awsMutex.Lock()
	defer p.awsMutex.Unlock()

	resp, err := p.getPools(ctx)
	if err != nil {
		logr.WithError(err).
			Errorln("cannot get pools from aws")
		return
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
				instance = &vmpool.Instance{
					ID: *inst.InstanceId,
					IP: *inst.PublicIpAddress,
				}
				break
			}
		}
		if instance != nil {
			break
		}
	}

	if instance == nil {
		logr.Debugln("no free instances")
		return
	}

	logr.Debugln("found an instance")

	err = p.TagInstance(ctx, instance.ID, "status", "build in progress")
	if err != nil {
		logr.WithError(err).
			WithField("instance", instance.ID).
			Errorln("cannot tag instance")
		instance = nil
		return
	}

	return
}

// Ping checks that we can log into EC2, and the regions respond
func (p *awsPool) Ping(ctx context.Context) error {
	client := p.credentials.getClient()

	allRegions := true
	input := &ec2.DescribeRegionsInput{
		AllRegions: &allRegions,
	}
	_, err := client.DescribeRegionsWithContext(ctx, input)

	return err
}

// Provision creates an aws instance for the pool, it will not perform build specific setup.
func (p *awsPool) Provision(ctx context.Context, addBuildingTag bool) (instance *vmpool.Instance, err error) { //nolint:funlen
	// add some tags
	awsTags := createCopy(p.defaultTags)
	awsTags["drone"] = "drone-runner-aws"
	awsTags["creator"] = p.runnerName
	if addBuildingTag {
		awsTags["status"] = "build in progress"
	} else {
		awsTags["pool"] = p.name
	}

	// create the instance
	startTime := time.Now()
	logger.FromContext(ctx).
		WithField("ami", p.GetInstanceType()).
		WithField("pool", awsTags["pool"]).
		WithField("adhoc", addBuildingTag).
		Debug("provision: creating instance")
	instance, createErr := p.Create(ctx)
	if createErr != nil {
		logger.FromContext(ctx).
			WithError(createErr).
			WithField("ami", p.GetInstanceType()).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			Debug("provision: failed to create the instance")
		return nil, createErr
	}
	logger.FromContext(ctx).
		WithField("ami", p.GetInstanceType()).
		WithField("pool", awsTags["pool"]).
		WithField("adhoc", addBuildingTag).
		WithField("id", instance.ID).
		WithField("ip", instance.IP).
		WithField("time(seconds)", (time.Since(startTime)).Seconds()).
		Info("provision: created the instance")
	// establish an ssh connection with the server
	client, dialErr := ssh.DialRetry(
		ctx,
		instance.IP,
		p.GetUser(),
		p.GetPrivateKey(),
	)
	if dialErr != nil {
		logger.FromContext(ctx).
			WithError(dialErr).
			WithField("ami", p.GetInstanceType()).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", instance.IP).
			WithField("id", instance.ID).
			WithField("error", dialErr).
			Debug("provision: failed to create client for ssh")
		return nil, dialErr
	}
	defer client.Close()
	logger.FromContext(ctx).
		WithField("ami", p.GetInstanceType()).
		WithField("pool", awsTags["pool"]).
		WithField("adhoc", addBuildingTag).
		WithField("ip", instance.IP).
		WithField("id", instance.ID).
		Debug("provision: Instance responding")
	clientftp, err := sftp.NewClient(client)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ami", p.GetInstanceType()).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", instance.IP).
			WithField("id", instance.ID).
			Debug("provision: failed to create sftp client")
		return nil, err
	}
	if clientftp != nil {
		defer clientftp.Close()
	}
	// setup common things, no matter what pipeline would use it
	mkdirErr := mkdir(clientftp, p.GetUser(), 0777) //nolint:gomnd // r/w/x for all users
	if mkdirErr != nil {
		logger.FromContext(ctx).
			WithError(mkdirErr).
			WithField("ami", p.GetInstanceType()).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", instance.IP).
			WithField("id", instance.ID).
			WithField("path", p.GetUser()).
			Error("provision: cannot create workspace directory")
		return nil, mkdirErr
	}
	// create docker network
	session, sessionErr := client.NewSession()
	if sessionErr != nil {
		logger.FromContext(ctx).
			WithError(sessionErr).
			WithField("ami", p.GetInstanceType()).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", instance.IP).
			WithField("id", instance.ID).
			Debug("provision: failed to create session")
		return nil, sessionErr
	}
	defer session.Close()
	// keep checking until docker is ok
	dockerErr := ssh.RetryApplication(ctx, client, "docker ps")
	if dockerErr != nil {
		logger.FromContext(ctx).
			WithError(dockerErr).
			WithField("ami", p.GetInstanceType()).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", instance.IP).
			WithField("id", instance.ID).
			Debug("provision: docker failed to start in a timely fashion")
		return nil, err
	}
	// create docker network
	networkCommand := "docker network create myNetwork"
	if p.os == "windows" {
		networkCommand = "docker network create --driver nat myNetwork"
	}
	err = session.Run(networkCommand)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ami", p.GetInstanceType()).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", instance.IP).
			WithField("id", instance.ID).
			WithField("command", networkCommand).
			Error("provision: unable to create docker network")
		return nil, err
	}
	logger.FromContext(ctx).
		WithField("ami", p.GetInstanceType()).
		WithField("pool", awsTags["pool"]).
		WithField("adhoc", addBuildingTag).
		WithField("ip", instance.IP).
		WithField("id", instance.ID).
		Info("provision: complete")

	return instance, nil
}

func mkdir(client *sftp.Client, path string, mode uint32) error {
	err := client.MkdirAll(path)
	if err != nil {
		return err
	}
	return client.Chmod(path, os.FileMode(mode))
}
