package amazon

import (
	"os"

	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
	"github.com/drone-runners/drone-runner-aws/oshelp"
)

type Option func(*provider)

// WithRunnerName returns an option to set the runner name
func WithRunnerName(name string) Option {
	return func(p *provider) {
		p.runnerName = name
	}
}

func WithOs(machineOs string) Option {
	return func(p *provider) {
		p.os = machineOs
	}
}

func WithArch(arch string) Option {
	return func(p *provider) {
		p.arch = arch
	}
}

func WithAccessKeyID(accessKeyID string) Option {
	return func(p *provider) {
		p.accessKeyID = accessKeyID
	}
}

// WithSecretAccessKey sets the AWS secret access key.
func WithSecretAccessKey(secretAccessKey string) Option {
	return func(p *provider) {
		p.secretAccessKey = secretAccessKey
	}
}

// WithRootDirectory sets the root directory for the virtual machine.
func WithRootDirectory(dir string) Option {
	return func(p *provider) {
		p.rootDir = tempdir(dir)
	}
}

// WithDeviceName returns an option to set the device name.
func WithDeviceName(n string) Option {
	return func(p *provider) {
		p.deviceName = n
	}
}

// WithAMI returns an option to set the image.
func WithAMI(ami string) Option {
	return func(p *provider) {
		p.image = ami
	}
}

// WithPrivateIP returns an option to set the private IP address.
func WithPrivateIP(private bool) Option {
	return func(p *provider) {
		p.allocPublicIP = !private
	}
}

// WithRetries returns an option to set the retry count.
func WithRetries(retries int) Option {
	return func(p *provider) {
		p.retries = retries
	}
}

// WithRegion returns an option to set the target region.
func WithRegion(region string) Option {
	return func(p *provider) {
		p.region = region
	}
}

// WithSecurityGroup returns an option to set the instance size.
func WithSecurityGroup(group ...string) Option {
	return func(p *provider) {
		p.groups = group
	}
}

// WithSize returns an option to set the instance size.
func WithSize(size string) Option {
	return func(p *provider) {
		p.size = size
	}
}

// WithSizeAlt returns an option to set the alternate instance
// size. If instance creation fails, the system will attempt to
// provision a second instance using the alternate size.
func WithSizeAlt(size string) Option {
	return func(p *provider) {
		p.sizeAlt = size
	}
}

// WithSubnet returns an option to set the subnet id.
func WithSubnet(id string) Option {
	return func(p *provider) {
		p.subnet = id
	}
}

// WithTags returns an option to set the image.
func WithTags(tags map[string]string) Option {
	return func(p *provider) {
		p.tags = tags
	}
}

// WithUserData returns an option to set the cloud-init
// template from text.
func WithUserData(text string, params *cloudinit.Params) Option {
	return func(p *provider) {
		if text == "" {
			if params == nil {
				return
			}
			params.Platform = p.os
			params.Architecture = p.arch
			if p.os == oshelp.OSWindows {
				p.userData = cloudinit.Windows(params)
			} else {
				p.userData = cloudinit.Linux(params)
			}
			return
		}
		data, err := os.ReadFile(text)
		if err != nil {
			logrus.Error(err)
			return
		}
		p.userData, _ = cloudinit.Custom(string(data), params)
	}
}

// WithVolumeSize returns an option to set the volume size
// in gigabytes.
func WithVolumeSize(s int64) Option {
	return func(p *provider) {
		p.volumeSize = s
	}
}

// WithVolumeType returns an option to set the volume type.
func WithVolumeType(t string) Option {
	return func(p *provider) {
		p.volumeType = t
	}
}

// WithVolumeIops returns an option to set the volume iops.
func WithVolumeIops(i int64) Option {
	return func(p *provider) {
		p.volumeIops = i
	}
}

// WithIamProfileArn returns an option to set the iam profile arn.
func WithIamProfileArn(t string) Option {
	return func(p *provider) {
		p.iamProfileArn = t
	}
}

// WithMarketType returns an option to set the instance market type.
func WithMarketType(t string) Option {
	return func(p *provider) {
		p.spotInstance = t == "spot"
	}
}

// WithName returns an option to set the instance name.
func WithName(name string) Option {
	return func(p *provider) {
		p.name = name
	}
}

// WithLimit the total number of running servers. If exceeded block or error.
func WithLimit(limit int) Option {
	return func(p *provider) {
		p.limit = limit
	}
}

// WithPool total number of warm instances in the pool at all times
func WithPool(pool int) Option {
	return func(p *provider) {
		p.pool = pool
	}
}

// WithZone returns an option to set the zone.
func WithZone(zone string) Option {
	return func(p *provider) {
		p.availabilityZone = zone
	}
}

// WithKeyPair returns an option to set the key pair.
func WithKeyPair(keyPair string) Option {
	return func(p *provider) {
		p.keyPairName = keyPair
	}
}
