package vmfusion

import (
	"os"

	"github.com/sirupsen/logrus"
)

type Option func(*provider)

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
