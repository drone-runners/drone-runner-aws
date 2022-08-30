package google

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
	if platform.OS != oshelp.OSLinux && platform.OS != oshelp.OSWindows {
		return platform, fmt.Errorf("invalid OS %s, has to be either'%s/%s'", platform.OS, oshelp.OSLinux, oshelp.OSWindows)
	}

	return platform, nil
}

// WithRootDirectory returns an OS specific temp directory
func WithRootDirectory(platform *types.Platform) Option {
	return func(p *config) {
		const dir = "gcp"
		switch platform.OS {
		case oshelp.OSWindows:
			p.rootDir = oshelp.JoinPaths(platform.OS, "C:\\Windows\\Temp", dir)
		default:
			p.rootDir = oshelp.JoinPaths(platform.OS, "/tmp", dir)
		}
	}
}

// WithDiskSize returns an option to set the instance disk size in gigabytes.
func WithDiskSize(diskSize int64) Option {
	return func(p *config) {
		if diskSize == 0 {
			p.diskSize = 50
		} else {
			p.diskSize = diskSize
		}
	}
}

// WithDiskType returns an option to set the instance disk type.
func WithDiskType(diskType string) Option {
	return func(p *config) {
		if diskType == "" {
			p.diskType = "pd-standard"
		} else {
			p.diskType = diskType
		}
	}
}

// WithMachineImage returns an option to set the image.
func WithMachineImage(image string) Option {
	return func(p *config) {
		if image == "" {
			p.image = "ubuntu-os-cloud/global/images/ubuntu-1604-xenial-v20170721"
		} else {
			p.image = image
		}
	}
}

// WithSize returns an option to set the instance type.
func WithSize(size string) Option {
	return func(p *config) {
		if size == "" {
			p.size = "n1-standard-1"
		} else {
			p.size = size
		}
	}
}

// WithNetwork returns an option to set the network.
func WithNetwork(network string) Option {
	return func(p *config) {
		if network == "" {
			p.network = "default"
		} else {
			p.network = network
		}
	}
}

// WithSubnetwork returns an option to set the subnetwork.
func WithSubnetwork(subnetwork string) Option {
	return func(p *config) {
		p.subnetwork = subnetwork
	}
}

// WithPrivateIP returns an option to set the private IP address.
func WithPrivateIP(private bool) Option {
	return func(p *config) {
		p.privateIP = private
	}
}

// WithProject returns an option to set the project.
func WithProject(project string) Option {
	return func(p *config) {
		p.projectID = project
	}
}

// WithJSONPath returns an option to set the json path
func WithJSONPath(path string) Option {
	return func(p *config) {
		p.JSONPath = path
	}
}

// WithTags returns an option to set the resource tags.
func WithTags(tags ...string) Option {
	return func(p *config) {
		if len(tags) == 0 {
			p.tags = defaultTags
		} else {
			p.tags = tags
		}
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

// WithUserDataKey allows to set the user data key for Google Cloud Platform
// This allows user to set either user-data or a startup script
func WithUserDataKey(text, platform string) Option {
	return func(p *config) {
		p.userDataKey = text
		if p.userDataKey == "" && platform == oshelp.OSLinux {
			p.userDataKey = "user-data"
		} else {
			p.userDataKey = "windows-startup-script-ps1"
		}
	}
}

// WithZones WithZone returns an option to set the target zone.
func WithZones(zones ...string) Option {
	return func(p *config) {
		if len(zones) == 0 {
			p.zones = []string{"us-central1-a"}
		} else {
			p.zones = zones
		}
	}
}

// WithScopes returns an option to set the scopes.
func WithScopes(scopes ...string) Option {
	return func(p *config) {
		if len(scopes) == 0 {
			p.scopes = defaultScopes
		} else {
			p.scopes = scopes
		}
	}
}

// WithServiceAccountEmail returns an option to set the ServiceAccountEmail.
func WithServiceAccountEmail(email string) Option {
	return func(p *config) {
		if email == "" {
			p.serviceAccountEmail = "default"
		} else {
			p.serviceAccountEmail = email
		}
	}
}

// WithNoServiceAccount returns an option to set the NoServiceAccount.
// It does not mount the service account on the vm.
func WithNoServiceAccount(v bool) Option {
	return func(p *config) {
		p.noServiceAccount = v
	}
}
