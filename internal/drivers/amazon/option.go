package amazon

import (
	"fmt"
	"os"

	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"

	"github.com/sirupsen/logrus"
)

type Option func(*config)

func SetPlatformDefaults(platform *types.Platform) (*types.Platform, error) {
	if platform.Arch == "" {
		platform.Arch = oshelp.ArchAMD64
	}
	if platform.Arch != oshelp.ArchAMD64 && platform.Arch != oshelp.ArchARM64 {
		return platform, fmt.Errorf("invalid arch %s, has to be '%s/%s'", platform.Arch, oshelp.ArchAMD64, oshelp.ArchARM64)
	}
	// verify that we are using sane values for OS
	if platform.OS == "" {
		platform.OS = oshelp.OSLinux
	}
	if platform.OS != oshelp.OSLinux && platform.OS != oshelp.OSWindows && platform.OS != oshelp.OSMac {
		return platform, fmt.Errorf("invalid OS %s, has to be one of the following '%s/%s/%s'", platform.OS, oshelp.OSLinux, oshelp.OSWindows, oshelp.OSMac)
	}
	// set osname, we dont separate different versions of windows or mac with osname yet.
	if platform.OS == oshelp.OSLinux {
		if platform.OSName == "" {
			platform.OSName = oshelp.Ubuntu
		}
		if platform.OSName != oshelp.Ubuntu && platform.OSName != oshelp.AmazonLinux {
			return platform, fmt.Errorf("invalid OS Name %s, has to be one of the following '%s/%s'", platform.OSName, oshelp.Ubuntu, oshelp.AmazonLinux)
		}
	}
	return platform, nil
}

func WithAccessKeyID(accessKeyID string) Option {
	return func(p *config) {
		p.accessKeyID = accessKeyID
	}
}

// WithSecretAccessKey sets the AWS secret access key.
func WithSecretAccessKey(secretAccessKey string) Option {
	return func(p *config) {
		p.secretAccessKey = secretAccessKey
	}
}

// WithRootDirectory sets the root directory for the virtual machine.
func WithRootDirectory(dir string) Option {
	return func(p *config) {
		p.rootDir = tempdir(dir)
	}
}

// WithDeviceName returns an option to set the device name.
func WithDeviceName(deviceName, osName string) Option {
	return func(p *config) {
		if p.deviceName == "" {
			if osName == oshelp.AmazonLinux {
				p.deviceName = "/dev/xvda"
			} else {
				p.deviceName = "/dev/sda1"
			}
		} else {
			p.deviceName = deviceName
		}
	}
}

// WithAMI returns an option to set the image.
func WithAMI(ami string) Option {
	return func(p *config) {
		p.image = ami
	}
}

// WithPrivateIP returns an option to set the private IP address.
func WithPrivateIP(private bool) Option {
	return func(p *config) {
		p.allocPublicIP = !private
	}
}

// WithRetries returns an option to set the retry count.
func WithRetries(retries int) Option {
	return func(p *config) {
		if retries == 0 {
			p.retries = 10
		} else {
			p.retries = retries
		}
	}
}

// WithRegion returns an option to set the target region.
func WithRegion(region, zone string) Option {
	return func(p *config) {
		if region == "" && zone != "" {
			// Only set region if zone not set
			p.region = "us-east-2"
		} else {
			p.region = region
		}
	}
}

// WithSecurityGroup returns an option to set the instance size.
func WithSecurityGroup(group ...string) Option {
	return func(p *config) {
		p.groups = group
	}
}

// WithSize returns an option to set the instance size.
func WithSize(size, arch string) Option {
	return func(p *config) {
		p.size = size
		// set default instance type if not provided
		if p.size == "" {
			if arch == oshelp.ArchARM64 {
				p.size = "a1.medium"
			} else {
				p.size = "t3.nano"
			}
		}
	}
}

// WithSizeAlt returns an option to set the alternate instance
// size. If instance creation fails, the system will attempt to
// provision a second instance using the alternate size.
func WithSizeAlt(size string) Option {
	return func(p *config) {
		p.sizeAlt = size
	}
}

// WithSubnet returns an option to set the subnet id.
func WithSubnet(id string) Option {
	return func(p *config) {
		p.subnet = id
	}
}

// WithUserData returns an option to set the cloud-init template from a file location or passed in text.
func WithUserData(text, path string) Option {
	if text != "" {
		return func(p *config) {
			p.userData = text
		}
	}
	return func(p *config) {
		if path != "" {
			data, err := os.ReadFile(path)
			if err != nil {
				logrus.WithError(err).
					Fatalln("failed to read user_data file")
				return
			}
			p.userData = string(data)
		}
	}
}

// WithVolumeSize returns an option to set the volume size in gigabytes.
func WithVolumeSize(s int64) Option {
	return func(p *config) {
		if s == 0 {
			p.volumeSize = 32
		} else {
			p.volumeSize = s
		}
	}
}

// WithVolumeType returns an option to set the volume type.
func WithVolumeType(t string) Option {
	return func(p *config) {
		if t == "" {
			p.volumeType = "gp2"
		} else {
			p.volumeType = t
		}
	}
}

// WithVolumeIops returns an option to set the volume iops.
func WithVolumeIops(iops int64, diskType string) Option {
	return func(p *config) {
		if diskType == "io1" && iops == 0 {
			p.volumeIops = 100
		} else {
			p.volumeIops = iops
		}
	}
}

// WithIamProfileArn returns an option to set the iam profile arn.
func WithIamProfileArn(t string) Option {
	return func(p *config) {
		p.iamProfileArn = t
	}
}

// WithVpc returns an option to set the vpc.
func WithVpc(t string) Option {
	return func(p *config) {
		p.vpc = t
	}
}

// WithMarketType returns an option to set the instance market type.
func WithMarketType(t string) Option {
	return func(p *config) {
		p.spotInstance = t == "spot"
	}
}

// WithZone returns an option to set the zone.
func WithZone(zone string) Option {
	return func(p *config) {
		p.availabilityZone = zone
	}
}

// WithKeyPair returns an option to set the key pair.
func WithKeyPair(keyPair string) Option {
	return func(p *config) {
		p.keyPairName = keyPair
	}
}

func WithHibernate(hibernate bool) Option {
	return func(p *config) {
		p.hibernate = hibernate
	}
}

// WithTags returns a list of tags to apply to the instance.
func WithTags(t map[string]string) Option {
	return func(p *config) {
		p.tags = t
	}
}

func WithUser(user, platform string) Option {
	return func(p *config) {
		p.user = user
		// set the default ssh user. this user account is responsible for executing the pipeline script.
		if p.user == "" {
			if platform == oshelp.OSWindows {
				p.user = "Administrator"
			} else {
				p.user = "root"
			}
		}
	}
}
