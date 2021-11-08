package delegate

import (
	"github.com/drone-runners/drone-runner-aws/engine"
	"github.com/drone-runners/drone-runner-aws/internal/platform"
	"github.com/drone-runners/drone-runner-aws/internal/poolfile"
	"github.com/drone/runner-go/pipeline/runtime"
)

func CompileDelegateSetupStage(creds platform.Credentials, pool *poolfile.Pool) (runtime.Spec, error) {
	vol := &engine.Volume{
		EmptyDir: &engine.VolumeEmptyDir{
			ID:   "volumeID",
			Name: "_workspace",
			Labels: map[string]string{
				"io.drone.ttl": "1h0m0s"},
		},
	}

	vols := []*engine.Volume{vol}

	speccy := &engine.Spec{
		CloudInstance: engine.CloudInstance{
			PoolName: pool.Name,
		},
		Volumes: vols,
	}

	return speccy, nil
}
