package poolfile

import (
	"errors"
	"fmt"
	"github.com/drone-runners/drone-runner-aws/internal/drivers/azure"
	"os"
	"path/filepath"
	"strings"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/drivers/amazon"
	"github.com/drone-runners/drone-runner-aws/internal/drivers/anka"
	"github.com/drone-runners/drone-runner-aws/internal/drivers/digitalocean"
	"github.com/drone-runners/drone-runner-aws/internal/drivers/google"
	"github.com/drone-runners/drone-runner-aws/internal/drivers/vmfusion"
	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
	"gopkg.in/yaml.v2"

	"github.com/sirupsen/logrus"
)

const (
	DefaultPoolName = "testpool"
)

func ProcessPool(poolFile *config.PoolFile, runnerName string) ([]drivers.Pool, error) {
	var pools = []drivers.Pool{}

	for i := range poolFile.Instances {
		instance := poolFile.Instances[i]
		switch instance.Type {
		case string(types.VMFusion):
			var v, ok = instance.Spec.(*config.VMFusion)
			if !ok {
				logrus.Errorln("unable to parse pool file")
			}
			// set platform defaults
			platform, platformErr := vmfusion.SetPlatformDefaults(&instance.Platform)
			if platformErr != nil {
				logrus.WithError(platformErr).WithField("driver", instance.Type)
			}
			instance.Platform = *platform

			driver, err := vmfusion.New(
				vmfusion.WithStorePath(v.StorePath),
				vmfusion.WithUsername(v.Account.Username),
				vmfusion.WithPassword(v.Account.Password),
				vmfusion.WithISO(v.ISO),
				vmfusion.WithCPU(v.CPU),
				vmfusion.WithMemory(v.Memory),
				vmfusion.WithVDiskPath(v.VDiskPath),
				vmfusion.WithUserData(v.UserData, v.UserDataPath),
				vmfusion.WithRootDirectory(v.RootDirectory),
			)
			if err != nil {
				logrus.WithError(err).WithField("driver", instance.Type)
			}
			pool := mapPool(&instance, runnerName)

			pool.Driver = driver
			pools = append(pools, pool)
		case string(types.Amazon):
			var a, ok = instance.Spec.(*config.Amazon)
			if !ok {
				logrus.Errorln("unable to parse pool file")
			}
			// set platform defaults
			platform, platformErr := amazon.SetPlatformDefaults(&instance.Platform)
			if platformErr != nil {
				logrus.WithError(platformErr).WithField("driver", instance.Type)
			}
			instance.Platform = *platform
			var driver, err = amazon.New(
				amazon.WithAccessKeyID(a.Account.AccessKeyID),
				amazon.WithSecretAccessKey(a.Account.AccessKeySecret),
				amazon.WithZone(a.Account.AvailabilityZone),
				amazon.WithKeyPair(a.Account.KeyPairName),
				amazon.WithDeviceName(a.DeviceName, instance.Platform.OSName),
				amazon.WithRootDirectory(a.RootDirectory),
				amazon.WithAMI(a.AMI),
				amazon.WithVpc(a.VPC),
				amazon.WithUser(a.User, instance.Platform.OS),
				amazon.WithRegion(a.Account.Region, a.Account.Region),
				amazon.WithRetries(a.Account.Retries),
				amazon.WithPrivateIP(a.Network.PrivateIP),
				amazon.WithSecurityGroup(a.Network.SecurityGroups...),
				amazon.WithSize(a.Size, instance.Platform.Arch),
				amazon.WithSizeAlt(a.SizeAlt),
				amazon.WithSubnet(a.Network.SubnetID),
				amazon.WithUserData(a.UserData, a.UserDataPath),
				amazon.WithVolumeSize(a.Disk.Size),
				amazon.WithVolumeType(a.Disk.Type),
				amazon.WithVolumeIops(a.Disk.Iops, a.Disk.Type),
				amazon.WithIamProfileArn(a.IamProfileArn),
				amazon.WithMarketType(a.MarketType),
				amazon.WithTags(a.Tags),
				amazon.WithHibernate(a.Hibernate),
			)
			if err != nil {
				logrus.WithError(err).WithField("driver", instance.Type)
			}
			pool := mapPool(&instance, runnerName)
			pool.Driver = driver
			pools = append(pools, pool)
		case string(types.Azure):
			var az, ok = instance.Spec.(*config.Azure)
			if !ok {
				logrus.Errorln("unable to parse pool file")
			}
			// set platform defaults
			platform, platformErr := azure.SetPlatformDefaults(&instance.Platform)
			if platformErr != nil {
				logrus.WithError(platformErr).WithField("driver", instance.Type)
			}
			instance.Platform = *platform
			var driver, err = azure.New(
				azure.WithSubscriptionID(az.Account.SubscriptionID),
				azure.WithClientID(az.Account.ClientID),
				azure.WithClientSecret(az.Account.ClientSecret),
				azure.WithTenantID(az.Account.TenantID),
				azure.WithResourceGroupName(az.ResourceGroup),
				azure.WithUserData(az.UserData, az.UserDataPath),
				azure.WithSize(az.Size),
				azure.WithImage(az.Image.Publisher, az.Image.Offer, az.Image.SKU, az.Image.Version),
				azure.WithUsername(az.Image.Username),
				azure.WithPassword(az.Image.Password),
				azure.WithLocation(az.Location),
				azure.WithRootDirectory(az.RootDirectory),
			)
			if err != nil {
				logrus.WithError(err).WithField("driver", instance.Type)
			}
			pool := mapPool(&instance, runnerName)
			pool.Driver = driver
			pools = append(pools, pool)
		case string(types.Google):
			var g, ok = instance.Spec.(*config.Google)
			if !ok {
				logrus.Errorln("unable to parse pool file")
			}
			// set platform defaults
			platform, platformErr := anka.SetPlatformDefaults(&instance.Platform)
			if platformErr != nil {
				logrus.WithError(platformErr).WithField("driver", instance.Type)
			}
			instance.Platform = *platform
			var driver, err = google.New(
				google.WithRootDirectory(&instance.Platform),
				google.WithDiskSize(g.Disk.Size),
				google.WithDiskType(g.Disk.Type),
				google.WithMachineImage(g.Image),
				google.WithSize(g.MachineType),
				google.WithNetwork(g.Network),
				google.WithSubnetwork(g.Subnetwork),
				google.WithPrivateIP(g.PrivateIP),
				google.WithServiceAccountEmail(g.Account.ServiceAccountEmail),
				google.WithProject(g.Account.ProjectID),
				google.WithJSONPath(g.Account.JSONPath),
				google.WithTags(g.Tags...),
				google.WithScopes(g.Scopes...),
				google.WithUserData(g.UserData, g.UserDataPath),
				google.WithZones(g.Zone...),
				google.WithUserDataKey(g.UserDataKey, instance.Platform.OS),
			)
			if err != nil {
				logrus.WithError(err).WithField("driver", instance.Type)
			}
			pool := mapPool(&instance, runnerName)
			pool.Driver = driver
			pools = append(pools, pool)
		case string(types.Anka):
			var ak, ok = instance.Spec.(*config.Anka)
			if !ok {
				logrus.Errorln("unable to parse pool file")
			}
			// set platform defaults
			platform, platformErr := anka.SetPlatformDefaults(&instance.Platform)
			if platformErr != nil {
				logrus.WithError(platformErr).WithField("driver", instance.Type)
			}
			instance.Platform = *platform
			driver, err := anka.New(
				anka.WithUsername(ak.Account.Username),
				anka.WithPassword(ak.Account.Password),
				anka.WithRootDirectory(ak.RootDirectory),
				anka.WithUserData(ak.UserData, ak.UserDataPath),
				anka.WithVMID(ak.VMID),
			)
			if err != nil {
				logrus.WithError(err).WithField("driver", instance.Type)
			}
			pool := mapPool(&instance, runnerName)
			pool.Driver = driver
			pools = append(pools, pool)
		case string(types.DigitalOcean):
			var do, ok = instance.Spec.(*config.DigitalOcean)
			if !ok {
				logrus.Errorln("unable to parse pool file")
			}
			// set platform defaults
			platform, platformErr := anka.SetPlatformDefaults(&instance.Platform)
			if platformErr != nil {
				logrus.WithError(platformErr).WithField("driver", instance.Type)
			}
			instance.Platform = *platform
			driver, err := digitalocean.New(
				digitalocean.WithPAT(do.Account.PAT),
				digitalocean.WithRegion(do.Account.Region),
				digitalocean.WithSize(do.Size),
				digitalocean.WithFirewallID(do.FirewallID),
				digitalocean.WithTags(do.Tags),
				digitalocean.WithSSHKeys(do.SSHKeys),
				digitalocean.WithImage(do.Image),
				digitalocean.WithUserData(do.UserData, do.UserDataPath),
			)
			if err != nil {
				logrus.WithError(err).WithField("driver", instance.Type)
			}
			pool := mapPool(&instance, runnerName)
			pool.Driver = driver
			pools = append(pools, pool)

		default:
			return nil, fmt.Errorf("unknown instance type %s", instance.Type)
		}
	}
	return pools, nil
}

