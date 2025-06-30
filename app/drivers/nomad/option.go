package nomad

import (
	"os"

	cf "github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/sirupsen/logrus"
)

type Option func(*config)

func WithAddress(s string) Option {
	return func(p *config) {
		p.address = s
	}
}

func WithCaCertPath(s string) Option {
	return func(p *config) {
		p.caCertPath = s
	}
}

func WithClientCertPath(s string) Option {
	return func(p *config) {
		p.clientCertPath = s
	}
}

func WithClientKeyPath(s string) Option {
	return func(p *config) {
		p.clientKeyPath = s
	}
}

func WithEnablePinning(s map[string]string) Option {
	return func(p *config) {
		p.enablePinning = s
	}
}

func WithInsecure(b bool) Option {
	return func(p *config) {
		p.insecure = b
	}
}

func WithImage(s string) Option {
	return func(p *config) {
		p.vmImage = s
		if p.vmImage == "" {
			p.vmImage = "weaveworks/ignite-ubuntu:latest"
		}
	}
}

func WithNoop(b bool) Option {
	return func(p *config) {
		p.noop = b
	}
}

func WithMemory(s string) Option {
	return func(p *config) {
		p.vmMemoryGB = s
		if p.vmMemoryGB == "" {
			p.vmMemoryGB = "6"
		}
	}
}

func WithCpus(s string) Option {
	return func(p *config) {
		p.vmCpus = s
		if p.vmCpus == "" {
			p.vmCpus = "2"
		}
	}
}

func WithDiskSize(s string) Option {
	return func(p *config) {
		p.vmDiskSize = s
		if p.vmDiskSize == "" {
			p.vmDiskSize = "100GB"
		}
	}
}

func WithResource(resource map[string]cf.NomadResource) Option {
	return func(p *config) {
		p.resource = resource
	}
}

func WithUsername(s string) Option {
	return func(p *config) {
		p.username = s
	}
}

func WithPassword(s string) Option {
	return func(p *config) {
		p.password = s
	}
}

func WithMachinePassword(s string) Option {
	return func(p *config) {
		p.machinePassword = s
	}
}

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

func WithVirtualizerEngine(driver string) Option {
	return func(p *config) {
		p.virtualizerEngine = driver
	}
}
