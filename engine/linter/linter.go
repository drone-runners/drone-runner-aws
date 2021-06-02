// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package linter

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone/drone-go/drone"
	"github.com/drone/runner-go/manifest"
)

// Linter evaluates the pipeline against a set of
// rules and returns an error if one or more of the
// rules are broken.
type Linter struct{}

// New returns a new Linter.
func New() *Linter {
	return new(Linter)
}

// Lint executes the linting rules for the pipeline
// configuration.
func (l *Linter) Lint(pipeline manifest.Resource, repo *drone.Repo) error {
	return checkPipeline(pipeline.(*resource.Pipeline), repo.Trusted)
}

func checkPipeline(pipeline *resource.Pipeline, trusted bool) error {
	if err := checkSteps(pipeline, trusted); err != nil {
		return err
	}
	if pipeline.Instance.AMI == "" {
		return errors.New("Linter: invalid or missing AMI")
	}
	if pipeline.Instance.IAMProfileARN == "" && pipeline.Platform.OS == "windows" {
		return errors.New("Linter: You must provide an IAMProfileARN if using a windows platform")
	}
	if err := checkVolumes(pipeline, trusted); err != nil {
		return err
	}
	return nil
}

func checkSteps(pipeline *resource.Pipeline, trusted bool) error {
	steps := append(pipeline.Services, pipeline.Steps...)

	for _, step := range steps {
		if step == nil {
			return errors.New("Linter: nil step")
		}
		if err := checkStep(step, trusted); err != nil {
			return err
		}
	}
	return nil
}

func checkStep(step *resource.Step, trusted bool) error {
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

func checkVolumes(pipeline *resource.Pipeline, trusted bool) error {
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
