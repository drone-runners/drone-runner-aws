package google

import (
	"os"

	"github.com/drone-runners/drone-runner-aws/internal/vmpool"
	"github.com/drone-runners/drone-runner-aws/oshelp"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
)

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

func New(opts ...Option) (vmpool.Pool, error) {
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
		p.os = oshelp.OSLinux
	}
	if p.arch == "" {
		p.arch = "amd64"
	}
	// apply defaults to instance
	if p.diskSize == 0 {
		p.diskSize = 50
	}
	if p.diskType == "" {
		p.diskType = "pd-standard"
	}
	if len(p.zones) == 0 {
		p.zones = []string{"us-central1-a"}
	}
	if p.size == "" {
		p.size = "n1-standard-1"
	}
	if p.image == "" {
		p.image = "ubuntu-os-cloud/global/images/ubuntu-1604-xenial-v20170721"
	}
	if p.network == "" {
		p.network = "global/networks/default"
	}
	if len(p.tags) == 0 {
		p.tags = defaultTags
	}
	if len(p.scopes) == 0 {
		p.scopes = defaultScopes
	}
	if p.serviceAccountEmail == "" {
		p.serviceAccountEmail = "default"
	}
	if p.userDataKey == "" && p.os == oshelp.OSLinux {
		p.userDataKey = "user-data"
	} else {
		p.userDataKey = "windows-startup-script-ps1"
	}
	if p.service == nil {
		if p.JSONPath != "" {
			p.JSON, _ = os.ReadFile(p.JSONPath)
		}
		client, err := google.DefaultClient(oauth2.NoContext, compute.ComputeScope)
		if err != nil {
			return nil, err
		}

		p.service, err = compute.New(client)
		if err != nil {
			return nil, err
		}
	}
	return p, nil
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
