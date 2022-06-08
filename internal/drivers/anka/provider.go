package anka

import "github.com/drone-runners/drone-runner-aws/internal/drivers"

type config struct {
	username string
	password string
	rootDir  string
	vmID     string
	userData string
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(config)
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}
