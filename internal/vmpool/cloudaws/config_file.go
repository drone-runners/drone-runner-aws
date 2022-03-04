package cloudaws

//
//import (
//	"bytes"
//	"fmt"
//	"io"
//	"os"
//
//	"github.com/drone/runner-go/logger"
//	"github.com/sirupsen/logrus"
//
//	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
//	"github.com/drone-runners/drone-runner-aws/internal/vmpool"
//	"github.com/drone-runners/drone-runner-aws/oshelp"
//
//	"gopkg.in/yaml.v2"
//)
//
//type (
//	poolDefinition struct {
//		Name        string   `json:"name,omitempty"`
//		MinPoolSize int      `json:"min_pool_size,omitempty" yaml:"min_pool_size"`
//		MaxPoolSize int      `json:"max_pool_size,omitempty" yaml:"max_pool_size"`
//		InitScript  string   `json:"init_script,omitempty" yaml:"init_script"`
//		Platform    platform `json:"platform,omitempty"`
//		Account     account  `json:"account,omitempty"`
//		gcpConfig    instance `json:"instance,omitempty"`
//	}
//
//	// account provides account settings
//	account struct {
//		AccessKeyID      string `json:"access_key_id,omitempty"  yaml:"access_key_id"`
//		AccessKeySecret  string `json:"access_key_secret,omitempty" yaml:"access_key_secret"`
//		Region           string `json:"region,omitempty"`
//		AvailabilityZone string `json:"availability_zone,omitempty" yaml:"availability_zone"`
//	}
//
//	platform struct {
//		OS      string `json:"os,omitempty"`
//		Arch    string `json:"arch,omitempty"`
//		Variant string `json:"variant,omitempty"`
//		Version string `json:"version,omitempty"`
//	}
//
//	// instance provides instance settings.
//	instance struct {
//		AMI           string            `json:"ami,omitempty"`
//		Tags          map[string]string `json:"tags,omitempty"`
//		IAMProfileARN string            `json:"iam_profile_arn,omitempty" yaml:"iam_profile_arn"`
//		Type          string            `json:"type,omitempty"`
//		User          string            `json:"user,omitempty"`
//		PrivateKey    string            `json:"private_key,omitempty" yaml:"private_key"`
//		PublicKey     string            `json:"public_key,omitempty" yaml:"public_key"`
//		UserData      string            `json:"user_data,omitempty"`
//		Disk          disk              `json:"disk,omitempty"`
//		Network       network           `json:"network,omitempty"`
//		Device        device            `json:"device,omitempty"`
//		ID            string            `json:"id,omitempty"`
//		IP            string            `json:"ip,omitempty"`
//	}
//
//	// network provides network settings.
//	network struct {
//		VPC               string   `json:"vpc,omitempty"`
//		VPCSecurityGroups []string `json:"vpc_security_group_ids,omitempty" yaml:"vpc_security_groups"`
//		SecurityGroups    []string `json:"security_groups,omitempty" yaml:"security_groups"`
//		SubnetID          string   `json:"subnet_id,omitempty" yaml:"subnet_id"`
//		PrivateIP         bool     `json:"private_ip,omitempty" yaml:"private_ip"`
//	}
//
//	// disk provides disk size and type.
//	disk struct {
//		Size int64  `json:"size,omitempty"`
//		Type string `json:"type,omitempty"`
//		Iops int64  `json:"iops,omitempty"`
//	}
//
//	// device provides the device settings.
//	device struct {
//		Name string `json:"name,omitempty"`
//	}
//)
//
//func ProcessPoolFile(rawFile string, defaultPoolSettings *vmpool.DefaultSettings) ([]vmpool.Pool, error) {
//	rawPool, err := os.ReadFile(rawFile)
//	if os.IsNotExist(err) {
//		return nil, nil
//	}
//	if err != nil {
//		err = fmt.Errorf("unable to read file %s: %w", rawFile, err)
//		return nil, err
//	}
//
//	buf := bytes.NewBuffer(rawPool)
//	dec := yaml.NewDecoder(buf)
//
//	var pools []vmpool.Pool
//
//	for {
//		poolDef := new(poolDefinition)
//		err := dec.Decode(poolDef)
//		if err == io.EOF {
//			break
//		}
//		if err != nil {
//			return nil, err
//		}
//
//		poolDef.applyDefaults(defaultPoolSettings)
//
//		// we need Access, error if its still empty
//		if poolDef.Account.AccessKeyID == "" {
//			logrus.Infof("AWS access key is not provided (falling back to ec2 instance profile)")
//		}
//		// TODO: Remove the comment
//		// if poolDef.Account.AccessKeySecret == "" {
//		// 	return nil, errors.New("missing AWS secret. Add to .env file or pool file")
//		// }
//
//		err = poolDef.applyInitScript(defaultPoolSettings)
//		if err != nil {
//			return nil, err
//		}
//
//		pool := &awsPool{
//			name:       poolDef.Name,
//			runnerName: defaultPoolSettings.RunnerName,
//			credentials: Credentials{
//				Client:           poolDef.Account.AccessKeyID,
//				Secret:           poolDef.Account.AccessKeySecret,
//				Region:           poolDef.Account.Region,
//				AvailabilityZone: poolDef.Account.AvailabilityZone,
//			},
//			keyPairName:   defaultPoolSettings.AwsKeyPairName,
//			iamProfileArn: poolDef.gcpConfig.IAMProfileARN,
//			os:            poolDef.Platform.OS,
//			rootDir:       tempdir(poolDef.Platform.OS),
//			image:         poolDef.gcpConfig.AMI,
//			instanceType:  poolDef.gcpConfig.Type,
//			user:          poolDef.gcpConfig.User,
//			userData:      poolDef.gcpConfig.UserData,
//			subnet:        poolDef.gcpConfig.Network.SubnetID,
//			groups:        poolDef.gcpConfig.Network.SecurityGroups,
//			allocPublicIP: !poolDef.gcpConfig.Network.PrivateIP,
//			device:        poolDef.gcpConfig.Device.Name,
//			volumeType:    poolDef.gcpConfig.Disk.Type,
//			volumeSize:    poolDef.gcpConfig.Disk.Size,
//			volumeIops:    poolDef.gcpConfig.Disk.Iops,
//			defaultTags:   poolDef.gcpConfig.Tags,
//			sizeMin:       poolDef.MinPoolSize,
//			sizeMax:       poolDef.MaxPoolSize,
//		}
//
//		logr := logger.Default.
//			WithField("name", poolDef.Name).
//			WithField("os", poolDef.Platform.OS).
//			WithField("arch", poolDef.Platform.Arch)
//
//		if poolDef.InitScript != "" {
//			logr = logr.WithField("cloud-init", poolDef.InitScript)
//		}
//
//		logr.Info("parsed pool file")
//
//		pools = append(pools, pool)
//	}
//
//	return pools, nil
//}
//
//func DummyPool(name, runnerName string) vmpool.Pool {
//	return &awsPool{
//		name:       name,
//		runnerName: runnerName,
//		sizeMin:    0,
//		sizeMax:    1,
//	}
//}
//
//func (poolDef *poolDefinition) applyDefaults(defaultPoolSettings *vmpool.DefaultSettings) {
//	if poolDef.MinPoolSize < 0 {
//		poolDef.MinPoolSize = 0
//	}
//	if poolDef.MaxPoolSize <= 0 {
//		poolDef.MaxPoolSize = 100
//	}
//
//	if poolDef.MinPoolSize > poolDef.MaxPoolSize {
//		poolDef.MinPoolSize = poolDef.MaxPoolSize
//	}
//	// apply defaults to Account
//	if poolDef.Account.AccessKeyID == "" {
//		poolDef.Account.AccessKeyID = defaultPoolSettings.AwsAccessKeyID
//	}
//	if poolDef.Account.AccessKeySecret == "" {
//		poolDef.Account.AccessKeySecret = defaultPoolSettings.AwsAccessKeySecret
//	}
//	if poolDef.Account.Region == "" {
//		if defaultPoolSettings.AwsRegion == "" {
//			poolDef.Account.Region = "us-east-1"
//		} else {
//			poolDef.Account.Region = defaultPoolSettings.AwsRegion
//		}
//	}
//	if poolDef.Account.AvailabilityZone == "" {
//		poolDef.Account.AvailabilityZone = defaultPoolSettings.AwsAvailabilityZone
//	}
//	// apply defaults to Platform
//	if poolDef.Platform.OS == "" {
//		poolDef.Platform.OS = oshelp.OSLinux
//	}
//	if poolDef.Platform.Arch == "" {
//		poolDef.Platform.Arch = "amd64"
//	}
//
//	// apply defaults to gcpConfig
//
//	// set default instance type if not provided
//	if poolDef.gcpConfig.Type == "" {
//		if poolDef.Platform.Arch == "arm64" {
//			poolDef.gcpConfig.Type = "a1.medium"
//		} else {
//			poolDef.gcpConfig.Type = "t3.nano"
//		}
//	}
//	// put something into tags even if empty
//	if poolDef.gcpConfig.Tags == nil {
//		poolDef.gcpConfig.Tags = make(map[string]string)
//	}
//	// set the default disk size if not provided
//	if poolDef.gcpConfig.Disk.Size == 0 {
//		poolDef.gcpConfig.Disk.Size = 32
//	}
//	// set the default disk type if not provided
//	if poolDef.gcpConfig.Disk.Type == "" {
//		poolDef.gcpConfig.Disk.Type = "gp2"
//	}
//	// set the default iops
//	if poolDef.gcpConfig.Disk.Type == "io1" && poolDef.gcpConfig.Disk.Iops == 0 {
//		poolDef.gcpConfig.Disk.Iops = 100
//	}
//	// set the default device
//	if poolDef.gcpConfig.Device.Name == "" {
//		poolDef.gcpConfig.Device.Name = "/dev/sda1"
//	}
//	// set the default ssh user. this user account is responsible for executing the pipeline script.
//	if poolDef.gcpConfig.User == "" {
//		if poolDef.Platform.OS == oshelp.OSWindows {
//			poolDef.gcpConfig.User = "Administrator"
//		} else {
//			poolDef.gcpConfig.User = "root"
//		}
//	}
//}
//
//func (poolDef *poolDefinition) applyInitScript(defaultPoolSettings *vmpool.DefaultSettings) (err error) {
//	cloudInitParams := &cloudinit.Params{
//		PublicKey:      poolDef.gcpConfig.PublicKey,
//		LiteEnginePath: defaultPoolSettings.LiteEnginePath,
//		CaCertFile:     defaultPoolSettings.CaCertFile,
//		CertFile:       defaultPoolSettings.CertFile,
//		KeyFile:        defaultPoolSettings.KeyFile,
//		Platform:       poolDef.Platform.OS,
//		Architecture:   poolDef.Platform.Arch,
//	}
//
//	if poolDef.InitScript == "" {
//		if poolDef.Platform.OS == oshelp.OSWindows {
//			poolDef.gcpConfig.UserData = cloudinit.Windows(cloudInitParams)
//		} else {
//			poolDef.gcpConfig.UserData = cloudinit.Linux(cloudInitParams)
//		}
//
//		return
//	}
//
//	data, err := os.ReadFile(poolDef.InitScript)
//	if err != nil {
//		err = fmt.Errorf("failed to load cloud init script template: %w", err)
//		return
//	}
//
//	poolDef.gcpConfig.UserData, err = cloudinit.Custom(string(data), cloudInitParams)
//
//	return
//}
