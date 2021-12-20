package cloudaws

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
	"github.com/drone-runners/drone-runner-aws/internal/sshkey"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool"
	"github.com/drone-runners/drone-runner-aws/oshelp"

	"gopkg.in/yaml.v2"
)

type (
	poolDefinition struct {
		Name        string   `json:"name,omitempty"`
		Root        string   `json:"root,omitempty"` // TODO: Remove this, the runner should not care about LE's root dir
		MinPoolSize int      `json:"min_pool_size,omitempty" yaml:"min_pool_size"`
		MaxPoolSize int      `json:"max_pool_size,omitempty" yaml:"max_pool_size"`
		Platform    platform `json:"platform,omitempty"`
		Account     account  `json:"account,omitempty"`
		Instance    instance `json:"instance,omitempty"`
	}

	// account provides account settings
	account struct {
		AccessKeyID     string `json:"access_key_id,omitempty"  yaml:"access_key_id"`
		AccessKeySecret string `json:"access_key_secret,omitempty" yaml:"access_key_secret"`
		Region          string `json:"region,omitempty"`
	}

	platform struct {
		OS      string `json:"os,omitempty"`
		Arch    string `json:"arch,omitempty"`
		Variant string `json:"variant,omitempty"`
		Version string `json:"version,omitempty"`
	}

	// instance provides instance settings.
	instance struct {
		AMI           string            `json:"ami,omitempty"`
		Tags          map[string]string `json:"tags,omitempty"`
		IAMProfileARN string            `json:"iam_profile_arn,omitempty" yaml:"iam_profile_arn"`
		Type          string            `json:"type,omitempty"`
		User          string            `json:"user,omitempty"`
		PrivateKey    string            `json:"private_key,omitempty" yaml:"private_key"`
		PublicKey     string            `json:"public_key,omitempty" yaml:"public_key"`
		UserData      string            `json:"user_data,omitempty"`
		Disk          disk              `json:"disk,omitempty"`
		Network       network           `json:"network,omitempty"`
		Device        device            `json:"device,omitempty"`
		ID            string            `json:"id,omitempty"`
		IP            string            `json:"ip,omitempty"`
	}

	// network provides network settings.
	network struct {
		VPC               string   `json:"vpc,omitempty"`
		VPCSecurityGroups []string `json:"vpc_security_group_ids,omitempty" yaml:"vpc_security_groups"`
		SecurityGroups    []string `json:"security_groups,omitempty" yaml:"security_groups"`
		SubnetID          string   `json:"subnet_id,omitempty" yaml:"subnet_id"`
		PrivateIP         bool     `json:"private_ip,omitempty" yaml:"private_ip"`
	}

	// disk provides disk size and type.
	disk struct {
		Size int64  `json:"size,omitempty"`
		Type string `json:"type,omitempty"`
		Iops int64  `json:"iops,omitempty"`
	}

	// device provides the device settings.
	device struct {
		Name string `json:"name,omitempty"`
	}
)

func ProcessPoolFile(rawFile string, defaultPoolSettings *vmpool.DefaultSettings) ([]vmpool.Pool, error) {
	rawPool, err := os.ReadFile(rawFile)
	if err != nil {
		err = fmt.Errorf("unable to read file %s: %w", rawFile, err)
		return nil, err
	}

	buf := bytes.NewBuffer(rawPool)
	dec := yaml.NewDecoder(buf)

	var pools []vmpool.Pool

	for {
		poolDef := new(poolDefinition)
		err := dec.Decode(poolDef)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		pool, err := compilePoolFile(poolDef, defaultPoolSettings)
		if err != nil {
			return nil, err
		}

		pools = append(pools, pool)
	}

	return pools, nil
}

func DummyPool(name, runnerName string) vmpool.Pool {
	return &awsPool{
		name:       name,
		runnerName: runnerName,
		sizeMin:    0,
		sizeMax:    1,
	}
}

