package nomad

import (
	"strings"
	"time"

	"github.com/dchest/uniuri"
	"github.com/hashicorp/nomad/api"
)

const (
	gigsToMegs        = 1024
	ignitePath        = "/usr/local/bin/ignite"
	tenSecondsTimeout = 10 * time.Second
)

// stringToPtr returns a pointer to a string
func stringToPtr(s string) *string {
	return &s
}

// intToPtr returns a pointer to a int
func intToPtr(i int) *int {
	return &i
}

// minNomadResources returns the minimum resources required for a Nomad job
func minNomadResources(cpuMhz, memoryMb int) *api.Resources {
	return &api.Resources{
		CPU:      intToPtr(cpuMhz),
		MemoryMB: intToPtr(memoryMb),
	}
}

// boolToPtr returns a pointer to a bool
func boolToPtr(b bool) *bool {
	return &b
}

// random generates a random string of length n
func random(n int) string {
	return uniuri.NewLen(n)
}

// convert gigs to megs
func convertGigsToMegs(p int) int {
	return p * gigsToMegs
}

// check if job is completed
func isTerminal(job *api.Job) bool {
	return Status(*job.Status) == Dead
}

// check if image is fully qualified
func isFullyQualifiedImage(imageName string) bool {
	if imageName == "" {
		return false
	}
	// Split only the first slash to isolate potential registry part
	parts := strings.SplitN(imageName, "/", 2)
	if len(parts) < 2 {
		return false // no slash means it's not a registry-based image
	}

	registryPart := parts[0]

	// Heuristic: if the registry part contains a dot or a colon, it's likely a registry (e.g., docker.io, localhost:5000)
	return strings.Contains(registryPart, ".") || strings.Contains(registryPart, ":")
}
