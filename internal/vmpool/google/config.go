package google

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"

	"github.com/drone-runners/drone-runner-aws/oshelp"

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
		ProjectID           string   `json:"project_id,omitempty"  yaml:"project_id"`
		JsonPath            string   `json:"json_path,omitempty"  yaml:"json_path"`
		Scopes              []string `json:"scopes,omitempty"  yaml:"scopes"`
		ServiceAccountEmail string   `json:"service_account_email,omitempty"  yaml:"service_account_email"`
	}

	platform struct {
		OS      string `json:"os,omitempty"`
		Arch    string `json:"arch,omitempty"`
		Variant string `json:"variant,omitempty"`
		Version string `json:"version,omitempty"`
	}

	instance struct {
		Image       string   `json:"image,omitempty" yaml:"image"`
		Name        string   `json:"name,omitempty"`
		Tags        []string `json:"tags,omitempty"`
		Size        string   `json:"size,omitempty"`
		MachineType string   `json:"machine_type,omitempty" yaml:"machine_type"`
		User        string   `json:"user,omitempty"`
		PrivateKey  string   `json:"private_key,omitempty" yaml:"private_key"`
		PublicKey   string   `json:"public_key,omitempty" yaml:"public_key"`
		UserData    string   `json:"user_data,omitempty"`
		UserDataKey string   `json:"user_data_key,omitempty"`
		Disk        disk     `json:"disk,omitempty"`
		Network     string   `json:"network,omitempty"`
		SubnetWork  string   `json:"subnet_work,omitempty"`
		Device      device   `json:"device,omitempty"`
		ID          string   `json:"id,omitempty"`
		IP          string   `json:"ip,omitempty"`
		Zone        []string `json:"zone,omitempty" yaml:"zone"`
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

		poolDef.applyDefaults(defaultPoolSettings)

		err = poolDef.applyInitScript(defaultPoolSettings)
		if err != nil {
			return nil, err
		}

		pool := &googlePool{
			name:       poolDef.Name,
			runnerName: defaultPoolSettings.RunnerName,
			credentials: Credentials{
				ProjectID: poolDef.Account.ProjectID,
				JsonPath:  poolDef.Account.JsonPath,
			},
			os:                  poolDef.Platform.OS,
			rootDir:             tempdir(poolDef.Platform.OS),
			image:               poolDef.Instance.Image,
			size:                poolDef.Instance.MachineType,
			diskType:            poolDef.Instance.Disk.Type,
			zones:               poolDef.Instance.Zone,
			project:             poolDef.Account.ProjectID,
			sizeMin:             poolDef.MinPoolSize,
			sizeMax:             poolDef.MaxPoolSize,
			diskSize:            poolDef.Instance.Disk.Size,
			userData:            poolDef.Instance.UserData,
			userDataKey:         poolDef.Instance.UserDataKey,
			tags:                poolDef.Instance.Tags,
			scopes:              poolDef.Account.Scopes,
			serviceAccountEmail: poolDef.Account.ServiceAccountEmail,
			network:             poolDef.Instance.Network,
			subnetwork:          poolDef.Instance.SubnetWork,
		}

		logr := logger.Default.
			WithField("name", poolDef.Name).
			WithField("os", poolDef.Platform.OS).
			WithField("arch", poolDef.Platform.Arch)

		if poolDef.InitScript != "" {
			logr = logr.WithField("cloud-init", poolDef.InitScript)
		}

		logr.Info("parsed pool file")

		pools = append(pools, pool)
	}

	return pools, nil
}

func (p *poolDefinition) applyInitScript(defaultPoolSettings *vmpool.DefaultSettings) (err error) {
	cloudInitParams := &cloudinit.Params{
		PublicKey:      p.Instance.PublicKey,
		LiteEnginePath: defaultPoolSettings.LiteEnginePath,
		CaCertFile:     defaultPoolSettings.CaCertFile,
		CertFile:       defaultPoolSettings.CertFile,
		KeyFile:        defaultPoolSettings.KeyFile,
		Platform:       p.Platform.OS,
		Architecture:   p.Platform.Arch,
	}

	if p.InitScript == "" {
		if p.Platform.OS == oshelp.OSWindows {
			p.Instance.UserData = cloudinit.Windows(cloudInitParams)
		} else {
			p.Instance.UserData = cloudinit.Linux(cloudInitParams)
		}

		return
	}

	data, err := os.ReadFile(p.InitScript)
	if err != nil {
		err = fmt.Errorf("failed to load cloud init script template: %w", err)
		return
	}

	p.Instance.UserData, err = cloudinit.Custom(string(data), cloudInitParams)

	return
}

func (p *poolDefinition) applyDefaults(defaultPoolSettings *vmpool.DefaultSettings) {
	if p.MinPoolSize < 0 {
		p.MinPoolSize = 0
	}
	if p.MaxPoolSize <= 0 {
		p.MaxPoolSize = 100
	}

	if p.MinPoolSize > p.MaxPoolSize {
		p.MinPoolSize = p.MaxPoolSize
	}

	// apply defaults to Platform
	if p.Platform.OS == "" {
		p.Platform.OS = oshelp.OSLinux
	}
	if p.Platform.Arch == "" {
		p.Platform.Arch = "amd64"
	}
	// apply defaults to instance
	if p.Instance.Disk.Size == 0 {
		p.Instance.Disk.Size = 50
	}
	if p.Instance.Disk.Type == "" {
		p.Instance.Disk.Type = "pd-standard"
	}
	if len(p.Instance.Zone) == 0 {
		p.Instance.Zone = []string{"us-central1-a"}
	}
	if p.Instance.Size == "" {
		p.Instance.Size = "n1-standard-1"
	}
	if p.Instance.Image == "" {
		p.Instance.Image = "ubuntu-os-cloud/global/images/ubuntu-1604-xenial-v20170721"
	}
	if p.Instance.Network == "" {
		p.Instance.Network = "global/networks/default"
	}
	if len(p.Instance.Tags) == 0 {
		p.Instance.Tags = defaultTags
	}
	if len(p.Account.Scopes) == 0 {
		p.Account.Scopes = defaultScopes
	}
	if p.Account.ServiceAccountEmail == "" {
		p.Account.ServiceAccountEmail = "default"
	}
	if p.Instance.UserDataKey == "" {
		p.Instance.UserDataKey = "user-data"
	}
}

// helper function returns the base temporary directory based on the target platform.
func tempdir(inputOS string) string {
	const dir = "google"

	switch inputOS {
	case oshelp.OSWindows:
		return oshelp.JoinPaths(inputOS, "C:\\Windows\\Temp", dir)
	default:
		return oshelp.JoinPaths(inputOS, "/tmp", dir)
	}
}

var (
	defaultTags = []string{
		"allow-docker",
	}

	defaultScopes = []string{
		"https://www.googleapis.com/auth/devstorage.read_only",
		"https://www.googleapis.com/auth/logging.write",
		"https://www.googleapis.com/auth/monitoring.write",
		"https://www.googleapis.com/auth/trace.append",
	}
)