func mapPool(instance *config.Instance, runnerName string) (pool drivers.Pool) {
	// set pool defaults
	if instance.Pool < 0 {
		instance.Pool = 0
	}
	if instance.Limit <= 0 {
		instance.Limit = 100
	}
	if instance.Pool > instance.Limit {
		instance.Limit = instance.Pool
	}

	pool = drivers.Pool{
		RunnerName: runnerName,
		Name:       instance.Name,
		MaxSize:    instance.Limit,
		MinSize:    instance.Pool,
		Platform:   instance.Platform,
	}
	return pool
}

func ConfigPoolFile(path string, conf *config.EnvConfig) (pool *config.PoolFile, err error) {
	if path == "" {
		logrus.Infof("no pool file provided")
		switch {
		case conf.AWS.AccessKeyID != "" || conf.AWS.AccessKeySecret != "":
			logrus.Infoln("in memory pool is using amazon")
			return createAmazonPool(conf.AWS.AccessKeyID, conf.AWS.AccessKeySecret, conf.AWS.Region, conf.Settings.MinPoolSize, conf.Settings.MaxPoolSize), nil
		case conf.DigitalOcean.PAT != "":
			logrus.Infoln("in memory pool is using digitalocean")
			return createDigitalOceanPool(conf.DigitalOcean.PAT, conf.Settings.MinPoolSize, conf.Settings.MaxPoolSize), nil
		case conf.Google.ProjectID != "":
			logrus.Infoln("in memory pool is using google")
			if checkGoogleCredentialsExist(conf.Google.JSONPath) {
				logrus.Printf("google:unable to find credentials file at '%s' or set GOOGLE_JSON_PATH to the correct location", conf.Google.JSONPath)
			}
			return createGooglePool(conf.Google.ProjectID, conf.Google.JSONPath, conf.Google.Zone, conf.Settings.MinPoolSize, conf.Settings.MaxPoolSize), nil
		case conf.Anka.VMName != "":
			return createAnkaPool(conf.Anka.VMName, conf.Settings.MinPoolSize, conf.Settings.MaxPoolSize), nil
		default:
			return pool,
				fmt.Errorf("unsupported driver, please choose a driver setting the manditory environment variables:\n " +
					"for amazon AWS_ACCESS_KEY_ID and AWS_ACCESS_KEY_SECRET\n " +
					"for google GOOGLE_PROJECT_ID\n " +
					"for Anka ANKA_VM_NAME\n " +
					"for digitalocean DIGITALOCEAN_PAT")
		}
	}
	pool, err = config.ParseFile(path)
	if err != nil {
		logrus.WithError(err).
			WithField("path", path).
			Errorln("exec: unable to parse pool file")
	}
	return pool, err
}

