package orka

import (
	"github.com/drone-runners/drone-runner-aws/app/oshelp"
)

func tempdir(inputOS string) string {
	const dir = "orka"
	return oshelp.JoinPaths(inputOS, "/tmp", dir)
}
