package google

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/buildkite/yaml"
	"github.com/drone/runner-go/logger"

	"github.com/drone-runners/drone-runner-aws/internal/vmpool"
)

type (
	poolDefinition struct {
		Name        string   `json:"name,omitempty"`
		MinPoolSize int      `json:"min_pool_size,omitempty" yaml:"min_pool_size"`
		MaxPoolSize int      `json:"max_pool_size,omitempty" yaml:"max_pool_size"`
		InitScript  string   `json:"init_script,omitempty" yaml:"init_script"`
		Platform    platform `json:"platform,omitempty"`
		Account     account  `json:"account,omitempty"`
		Instance    instance `json:"instance,omitempty"`
	}

	account struct {
		ProjectID string `json:"project_id,omitempty"  yaml:"project_id"`
		Zone      string `json:"zone,omitempty"  yaml:"zone"`
		JsonPath  string `json:"json_path,omitempty"  yaml:"json_path"`
	}

	platform struct {
		OS      string `json:"os,omitempty"`
		Arch    string `json:"arch,omitempty"`
		Variant string `json:"variant,omitempty"`
		Version string `json:"version,omitempty"`
	}

	instance struct {
		Name       string            `json:"name,omitempty"`
		Tags       map[string]string `json:"tags,omitempty"`
		Size       string            `json:"size,omitempty"`
		Type       string            `json:"type,omitempty"`
		User       string            `json:"user,omitempty"`
		PrivateKey string            `json:"private_key,omitempty" yaml:"private_key"`
		PublicKey  string            `json:"public_key,omitempty" yaml:"public_key"`
		UserData   string            `json:"user_data,omitempty"`
		Disk       disk              `json:"disk,omitempty"`
		Network    network           `json:"network,omitempty"`
		Device     device            `json:"device,omitempty"`
		ID         string            `json:"id,omitempty"`
		IP         string            `json:"ip,omitempty"`
	}

	// network provides network settings.
	network struct {
		VPC               string   `json:"vpc,omitempty"`
		VPCSecurityGroups []string `json:"vpc_security_group_ids,omitempty" yaml:"vpc_security_groups"`
		SecurityGroups    []string `json:"security_groups,omitempty" yaml:"security_groups"`
		SubnetID          string   `json:"subnet_id,omitempty" yaml:"subnet_id"`
		PrivateIP         bool     `json:"private_ip,omitempty" yaml:"private_ip"`
	}

	// disk provides disk size and type.
	disk struct {
		Size int64  `json:"size,omitempty"`
		Type string `json:"type,omitempty"`
		Iops int64  `json:"iops,omitempty"`
	}

	// device provides the device settings.
	device struct {
		Name string `json:"name,omitempty"`
	}
)

func ProcessPoolFile(rawFile string, defaultPoolSettings *vmpool.DefaultSettings) ([]vmpool.Pool, error) {
	rawPool, err := os.ReadFile(rawFile)
	if err != nil {
		err = fmt.Errorf("unable to read file %s: %w", rawFile, err)
		return nil, err
	}
	defaultPrivateKey, defaultPublicKey, err := defaultPoolSettings.LoadKeys()
	if err != nil {
		err = fmt.Errorf("failed to load keys: %w", err)
		return nil, err
	}

	buf := bytes.NewBuffer(rawPool)
	dec := yaml.NewDecoder(buf)

	var pools []vmpool.Pool
	for {
		poolDef := new(poolDefinition)
		err := dec.Decode(poolDef)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		pool := &googlePool{
			name: poolDef.Name,
		}

		logr := logger.Default.WithField("name", poolDef.Name)
		if defaultPrivateKey != "" {
			logr = logr.WithField("private-key", defaultPoolSettings.PrivateKeyFile)
		}
		if defaultPublicKey != "" {
			logr = logr.WithField("public-key", defaultPoolSettings.PublicKeyFile)
		}
		if poolDef.InitScript != "" {
			logr = logr.WithField("cloud-init", poolDef.InitScript)
		}

		logr.Info("parsed pool file")

		pools = append(pools, pool)
	}

	return pools, nil
}
