package poolfile

import (
	"fmt"

	"github.com/drone-runners/drone-runner-aws/oshelp"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/drivers/amazon"
	"github.com/drone-runners/drone-runner-aws/internal/drivers/google"
	"github.com/drone-runners/drone-runner-aws/internal/drivers/vmfusion"
	"github.com/sirupsen/logrus"
)

func ProcessPool(poolFile *config.PoolFile, runnerName string) ([]drivers.Pool, error) {
	var pools = []drivers.Pool{}

	for _, i := range poolFile.Instances {
		i := i
		switch i.Type {
		case "vmfusion":
			var v, ok = i.Spec.(*config.VMFusion)
			if !ok {
				logrus.Errorln("daemon: unable to parse pool file")
			}
			driver, err := vmfusion.New(
				vmfusion.WithStorePath(v.StorePath),
				vmfusion.WithUsername(v.Account.Username),
				vmfusion.WithPassword(v.Account.Password),
				vmfusion.WithISO(v.ISO),
				vmfusion.WithCPU(v.CPU),
				vmfusion.WithMemory(v.Memory),
				vmfusion.WithVDiskPath(v.VDiskPath),
				vmfusion.WithUserData(v.UserData),
				vmfusion.WithRootDirectory(v.RootDirectory),
			)
			if err != nil {
				logrus.WithError(err).Errorln("daemon: unable to create vmfusion config")
			}
			pool := mapPool(&i, runnerName)
			pool.Driver = driver
			pools = append(pools, pool)
		case "amazon":
			var a, ok = i.Spec.(*config.Amazon)
			if !ok {
				logrus.Errorln("daemon: unable to parse pool file")
			}
			var driver, err = amazon.New(
				amazon.WithAccessKeyID(a.Account.AccessKeyID),
				amazon.WithSecretAccessKey(a.Account.AccessKeySecret),
				amazon.WithZone(a.Account.AvailabilityZone),
				amazon.WithKeyPair(a.Account.KeyPairName),
				amazon.WithDeviceName(a.DeviceName),
				amazon.WithRootDirectory(a.RootDirectory),
				amazon.WithAMI(a.AMI),
				amazon.WithUser(a.User, i.Platform.OS),
				amazon.WithRegion(a.Account.Region),
				amazon.WithRetries(a.Account.Retries),
				amazon.WithPrivateIP(a.Network.PrivateIP),
				amazon.WithSecurityGroup(a.Network.SecurityGroups...),
				amazon.WithSize(a.Size, i.Platform.Arch),
				amazon.WithSizeAlt(a.SizeAlt),
				amazon.WithSubnet(a.Network.SubnetID),
				amazon.WithUserData(a.UserData),
				amazon.WithVolumeSize(a.Disk.Size),
				amazon.WithVolumeType(a.Disk.Type),
				amazon.WithVolumeIops(a.Disk.Iops),
				amazon.WithIamProfileArn(a.IamProfileArn),
				amazon.WithMarketType(a.MarketType),
				amazon.WithHibernate(a.Hibernate),
			)
			if err != nil {
				logrus.WithError(err).Errorln("daemon: unable to create google config")
			}
			pool := mapPool(&i, runnerName)
			pool.Driver = driver
			pools = append(pools, pool)
		case "gcp":
			var g, ok = i.Spec.(*config.Google)
			if !ok {
				logrus.Errorln("daemon: unable to parse pool file")
			}
			var driver, err = google.New(
				google.WithDiskSize(g.Disk.Size),
				google.WithDiskType(g.Disk.Type),
				google.WithMachineImage(g.Image),
				google.WithMachineType(g.MachineType),
				google.WithNetwork(g.Network),
				google.WithSubnetwork(g.Subnetwork),
				google.WithPrivateIP(g.PrivateIP),
				google.WithServiceAccountEmail(g.Account.ServiceAccountEmail),
				google.WithProject(g.Account.ProjectID),
				google.WithJSONPath(g.Account.JSONPath),
				google.WithTags(g.Tags...),
				google.WithScopes(g.Scopes...),
				google.WithUserData(g.UserData),
				google.WithZones(g.Zone...),
				google.WithUserDataKey(g.UserDataKey, i.Platform.OS),
			)
			if err != nil {
				logrus.WithError(err).Errorln("daemon: unable to create google config")
			}
			pool := mapPool(&i, runnerName)
			pool.Driver = driver
			pools = append(pools, pool)
		default:
			return nil, fmt.Errorf("unknown instance type %s", i.Type)
		}
	}
	return pools, nil
}

func mapPool(i *config.Instance, runnerName string) drivers.Pool {
	if i.Pool < 0 {
		i.Pool = 0
	}
	if i.Limit <= 0 {
		i.Limit = 100
	}
	if i.Pool > i.Limit {
		i.Limit = i.Pool
	}
	if i.Platform.OS == "" {
		if i.Type == "vmfusion" {
			i.Platform.OS = oshelp.OSMac
		} else {
			i.Platform.OS = oshelp.OSLinux
		}
	}
	if i.Platform.Arch == "" {
		i.Platform.Arch = "amd64"
	}
	var pool = drivers.Pool{
		RunnerName: runnerName,
		Name:       i.Name,
		MaxSize:    i.Limit,
		MinSize:    i.Pool,
		OS:         i.Platform.OS,
		Arch:       i.Platform.Arch,
		Version:    i.Platform.Version,
	}
	return pool
}
