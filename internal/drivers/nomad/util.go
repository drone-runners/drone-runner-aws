package nomad

import (
	"github.com/dchest/uniuri"
	"github.com/hashicorp/nomad/api"
)

const (
	gigsToMegs = 1024
)

// stringToPtr returns a pointer to a string
func stringToPtr(s string) *string {
	return &s
}

// intToPtr returns a pointer to a int
func intToPtr(i int) *int {
	return &i
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