func PrintPoolFile(pool *config.PoolFile) {
	marshalledPool, marshalErr := yaml.Marshal(pool)
	if marshalErr != nil {
		logrus.WithError(marshalErr).
			Errorln("unable to marshal pool file, cannot print")
	}
	fmt.Printf("Pool file:\n%s\n", marshalledPool)
}

func checkGoogleCredentialsExist(path string) bool {
	absPath := filepath.Clean(path)
	if strings.HasPrefix(absPath, "~/") {
		dirname, _ := os.UserHomeDir()
		absPath = filepath.Join(dirname, absPath[2:])
	}
	_, pathErr := os.Stat(absPath)
	return !errors.Is(pathErr, os.ErrNotExist)
}

func createAmazonPool(accessKeyID, accessKeySecret, region string, minPoolSize, maxPoolSize int) *config.PoolFile {
	instance := config.Instance{
		Name:    DefaultPoolName,
		Default: true,
		Type:    string(types.Amazon),
		Pool:    minPoolSize,
		Limit:   maxPoolSize,
		Platform: types.Platform{
			Arch: oshelp.ArchAMD64,
			OS:   oshelp.OSLinux,
		},
		Spec: &config.Amazon{
			Account: config.AmazonAccount{
				Region:          region,
				AccessKeyID:     accessKeyID,
				AccessKeySecret: accessKeySecret,
			},
			AMI:  "ami-051197ce9cbb023ea",
			Size: "t2.micro",
		},
	}
	poolfile := config.PoolFile{
		Version:   "1",
		Instances: []config.Instance{instance},
	}

	return &poolfile
}

