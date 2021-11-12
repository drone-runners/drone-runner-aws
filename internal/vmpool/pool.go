package vmpool

import (
	"context"
)

type Pool interface {
	GetName() string
	GetInstanceType() string // TODO: returns AMI, used for logging... probably rename to GetImage
	GetOS() string

	GetUser() string
	GetPrivateKey() string
	GetRootDir() string

	// GetMaxSize and GetMinSize should be used for managing pool size: Number of VM instances available in the pool.
	GetMaxSize() int
	GetMinSize() int

	Provision(ctx context.Context, addBuildingTag bool) (instance *Instance, err error)

	// Create creates a new VM instance in the pool
	Create(ctx context.Context) (*Instance, error)

	// Destroy removes the instance
	Destroy(ctx context.Context, instance *Instance) error

	// TagInstance tags a VM instance with a tag given by key parameter and value parameter as its value.
	TagInstance(ctx context.Context, instanceID, key, value string) error

	CleanPools(ctx context.Context) error
	PoolCountFree(ctx context.Context) (free int, err error)
	TryPool(ctx context.Context) (instance *Instance, err error)
	Ping(ctx context.Context) error
}

// Instance represents a provisioned server instance.
type Instance struct {
	ID string
	IP string
}

// Platform defines the target platform.
type Platform struct {
	OS      string `json:"os,omitempty"`
	Arch    string `json:"arch,omitempty"`
	Variant string `json:"variant,omitempty"`
	Version string `json:"version,omitempty"`
}
