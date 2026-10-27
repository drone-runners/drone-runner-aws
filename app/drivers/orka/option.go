package orka

import (
	"fmt"
	"os"

	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"

	"github.com/sirupsen/logrus"
)

type Option func(*config)

// SetPlatformDefaults defaults/validates the platform for an Orka pool. Orka's deploy path is
// OCI-image-driven and Apple-silicon only (see implementation plan section 4 - Intel nodes use
// NFS cluster-storage images instead, which this driver does not support).
func SetPlatformDefaults(platform *types.Platform) (*types.Platform, error) {
	if platform.OS == "" {
		platform.OS = oshelp.OSMac
	}
	if platform.OS != oshelp.OSMac {
		return platform, fmt.Errorf("orka - invalid OS %s, has to be '%s'", platform.OS, oshelp.OSMac)
	}
	if platform.Arch == "" {
		platform.Arch = oshelp.ArchARM64
	}
	if platform.Arch != oshelp.ArchARM64 {
		return platform, fmt.Errorf("orka - invalid arch %s, has to be '%s' (OCI images are Apple-silicon only)", platform.Arch, oshelp.ArchARM64)
	}

	return platform, nil
}

func WithNamespace(namespace string) Option {
	return func(p *config) {
		p.namespace = namespace
	}
}

func WithControllerURI(url string) Option {
	return func(p *config) {
		p.controllerURL = url
	}
}

func WithAuthToken(token string) Option {
	return func(p *config) {
		p.authToken = token
	}
}

// WithVMConfig sets the Orka vmConfig template name. If both vmConfig and image are set,
// vmConfig wins.
func WithVMConfig(vmConfig string) Option {
	return func(p *config) {
		p.vmConfig = vmConfig
	}
}

func WithImage(image string) Option {
	return func(p *config) {
		p.image = image
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

// WithTimeout sets the deploy/poll timeout in seconds. 0 means "use the driver's default".
func WithTimeout(timeoutSec int) Option {
	return func(p *config) {
		p.timeoutSec = timeoutSec
	}
}

// WithCPU sets the number of CPU cores requested for the VM.
func WithCPU(cpu int) Option {
	return func(p *config) {
		p.cpu = cpu
	}
}

// WithMemory sets the amount of memory (GB) requested for the VM.
func WithMemory(memory float64) Option {
	return func(p *config) {
		p.memory = memory
	}
}

// WithVNC enables/disables VNC access to the VM.
func WithVNC(enabled bool) Option {
	return func(p *config) {
		p.vnc = enabled
	}
}

// WithTag sets the node tag the VM is scheduled against, and whether that tag is required.
func WithTag(tag string, required bool) Option {
	return func(p *config) {
		p.tag = tag
		p.tagRequired = required
	}
}

// WithNode pins the VM to a specific Orka node.
func WithNode(node string) Option {
	return func(p *config) {
		p.node = node
	}
}

// WithScheduler selects the Orka scheduler used to place the VM.
func WithScheduler(scheduler string) Option {
	return func(p *config) {
		p.scheduler = scheduler
	}
}

// WithDisplay sets the VM's virtual display resolution/DPI.
func WithDisplay(width, height, dpi int) Option {
	return func(p *config) {
		p.displayWidth = width
		p.displayHeight = height
		p.displayDPI = dpi
	}
}

// WithCustomMetadata sets customer-supplied Orka customMetadata key/value pairs. The driver's
// own "startup_script" key always wins over a customer-supplied collision (see Create()).
func WithCustomMetadata(metadata map[string]string) Option {
	return func(p *config) {
		p.customMetadata = metadata
	}
}
