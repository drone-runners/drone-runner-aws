package poolfile

import (
	"fmt"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/drivers/amazon"
	"github.com/drone-runners/drone-runner-aws/internal/drivers/google"

	"github.com/sirupsen/logrus"
)

func ProcessPool(poolFile *config.PoolFile, runnerName string) ([]drivers.Pool, error) {
	var pools = []drivers.Pool{}

	for _, i := range poolFile.Instances {
		switch i.Type {
		case "amazon":
			var a, ok = i.Spec.(*config.Amazon)
			if !ok {
				logrus.Errorln("daemon: unable to parse pool file")
			}
			var pool, err = amazon.New(
				amazon.WithLimit(i.Limit),
				amazon.WithPool(i.Pool),
				amazon.WithArch(i.Platform.Arch),
				amazon.WithOs(i.Platform.OS),
				amazon.WithAccessKeyID(a.Account.AccessKeyID),
				amazon.WithSecretAccessKey(a.Account.AccessKeySecret),
				amazon.WithZone(a.Account.AvailabilityZone),
				amazon.WithRunnerName(runnerName),
				amazon.WithKeyPair(a.Account.KeyPairName),
				amazon.WithName(i.Name), // pool name
				amazon.WithDeviceName(a.DeviceName),
				amazon.WithRootDirectory(a.RootDirectory),
				amazon.WithAMI(a.AMI),
				amazon.WithRegion(a.Account.Region),
				amazon.WithRetries(a.Account.Retries),
				amazon.WithPrivateIP(a.Network.PrivateIP),
				amazon.WithSecurityGroup(a.Network.SecurityGroups...),
				amazon.WithSize(a.Size),
				amazon.WithSizeAlt(a.SizeAlt),
				amazon.WithSubnet(a.Network.SubnetID),
				amazon.WithUserData(a.UserData),
				amazon.WithVolumeSize(a.Disk.Size),
				amazon.WithVolumeType(a.Disk.Type),
				amazon.WithVolumeIops(a.Disk.Iops),
				amazon.WithIamProfileArn(a.IamProfileArn),
				amazon.WithMarketType(a.MarketType),
			)
			if err != nil {
				logrus.WithError(err).Errorln("daemon: unable to create google config")
			}
			pools = append(pools, pool)
		case "gcp":
			var g, ok = i.Spec.(*config.Google)
			if !ok {
				logrus.Errorln("daemon: unable to parse pool file")
			}
			var pool, err = google.New(
				google.WithRunnerName(runnerName),
				google.WithArch(i.Platform.Arch),
				google.WithOs(i.Platform.OS),
				google.WithLimit(i.Limit),
				google.WithPool(i.Pool),
				google.WithName(i.Name),
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
				google.WithUserDataKey(g.UserDataKey),
			)
			if err != nil {
				logrus.WithError(err).Errorln("daemon: unable to create google config")
			}
			pools = append(pools, pool)
		default:
			return nil, fmt.Errorf("unknown instance type %s", i.Type)
		}
	}
	return pools, nil
}
