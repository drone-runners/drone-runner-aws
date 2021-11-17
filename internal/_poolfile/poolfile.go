// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package poolfile

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/drone-runners/drone-runner-aws/oshelp"
	"github.com/drone/runner-go/logger"
	"github.com/pkg/sftp"
	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
	"github.com/drone-runners/drone-runner-aws/internal/platform"
	"github.com/drone-runners/drone-runner-aws/internal/ssh"
	"github.com/drone-runners/drone-runner-aws/internal/sshkey"
	"gopkg.in/yaml.v2"
)

// PoolSettings defines default settings.
type (
	PoolSettings struct {
		AwsAccessKeyID     string
		LiteEnginePath     string
		AwsAccessKeySecret string
		AwsRegion          string
		PrivateKeyFile     string
		PublicKeyFile      string
	}

	Pool struct {
		Name        string   `json:"name,omitempty"`
		Root        string   `json:"root,omitempty"`
		MaxPoolSize int      `json:"max_pool_size,omitempty" yaml:"max_pool_size"`
		Platform    Platform `json:"platform,omitempty"`
		Account     Account  `json:"account,omitempty"`
		Instance    Instance `json:"instance,omitempty"`
	}
	// Account provides account settings
	Account struct {
		AccessKeyID     string `json:"access_key_id,omitempty"  yaml:"access_key_id"`
		AccessKeySecret string `json:"access_key_secret,omitempty" yaml:"access_key_secret"`
		Region          string `json:"region,omitempty"`
	}

	Platform struct {
		OS      string `json:"os,omitempty"`
		Arch    string `json:"arch,omitempty"`
		Variant string `json:"variant,omitempty"`
		Version string `json:"version,omitempty"`
	}
	// Instance provides instance settings.
	Instance struct {
		AMI           string            `json:"ami,omitempty"`
		Tags          map[string]string `json:"tags,omitempty"`
		IAMProfileARN string            `json:"iam_profile_arn,omitempty" yaml:"iam_profile_arn"`
		Type          string            `json:"type,omitempty"`
		User          string            `json:"user,omitempty"`
		PrivateKey    string            `json:"private_key,omitempty" yaml:"private_key"`
		PublicKey     string            `json:"public_key,omitempty" yaml:"public_key"`
		UserData      string            `json:"user_data,omitempty"`
		Disk          Disk              `json:"disk,omitempty"`
		Network       Network           `json:"network,omitempty"`
		Device        Device            `json:"device,omitempty"`
		ID            string            `json:"id,omitempty"`
		IP            string            `json:"ip,omitempty"`
	}

	// Network provides network settings.
	Network struct {
		VPC               string   `json:"vpc,omitempty"`
		VPCSecurityGroups []string `json:"vpc_security_group_ids,omitempty" yaml:"vpc_security_groups"`
		SecurityGroups    []string `json:"security_groups,omitempty" yaml:"security_groups"`
		SubnetID          string   `json:"subnet_id,omitempty" yaml:"subnet_id"`
		PrivateIP         bool     `json:"private_ip,omitempty" yaml:"private_ip"`
	}

	// Disk provides disk size and type.
	Disk struct {
		Size int64  `json:"size,omitempty"`
		Type string `json:"type,omitempty"`
		Iops int64  `json:"iops,omitempty"`
	}

	// Device provides the device settings.
	Device struct {
		Name string `json:"name,omitempty"`
	}
)

// helper function returns the base temporary directory based on the target platform.
func tempdir(inputOS string) string {
	dir := "aws"
	switch inputOS {
	case oshelp.WindowsString:
		return oshelp.JoinPaths(inputOS, "C:\\Windows\\Temp", dir)
	default:
		return oshelp.JoinPaths(inputOS, "/tmp", dir)
	}
}

