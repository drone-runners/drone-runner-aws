package vmfusion

import (
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
)

type provider struct {
	username string
	password string

	rootDir string

	ISO         string
	MachineName string
	CPU         int64
	Memory      int64
	VDiskPath   string
	StorePath   string

	userData string
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(provider)
	for _, opt := range opts {
		opt(p)
	}
	if p.CPU == 0 {
		p.CPU = 1
	}
	if p.Memory == 0 {
		p.Memory = 1024
	}
	return p, nil
}
