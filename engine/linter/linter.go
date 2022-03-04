// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package linter

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/drone-runners/drone-runner-aws/internal/vmpool"

	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone/drone-go/drone"
	"github.com/drone/runner-go/manifest"
)

// ErrDuplicateStepName is returned when two Pipeline steps
// have the same name.
var ErrDuplicateStepName = errors.New("linter: duplicate step names")

// Linter evaluates the pipeline against a set of
// rules and returns an error if one or more of the
// rules are broken.
type Linter struct {
	PoolManager *vmpool.Manager
}

// New returns a new Linter.
func New() *Linter {
	return new(Linter)
}

// Lint executes the linting rules for the pipeline configuration.
func (l *Linter) Lint(pipeline manifest.Resource, repo *drone.Repo) error {
	if err := checkPipeline(pipeline.(*resource.Pipeline)); err != nil {
		return err
	}
	if err := checkPools(pipeline.(*resource.Pipeline), l.PoolManager); err != nil {
		return err
	}
	return nil
}

func checkPipeline(pipeline *resource.Pipeline) error {
	if err := checkSteps(pipeline); err != nil {
		return err
	}
	err := checkVolumes(pipeline)
	return err
}

func checkPools(pipeline *resource.Pipeline, poolManager *vmpool.Manager) error {
	if poolManager.Count() == 0 {
		return fmt.Errorf("linter: there are no pools defined")
	}
	if pipeline.Pool.Use == "" {
		return fmt.Errorf("linter: you must specify a 'pool' to 'use'")
	}
	// if we dont match lets exit
	if !poolManager.Exists(pipeline.Pool.Use) {
		errMsg := fmt.Sprintf("linter: unable to find definition of pool %q.", pipeline.Pool.Use)
		return errors.New(errMsg)
	}
	return nil
}

func checkSteps(pipeline *resource.Pipeline) error {
	steps := append(pipeline.Services, pipeline.Steps...) //nolint:gocritic // creating a new slice is ok
	names := map[string]struct{}{}
	if !pipeline.Clone.Disable {
		names["clone"] = struct{}{}
	}

	for _, step := range steps {
		if step == nil {
			return errors.New("linter: nil step")
		}

		// unique list of names
		_, ok := names[step.Name]
		if ok {
			return ErrDuplicateStepName
		}
		names[step.Name] = struct{}{}

		if err := checkStep(step); err != nil {
			return err
		}
		if err := checkDeps(step, names); err != nil {
			return err
		}
	}
	return nil
}

func checkStep(step *resource.Step) error {
	for _, mount := range step.Volumes {
		switch mount.Name {
		case "workspace", "_workspace", "_docker_socket":
			return fmt.Errorf("linter: invalid volume name: %s", mount.Name)
		}
		if strings.HasPrefix(filepath.Clean(mount.MountPath), "/run/drone") {
			return fmt.Errorf("linter: cannot mount volume at /run/drone")
		}
	}

	return nil
}

func checkVolumes(pipeline *resource.Pipeline) error {
	for _, volume := range pipeline.Volumes {
		switch volume.Name {
		case "":
			return fmt.Errorf("linter: missing volume name")
		case "workspace", "_workspace", "_docker_socket":
			return fmt.Errorf("linter: invalid volume name: %s", volume.Name)
		}
	}
	return nil
}

func checkDeps(step *resource.Step, deps map[string]struct{}) error {
	for _, dep := range step.DependsOn {
		_, ok := deps[dep]
		if !ok {
			return fmt.Errorf("linter: unknown step dependency detected: %s references %s", step.Name, dep)
		}
		if step.Name == dep {
			return fmt.Errorf("linter: cyclical step dependency detected: %s", dep)
		}
	}
	return nil
}
