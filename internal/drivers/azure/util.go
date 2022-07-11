package azure

import "github.com/drone-runners/drone-runner-aws/internal/oshelp"

// helper function returns the base temporary directory based on the target platform.
func tempdir(inputOS string) string {
	const dir = "azure"

	switch inputOS {
	case oshelp.OSWindows:
		return oshelp.JoinPaths(inputOS, "C:\\Windows\\Temp", dir)
	default:
		return oshelp.JoinPaths(inputOS, "/tmp", dir)
	}
}
