package vmpool

import (
	"context"
	"time"
)

const (
	RunnerName     = "drone-runner-cloud"
	TagPrefix      = "runner-"
	TagStageID     = TagPrefix + "stage-id"
	TagStatus      = TagPrefix + "status"
	TagRunner      = TagPrefix + "name"
	TagCreator     = TagPrefix + "creator"
	TagPool        = TagPrefix + "pool"
	TagStatusValue = "in-use"
)

type Pool interface {
	// GetProviderName returns VM provider name. It should be a fixed string for each implementation. The value is used for logging.
	GetProviderName() string

	GetName() string
	GetOS() string
	GetRootDir() string

	// GetMaxSize and GetMinSize should be used for managing pool size: Number of VM instances available in the pool.
	GetMaxSize() int
	GetMinSize() int

	Ping(ctx context.Context) error
	Provision(ctx context.Context, tagAsInUse bool) (instance *Instance, err error)
	List(ctx context.Context) (busy, free []Instance, err error)
	GetUsedInstanceByTag(ctx context.Context, tag, value string) (inst *Instance, err error)
	Tag(ctx context.Context, instanceID string, tags map[string]string) (err error)
	Destroy(ctx context.Context, instanceIDs ...string) (err error)
}

// Instance represents a provisioned server instance.
type Instance struct {
	ID        string
	IP        string
	Tags      map[string]string
	StartedAt time.Time
}

// Platform defines the target platform.
type Platform struct {
	OS      string `json:"os,omitempty"`
	Arch    string `json:"arch,omitempty"`
	Variant string `json:"variant,omitempty"`
	Version string `json:"version,omitempty"`
}

type DefaultSettings struct {
	RunnerName          string
	AwsAccessKeyID      string
	AwsAccessKeySecret  string
	AwsRegion           string
	AwsAvailabilityZone string
	AwsKeyPairName      string
	LiteEnginePath      string
	CaCertFile          string
	CertFile            string
	KeyFile             string
}
