package anka

import "github.com/drone-runners/drone-runner-vm/internal/oshelp"

func tempdir(inputOS string) string {
	const dir = "anka"

	switch inputOS {
	case oshelp.OSWindows:
		return oshelp.JoinPaths(inputOS, "C:\\Windows\\Temp", dir)
	case oshelp.OSMac:
		return oshelp.JoinPaths(inputOS, "/tmp", dir)
	default:
		return oshelp.JoinPaths(inputOS, "/tmp", dir)
	}
}
