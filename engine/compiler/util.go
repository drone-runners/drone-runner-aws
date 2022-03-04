// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package compiler

import (
	"strings"

	"github.com/drone-runners/drone-runner-aws/engine"
	"github.com/drone-runners/drone-runner-aws/engine/resource"

	"github.com/drone/drone-go/drone"
	"github.com/drone/runner-go/manifest"
	lespec "github.com/harness/lite-engine/engine/spec"
)

const cloneStep = "clone"

// helper function returns true if the step is configured to
// always run regardless of status.
func isRunAlways(step *resource.Step) bool {
	if len(step.When.Status.Include) == 0 &&
		len(step.When.Status.Exclude) == 0 {
		return false
	}
	return step.When.Status.Match(drone.StatusFailing) &&
		step.When.Status.Match(drone.StatusPassing)
}

// helper function returns true if the step is configured to
// only run on failure.
func isRunOnFailure(step *resource.Step) bool {
	if len(step.When.Status.Include) == 0 &&
		len(step.When.Status.Exclude) == 0 {
		return false
	}
	return step.When.Status.Match(drone.StatusFailing)
}

// helper function returns true if the pipeline specification
// manually defines an execution graph.
func isGraph(spec *engine.Spec) bool {
	for _, step := range spec.Steps {
		if len(step.DependsOn) > 0 {
			return true
		}
	}
	return false
}

// helper function creates the dependency graph for serial
// pipeline execution.
func configureSerial(spec *engine.Spec) {
	var prev *engine.Step
	for _, step := range spec.Steps {
		if prev != nil {
			step.DependsOn = []string{prev.Name}
		}
		prev = step
	}
}

// helper function converts the environment variables to a map,
// returning only inline environment variables not derived from
// a secret.
func convertStaticEnv(src map[string]*manifest.Variable) map[string]string {
	dst := map[string]string{}
	for k, v := range src {
		if v == nil {
			continue
		}
		if strings.TrimSpace(v.Secret) == "" {
			dst[k] = v.Value
		}
	}
	return dst
}

// helper function converts the environment variables to a map,
// returning only inline environment variables not derived from
// a secret.
func convertSecretEnv(src map[string]*manifest.Variable) []*lespec.Secret {
	dst := []*lespec.Secret{}
	for k, v := range src {
		if v == nil {
			continue
		}
		if strings.TrimSpace(v.Secret) != "" {
			dst = append(dst, &lespec.Secret{
				Name: v.Secret,
				Mask: true,
				Env:  k,
			})
		}
	}
	return dst
}

// helper function modifies the pipeline dependency graph to
// account for the clone step.
func configureCloneDeps(spec *engine.Spec) {
	for _, step := range spec.Steps {
		if step.Name == cloneStep {
			continue
		}
		if len(step.DependsOn) == 0 {
			step.DependsOn = []string{cloneStep}
		}
	}
}

// helper function modifies the pipeline dependency graph to
// account for a disabled clone step.
func removeCloneDeps(spec *engine.Spec) {
	for _, step := range spec.Steps {
		if step.Name == cloneStep {
			return
		}
	}
	for _, step := range spec.Steps {
		if len(step.DependsOn) == 1 &&
			step.DependsOn[0] == cloneStep {
			step.DependsOn = []string{}
		}
	}
}

// helper function modifies the pipeline dependency graph to
// account for the clone step.
func convertPullPolicy(s string) lespec.PullPolicy {
	switch strings.ToLower(s) {
	case "always":
		return lespec.PullAlways
	case "if-not-exists":
		return lespec.PullIfNotExists
	case "never":
		return lespec.PullNever
	default:
		return lespec.PullDefault
	}
}
