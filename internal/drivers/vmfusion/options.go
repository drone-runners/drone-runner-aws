package vmfusion

import (
	"os"

	"github.com/sirupsen/logrus"
)

type Option func(*provider)

// WithRunnerName returns an option to set the runner name
func WithRunnerName(name string) Option {
	return func(p *provider) {
		p.runnerName = name
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

// WithName sets pool name
func WithName(name string) Option {
	return func(p *provider) {
		p.name = name
	}
}

func WithUsername(username string) Option {
	return func(p *provider) {
		p.username = username
	}
}

func WithPassword(password string) Option {
	return func(p *provider) {
		p.password = password
	}
}

func WithISO(iso string) Option {
	return func(p *provider) {
		p.ISO = iso
	}
}

func WithCPU(cpu int64) Option {
	return func(p *provider) {
		p.CPU = cpu
	}
}

func WithMemory(memory int64) Option {
	return func(p *provider) {
		p.Memory = memory
	}
}

func WithVDiskPath(vDiskPath string) Option {
	return func(p *provider) {
		p.VDiskPath = vDiskPath
	}
}

func WithVersion(version string) Option {
	return func(p *provider) {
		p.Version = version
	}
}

// WithUserData returns an option to set the cloud-init
// template from text.
func WithUserData(text string) Option {
	return func(p *provider) {
		if text != "" {
			data, err := os.ReadFile(text)
			if err != nil {
				logrus.Error(err)
				return
			}
			p.userData = string(data)
		}
	}
}

// WithStorePath the path where VM machines are stored
func WithStorePath(storePath string) Option {
	return func(p *provider) {
		p.StorePath = storePath
	}
}

// WithRootDirectory sets the root directory for the virtual machine.
func WithRootDirectory(dir string) Option {
	return func(p *provider) {
		p.rootDir = tempdir(dir)
	}
}
