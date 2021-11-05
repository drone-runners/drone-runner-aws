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

	switch {
	case pool.Instance.User == "" && pool.Platform.OS == "windows":
		pool.Instance.User = "Administrator"
	case pool.Instance.User == "":
		pool.Instance.User = "root"
	}
	speccy := &engine.Spec{
		Pool: engine.Pool{
			Name: pool.Name,
		},
		Volumes: vols,
	}

	return speccy, nil
}
