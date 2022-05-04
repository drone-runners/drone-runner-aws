package anka

import (
	"github.com/sirupsen/logrus"
	"os"
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

func WithVmID(vmID string) Option {
	return func(p *provider) {
		p.vmId = vmID
	}
}

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

// WithRootDirectory sets the root directory for the virtual machine.
func WithRootDirectory(dir string) Option {
	return func(p *provider) {
		p.rootDir = tempdir(dir)
	}
}
