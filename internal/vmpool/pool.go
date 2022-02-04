package vmpool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	RunnerName = "drone-runner-aws"
	TagPrefix  = "runner-"
	TagStageID = TagPrefix + "stage-id"
)

type Pool interface {
	// GetProviderName returns VM provider name. It should be a fixed string for each implementation. The value is used for logging.
	GetProviderName() string

	GetName() string
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
	GetUsedInstanceByTag(ctx context.Context, tag, value string) (inst *Instance, err error)
	Tag(ctx context.Context, instanceID string, tags map[string]string) (err error)
	TagAsInUse(ctx context.Context, instanceID string) (err error)
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
	RunnerName         string
	AwsAccessKeyID     string
	AwsAccessKeySecret string
	AwsRegion          string
	AwsKeyPairName     string
	PrivateKeyFile     string
	PublicKeyFile      string
	LiteEnginePath     string
	CaCertFile         string
	CertFile           string
	KeyFile            string
}

func (def *DefaultSettings) LoadKeys() (privateKey, publicKey string, err error) {
	if def.PrivateKeyFile == "" && def.PublicKeyFile == "" {
		return
	}

	if def.PrivateKeyFile == "" && def.PublicKeyFile != "" || def.PrivateKeyFile != "" && def.PublicKeyFile == "" {
		err = errors.New("both keys, private and public, must be provided")
		return
	}

	if _, statErr := os.Stat(def.PrivateKeyFile); statErr != nil && !os.IsNotExist(statErr) {
		return
	}
	if _, statErr := os.Stat(def.PublicKeyFile); statErr != nil && !os.IsNotExist(statErr) {
		return
	}

	var body []byte

	body, err = os.ReadFile(def.PrivateKeyFile)
	if err != nil {
		err = fmt.Errorf("unable to read private key file: %w", err)
		return
	}

	privateKey = string(body)

	body, err = os.ReadFile(def.PublicKeyFile)
	if err != nil {
		err = fmt.Errorf("unable to read public key file: %w", err)
	}

	publicKey = string(body)

	return
}
