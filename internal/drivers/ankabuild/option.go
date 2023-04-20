package ankabuild

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
		platform.OS = oshelp.OSMac
	}
	if platform.OS != oshelp.OSMac {
		return platform, fmt.Errorf("ankabuild - invalid OS %s, has to be '%s'", platform.OS, oshelp.OSMac)
	}

	return platform, nil
}

func WithUsername(username string) Option {
	return func(p *config) {
		p.username = username
	}
}

func WithPassword(password string) Option {
	return func(p *config) {
		p.password = password
	}
}

func WithVMID(vmID string) Option {
	return func(p *config) {
		p.vmID = vmID
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

// WithRootDirectory sets the root directory for the virtual machine.
func WithRootDirectory(dir string) Option {
	return func(p *config) {
		p.rootDir = tempdir(dir)
	}
}

func WithControllerURI(url string) Option {
	return func(p *config) {
		p.controllerURL = url
	}
}

func WithNodeID(nodeID string) Option {
	return func(p *config) {
		p.nodeID = nodeID
	}
}

func WithTag(tag string) Option {
	return func(p *config) {
		p.tag = tag
	}
}

func WithAuthToken(token string) Option {
	return func(p *config) {
		p.authToken = token
	}
}

func WithGroupID(groupID string) Option {
	return func(p *config) {
		p.groupID = groupID
	}
}
