package anka

import "github.com/drone-runners/drone-runner-aws/internal/drivers"

type provider struct {
	username    string
	password    string
	MachineName string
	rootDir     string
	vmId        string
	userData    string
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(provider)
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}