func compilePoolFile(rawPool Pool, settings *PoolSettings) (Pool, error) { //nolint:gocritic,gocyclo // its complex but standard
	pipelineOS := rawPool.Platform.OS
	// secrets and error here
	if rawPool.Account.AccessKeyID == "" {
		rawPool.Account.AccessKeyID = settings.AwsAccessKeyID
	}
	if rawPool.Account.AccessKeySecret == "" {
		rawPool.Account.AccessKeySecret = settings.AwsAccessKeySecret
	}
	// we need Access, error if its still empty
	if rawPool.Account.AccessKeyID == "" || rawPool.Account.AccessKeySecret == "" {
		return Pool{}, fmt.Errorf("missing AWS access key or AWS secret. Add to .env file or pool file")
	}
	// try config first. then set the default region if not provided
	if rawPool.Account.Region == "" && settings.AwsRegion != "" {
		rawPool.Account.Region = settings.AwsRegion
	} else if rawPool.Account.Region == "" {
		rawPool.Account.Region = "us-east-1"
	}
	// set default instance type if not provided
	if rawPool.Instance.Type == "" {
		rawPool.Instance.Type = "t3.nano"
		if rawPool.Platform.Arch == "arm64" {
			rawPool.Instance.Type = "a1.medium"
		}
	}
	// put something into tags even if empty
	if rawPool.Instance.Tags == nil {
		rawPool.Instance.Tags = make(map[string]string)
	}
	// set the default disk size if not provided
	if rawPool.Instance.Disk.Size == 0 {
		rawPool.Instance.Disk.Size = 32
	}
	// set the default disk type if not provided
	if rawPool.Instance.Disk.Type == "" {
		rawPool.Instance.Disk.Type = "gp2"
	}
	// set the default iops
	if rawPool.Instance.Disk.Type == "io1" && rawPool.Instance.Disk.Iops == 0 {
		rawPool.Instance.Disk.Iops = 100
	}
	// set the default device
	if rawPool.Instance.Device.Name == "" {
		rawPool.Instance.Device.Name = "/dev/sda1"
	}
	// set the default ssh user. this user account is responsible for executing the pipeline script.
	switch {
	case rawPool.Instance.User == "" && rawPool.Platform.OS == oshelp.WindowsString:
		rawPool.Instance.User = "Administrator"
	case rawPool.Instance.User == "":
		rawPool.Instance.User = "root"
	}
	_, statErr := os.Stat(settings.PrivateKeyFile)
	if os.IsNotExist(statErr) {
		// there are no key files
		publickey, privatekey, generateKeyErr := sshkey.GeneratePair()
		if generateKeyErr != nil {
			publickey = ""
			privatekey = ""
		}
		rawPool.Instance.PrivateKey = privatekey
		rawPool.Instance.PublicKey = publickey
	} else {
		body, privateKeyErr := os.ReadFile(settings.PrivateKeyFile)
		if privateKeyErr != nil {
			log.Fatalf("unable to read file ``: %v", privateKeyErr)
		}
		rawPool.Instance.PrivateKey = string(body)

		body, publicKeyErr := os.ReadFile(settings.PublicKeyFile)
		if publicKeyErr != nil {
			log.Fatalf("unable to read file: %v", publicKeyErr)
		}
		rawPool.Instance.PublicKey = string(body)
	}
	// generate the cloudinit file
	var userDataWithSSH string
	if rawPool.Platform.OS == oshelp.WindowsString {
		userDataWithSSH = cloudinit.Windows(cloudinit.Params{
			PublicKey:      rawPool.Instance.PublicKey,
			LiteEnginePath: settings.LiteEnginePath,
		})
	} else {
		// try using cloud init.
		userDataWithSSH = cloudinit.Linux(cloudinit.Params{
			PublicKey:      rawPool.Instance.PublicKey,
			LiteEnginePath: settings.LiteEnginePath,
		})
	}
	rawPool.Instance.UserData = userDataWithSSH
	// create the root directory
	rawPool.Root = tempdir(pipelineOS)

	return rawPool, nil
}

