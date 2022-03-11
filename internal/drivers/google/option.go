package google

import (
	"net/http"
	"os"

	"github.com/sirupsen/logrus"
	"google.golang.org/api/compute/v1"
)

type Option func(*provider)

// WithClient returns an option to set the default http
// Client used with the Google Compute provider.
func WithClient(client *http.Client) Option {
	return func(p *provider) {
		service, err := compute.New(client) //nolint:staticcheck
		if err != nil {
			panic(err)
		}
		p.service = service
	}
}

// WithRunnerName returns an option to set the runner name
func WithRunnerName(name string) Option {
	return func(p *provider) {
		p.runnerName = name
	}
}

// WithLimit the total number of running servers. If exceeded block or error.
func WithLimit(limit int) Option {
	return func(p *provider) {
		p.limit = limit
	}
}

// WithPool total number of warm instances in the pool at all times
func WithPool(pool int) Option {
	return func(p *provider) {
		p.pool = pool
	}
}

func WithOs(machineOs string) Option {
	return func(p *provider) {
		p.os = machineOs
	}
}

func WithArch(arch string) Option {
	return func(p *provider) {
		p.arch = arch
	}
}

// WithDiskSize returns an option to set the instance disk
// size in gigabytes.
func WithDiskSize(diskSize int64) Option {
	return func(p *provider) {
		p.diskSize = diskSize
	}
}

// WithName returns an option to set the instance name.
func WithName(name string) Option {
	return func(p *provider) {
		p.name = name
	}
}

// WithDiskType returns an option to set the instance disk type.
func WithDiskType(diskType string) Option {
	return func(p *provider) {
		p.diskType = diskType
	}
}

// WithLabels returns an option to set the metadata labels.
func WithLabels(labels map[string]string) Option {
	return func(p *provider) {
		p.labels = labels
	}
}

// WithMachineImage returns an option to set the image.
func WithMachineImage(image string) Option {
	return func(p *provider) {
		p.image = image
	}
}

// WithMachineType returns an option to set the instance type.
func WithMachineType(size string) Option {
	return func(p *provider) {
		p.size = size
	}
}

// WithNetwork returns an option to set the network.
func WithNetwork(network string) Option {
	return func(p *provider) {
		p.network = network
	}
}

// WithSubnetwork returns an option to set the subnetwork.
func WithSubnetwork(subnetwork string) Option {
	return func(p *provider) {
		p.subnetwork = subnetwork
	}
}

// WithPrivateIP returns an option to set the private IP address.
func WithPrivateIP(private bool) Option {
	return func(p *provider) {
		p.privateIP = private
	}
}

// WithProject returns an option to set the project.
func WithProject(project string) Option {
	return func(p *provider) {
		p.projectID = project
	}
}

// WithJSONPath returns an option to set the json path
func WithJSONPath(path string) Option {
	return func(p *provider) {
		p.JSONPath = path
	}
}

// WithTags returns an option to set the resource tags.
func WithTags(tags ...string) Option {
	return func(p *provider) {
		p.tags = tags
	}
}

// WithUserData returns an option to set the cloud-init
// template from text.
func WithUserData(text string) Option {
	return func(p *provider) {
		if text != "" {
			data, err := os.ReadFile(text)
			if err != nil {
				logrus.Error(err)
				return
			}
			p.userData = string(data)
		}
		return
	}
}

// WithUserDataKey allows to set the user data key for Google Cloud Platform
// This allows user to set either user-data or a startup script
func WithUserDataKey(text string) Option {
	return func(p *provider) {
		if text != "" {
			p.userDataKey = text
		}
	}
}

// WithZones WithZone returns an option to set the target zone.
func WithZones(zones ...string) Option {
	return func(p *provider) {
		p.zones = zones
	}
}

// WithScopes returns an option to set the scopes.
func WithScopes(scopes ...string) Option {
	return func(p *provider) {
		p.scopes = scopes
	}
}

// WithServiceAccountEmail returns an option to set the ServiceAccountEmail.
func WithServiceAccountEmail(email string) Option {
	return func(p *provider) {
		p.serviceAccountEmail = email
	}
}