func compilePoolFile(rawPool *poolDefinition, defaultPoolSettings *vmpool.DefaultSettings) (*awsPool, error) {
	pipelineOS := rawPool.Platform.OS

	creds := Credentials{
		Client: defaultPoolSettings.AwsAccessKeyID,
		Secret: defaultPoolSettings.AwsAccessKeySecret,
		Region: defaultPoolSettings.AwsRegion,
	}

	// override access key-ID, secret and region defaults with the values from config file
	if rawPool.Account.AccessKeyID != "" {
		creds.Client = rawPool.Account.AccessKeyID
	}
	if rawPool.Account.AccessKeySecret != "" {
		creds.Secret = rawPool.Account.AccessKeySecret
	}
	if rawPool.Account.Region != "" {
		creds.Region = rawPool.Account.Region
	}

	// we need Access, error if its still empty
	if creds.Client == "" || creds.Secret == "" {
		return nil, errors.New("missing AWS access key or AWS secret. Add to .env file or pool file")
	}
	// set the default region if not provided
	if creds.Region == "" {
		creds.Region = "us-east-1"
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
	_, statErr := os.Stat(defaultPoolSettings.PrivateKeyFile)
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
		body, privateKeyErr := os.ReadFile(defaultPoolSettings.PrivateKeyFile)
		if privateKeyErr != nil {
			log.Fatalf("unable to read file ``: %v", privateKeyErr)
		}
		rawPool.Instance.PrivateKey = string(body)

		body, publicKeyErr := os.ReadFile(defaultPoolSettings.PublicKeyFile)
		if publicKeyErr != nil {
			log.Fatalf("unable to read file: %v", publicKeyErr)
		}
		rawPool.Instance.PublicKey = string(body)
	}

	// generate the cloudinit file
	var userDataWithSSH string
	if rawPool.Platform.OS == oshelp.WindowsString {
		userDataWithSSH = cloudinit.Windows(&cloudinit.Params{
			PublicKey:      rawPool.Instance.PublicKey,
			LiteEnginePath: defaultPoolSettings.LiteEnginePath,
			CaCertFile:     defaultPoolSettings.CaCertFile,
			CertFile:       defaultPoolSettings.CertFile,
			KeyFile:        defaultPoolSettings.KeyFile,
		})
	} else {
		// try using cloud init.
		userDataWithSSH = cloudinit.Linux(&cloudinit.Params{
			PublicKey:      rawPool.Instance.PublicKey,
			LiteEnginePath: defaultPoolSettings.LiteEnginePath,
			CaCertFile:     defaultPoolSettings.CaCertFile,
			CertFile:       defaultPoolSettings.CertFile,
			KeyFile:        defaultPoolSettings.KeyFile,
		})
	}
	rawPool.Instance.UserData = userDataWithSSH
	// create the root directory
	rawPool.Root = tempdir(pipelineOS)

	return &awsPool{
		name:          rawPool.Name,
		runnerName:    defaultPoolSettings.RunnerName,
		credentials:   creds,
		keyPairName:   defaultPoolSettings.AwsKeyPairName,
		privateKey:    rawPool.Instance.PrivateKey,
		iamProfileArn: rawPool.Instance.IAMProfileARN,
		os:            pipelineOS,
		rootDir:       rawPool.Root,
		image:         rawPool.Instance.AMI,
		instanceType:  rawPool.Instance.Type,
		user:          rawPool.Instance.User,
		userData:      rawPool.Instance.UserData,
		subnet:        rawPool.Instance.Network.SubnetID,
		groups:        rawPool.Instance.Network.SecurityGroups,
		allocPublicIP: !rawPool.Instance.Network.PrivateIP,
		device:        rawPool.Instance.Device.Name,
		volumeType:    rawPool.Instance.Disk.Type,
		volumeSize:    rawPool.Instance.Disk.Size,
		volumeIops:    rawPool.Instance.Disk.Iops,
		defaultTags:   rawPool.Instance.Tags,
		sizeMin:       rawPool.MinPoolSize,
		sizeMax:       rawPool.MaxPoolSize,
	}, nil
}