func ProcessPoolFile(rawFile string, settings *PoolSettings) (foundPools map[string]Pool, err error) {
	rawPool, readPoolFileErr := os.ReadFile(rawFile)
	if readPoolFileErr != nil {
		errorMessage := fmt.Sprintf("unable to read file: %s", rawFile)
		return nil, fmt.Errorf(errorMessage, readPoolFileErr)
	}
	foundPools = make(map[string]Pool)
	buf := bytes.NewBuffer(rawPool)
	dec := yaml.NewDecoder(buf)

	for {
		rawPool := new(Pool)
		err := dec.Decode(rawPool)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		preppedPool, compilePoolFileErr := compilePoolFile(*rawPool, settings)
		if compilePoolFileErr != nil {
			return nil, compilePoolFileErr
		}
		foundPools[rawPool.Name] = preppedPool
	}
	return foundPools, nil
}

// create an aws instance for the pool, it will not perform build specific setup.
func Provision(ctx context.Context, poolInfo *Pool, runnerName string, addBuildingTag bool) (id, ip string, err error) { //nolint:funlen
	// create creds
	creds := platform.Credentials{
		Client: poolInfo.Account.AccessKeyID,
		Secret: poolInfo.Account.AccessKeySecret,
		Region: poolInfo.Account.Region,
	}
	// add some tags
	awsTags := poolInfo.Instance.Tags
	awsTags["drone"] = "drone-runner-aws"
	awsTags["creator"] = runnerName
	if addBuildingTag {
		awsTags["status"] = "build in progress"
	} else {
		awsTags["pool"] = poolInfo.Name
	}
	// provisioning information
	provArgs := platform.ProvisionArgs{
		Image:         poolInfo.Instance.AMI,
		IamProfileArn: poolInfo.Instance.IAMProfileARN,
		Size:          poolInfo.Instance.Type,
		Region:        poolInfo.Account.Region,
		Userdata:      poolInfo.Instance.UserData,
		// Tags:
		Tags: awsTags,
		// network
		Subnet:    poolInfo.Instance.Network.SubnetID,
		Groups:    poolInfo.Instance.Network.SecurityGroups,
		Device:    poolInfo.Instance.Device.Name,
		PrivateIP: poolInfo.Instance.Network.PrivateIP,
		// disk
		VolumeType: poolInfo.Instance.Disk.Type,
		VolumeSize: poolInfo.Instance.Disk.Size,
		VolumeIops: poolInfo.Instance.Disk.Iops,
	}
	// create the instance
	startTime := time.Now()
	logger.FromContext(ctx).
		WithField("ami", poolInfo.Instance.AMI).
		WithField("pool", awsTags["pool"]).
		WithField("adhoc", addBuildingTag).
		Debug("provision: creating instance")
	instance, createErr := platform.Create(ctx, creds, &provArgs)
	if createErr != nil {
		logger.FromContext(ctx).
			WithError(createErr).
			WithField("ami", poolInfo.Instance.AMI).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			Debug("provision: failed to create the instance")
		return "", "", createErr
	}
	logger.FromContext(ctx).
		WithField("ami", poolInfo.Instance.AMI).
		WithField("pool", awsTags["pool"]).
		WithField("adhoc", addBuildingTag).
		WithField("id", instance.ID).
		WithField("ip", instance.IP).
		WithField("time(seconds)", (time.Since(startTime)).Seconds()).
		Info("provision: created the instance")
	// we have a system
	poolInfo.Instance.ID = instance.ID
	poolInfo.Instance.IP = instance.IP
	// establish an ssh connection with the server
	client, dialErr := ssh.DialRetry(
		ctx,
		poolInfo.Instance.IP,
		poolInfo.Instance.User,
		poolInfo.Instance.PrivateKey,
	)
	if dialErr != nil {
		logger.FromContext(ctx).
			WithError(dialErr).
			WithField("ami", poolInfo.Instance.AMI).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", poolInfo.Instance.IP).
			WithField("id", poolInfo.Instance.ID).
			WithField("error", dialErr).
			Debug("provision: failed to create client for ssh")
		return "", "", dialErr
	}
	defer client.Close()
	logger.FromContext(ctx).
		WithField("ami", poolInfo.Instance.AMI).
		WithField("pool", awsTags["pool"]).
		WithField("adhoc", addBuildingTag).
		WithField("ip", poolInfo.Instance.IP).
		WithField("id", poolInfo.Instance.ID).
		Debug("provision: Instance responding")
	clientftp, err := sftp.NewClient(client)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ami", poolInfo.Instance.AMI).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", poolInfo.Instance.IP).
			WithField("id", poolInfo.Instance.ID).
			Debug("provision: failed to create sftp client")
		return "", "", err
	}
	if clientftp != nil {
		defer clientftp.Close()
	}
	// setup common things, no matter what pipeline would use it
	mkdirErr := mkdir(clientftp, poolInfo.Root, 0777) //nolint:gomnd // r/w/x for all users
	if mkdirErr != nil {
		logger.FromContext(ctx).
			WithError(mkdirErr).
			WithField("ami", poolInfo.Instance.AMI).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", poolInfo.Instance.IP).
			WithField("id", poolInfo.Instance.ID).
			WithField("path", poolInfo.Root).
			Error("provision: cannot create workspace directory")
		return "", "", mkdirErr
	}
	// create docker network
	session, sessionErr := client.NewSession()
	if sessionErr != nil {
		logger.FromContext(ctx).
			WithError(sessionErr).
			WithField("ami", poolInfo.Instance.AMI).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", poolInfo.Instance.IP).
			WithField("id", poolInfo.Instance.ID).
			Debug("provision: failed to create session")
		return "", "", sessionErr
	}
	defer session.Close()
	// keep checking until docker is ok
	dockerErr := ssh.RetryApplication(ctx, client, "docker ps")
	if dockerErr != nil {
		logger.FromContext(ctx).
			WithError(dockerErr).
			WithField("ami", poolInfo.Instance.AMI).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", poolInfo.Instance.IP).
			WithField("id", poolInfo.Instance.ID).
			Debug("provision: docker failed to start in a timely fashion")
		return "", "", err
	}
	// create docker network
	networkCommand := "docker network create myNetwork"
	if poolInfo.Platform.OS == "windows" {
		networkCommand = "docker network create --driver nat myNetwork"
	}
	err = session.Run(networkCommand)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ami", poolInfo.Instance.AMI).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", poolInfo.Instance.IP).
			WithField("id", poolInfo.Instance.ID).
			WithField("command", networkCommand).
			Error("provision: unable to create docker network")
		return "", "", err
	}
	logger.FromContext(ctx).
		WithField("ami", poolInfo.Instance.AMI).
		WithField("pool", awsTags["pool"]).
		WithField("adhoc", addBuildingTag).
		WithField("ip", poolInfo.Instance.IP).
		WithField("id", poolInfo.Instance.ID).
		Info("provision: complete")
	return poolInfo.Instance.ID, poolInfo.Instance.IP, nil
}

func BuildPools(ctx context.Context, pools map[string]Pool, creds platform.Credentials, runnerName string, awsMutex *sync.Mutex) error {
	for i := range pools {
		poolcount, _ := platform.PoolCountFree(ctx, creds, pools[i].Name, awsMutex)
		for poolcount < pools[i].MaxPoolSize {
			poolInstance := pools[i]
			id, ip, setupErr := Provision(ctx, &poolInstance, runnerName, false)
			if setupErr != nil {
				return setupErr
			}
			logrus.Infof("BuildPools: created instance %s %s %s", pools[i].Name, id, ip)
			poolcount++
		}
	}
	return nil
}

func mkdir(client *sftp.Client, path string, mode uint32) error {
	err := client.MkdirAll(path)
	if err != nil {
		return err
	}
	return client.Chmod(path, os.FileMode(mode))
}