func createDigitalOceanPool(pat string, minPoolSize, maxPoolSize int) *config.PoolFile {
	instance := config.Instance{
		Name:    DefaultPoolName,
		Default: true,
		Type:    string(types.DigitalOcean),
		Pool:    minPoolSize,
		Limit:   maxPoolSize,
		Platform: types.Platform{
			Arch: oshelp.ArchAMD64,
			OS:   oshelp.OSLinux,
		},
		Spec: &config.DigitalOcean{
			Account: config.DigitalOceanAccount{
				PAT: pat,
			},
		},
	}
	poolfile := config.PoolFile{
		Version:   "1",
		Instances: []config.Instance{instance},
	}

	return &poolfile
}

func createGooglePool(projectID, path, zone string, minPoolSize, maxPoolSize int) *config.PoolFile {
	instance := config.Instance{
		Name:    DefaultPoolName,
		Default: true,
		Type:    string(types.Google),
		Pool:    minPoolSize,
		Limit:   maxPoolSize,
		Platform: types.Platform{
			Arch: "amd64",
			OS:   "linux",
		},
		Spec: &config.Google{
			Account: config.GoogleAccount{
				ProjectID: projectID,
				JSONPath:  path,
			},
			Image:       "projects/ubuntu-os-pro-cloud/global/images/ubuntu-pro-1804-bionic-v20220131",
			MachineType: "e2-small",
			Zone:        []string{zone},
		},
	}
	poolfile := config.PoolFile{
		Version:   "1",
		Instances: []config.Instance{instance},
	}

	return &poolfile
}

func createAnkaPool(vmName string, minPoolSize, maxPoolSize int) *config.PoolFile {
	instance := config.Instance{
		Name:    DefaultPoolName,
		Default: true,
		Type:    string(types.Anka),
		Pool:    minPoolSize,
		Limit:   maxPoolSize,
		Platform: types.Platform{
			Arch: oshelp.ArchAMD64,
			OS:   oshelp.OSMac,
		},
		Spec: &config.Anka{
			VMID: vmName,
		},
	}
	poolfile := config.PoolFile{
		Version:   "1",
		Instances: []config.Instance{instance},
	}

	return &poolfile
}
