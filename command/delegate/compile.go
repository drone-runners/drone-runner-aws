package delegate

import (
	"github.com/drone-runners/drone-runner-aws/engine"
	"github.com/drone-runners/drone-runner-aws/internal/platform"
	"github.com/drone/runner-go/pipeline/runtime"
)

func CompileDelegateSetupStage(creds platform.Credentials, pool *engine.Pool) (runtime.Spec, error) {
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
		Pool: engine.Pool{
			Name: pool.Name,
			Account: engine.Account{
				AccessKeyID:     creds.Client,
				AccessKeySecret: creds.Secret,
				Region:          creds.Region,
			},
			Instance: engine.Instance{
				PrivateKey: pool.Instance.PrivateKey,
				User:       "root",
			},
		},
		Volumes: vols,
	}

	return speccy, nil
}
