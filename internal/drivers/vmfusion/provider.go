package vmfusion

import (
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/oshelp"
)

type provider struct {
	runnerName string
	name       string

	username string
	password string

	os      string
	arch    string
	rootDir string
	Version string

	ISO         string
	MachineName string
	CPU         int64
	Memory      int64
	VDiskPath   string
	StorePath   string

	pool  int
	limit int

	userData string
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(provider)
	for _, opt := range opts {
		opt(p)
	}
	if p.pool < 0 {
		p.pool = 0
	}
	if p.limit <= 0 {
		p.limit = 100
	}

	if p.pool > p.limit {
		p.limit = p.pool
	}
	// apply defaults to Platform
	if p.os == "" {
		p.os = oshelp.OSMac
	}
	if p.arch == "" {
		p.arch = "amd64"
	}
	if p.CPU == 0 {
		p.CPU = 1
	}
	if p.Memory == 0 {
		p.Memory = 1024
	}
	return p, nil
}
