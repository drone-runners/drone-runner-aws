package vmpool

import (
	"context"
	"sync"
)

const RunnerName = "drone-runner-aws"

type Pool interface {
	sync.Locker

	GetProviderName() string

	GetName() string
	GetInstanceType() string // TODO: returns AMI, used for logging... probably rename to GetImage
	GetOS() string
	GetUser() string
	GetPrivateKey() string
	GetRootDir() string

	// GetMaxSize and GetMinSize should be used for managing pool size: Number of VM instances available in the pool.
	GetMaxSize() int
	GetMinSize() int

	Ping(ctx context.Context) error
	Provision(ctx context.Context, tagAsInUse bool) (instance *Instance, err error)
	List(ctx context.Context) (busy, free []Instance, err error)
	Tag(ctx context.Context, instanceID, key, value string) (err error)
	TagAsInUse(ctx context.Context, instanceID string) (err error)
	Destroy(ctx context.Context, instanceIDs ...string) (err error)
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

type DefaultSettings struct {
	RunnerName         string
	AwsAccessKeyID     string
	AwsAccessKeySecret string
	AwsRegion          string
	PrivateKeyFile     string
	PublicKeyFile      string
	LiteEnginePath     string
	CaCertFile         string
	CertFile           string
	KeyFile            string
}
