// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package engine

import (
	"github.com/drone/runner-go/environ"
	"github.com/drone/runner-go/pipeline/runtime"
)

type (
	// Spec provides the pipeline spec. This provides the
	// required instructions for reproducible pipeline
	// execution.
	Spec struct {
		Pool    Pool      `json:"pool,omitempty"`
		Root    string    `json:"root,omitempty"`
		Files   []*File   `json:"files,omitempty"`
		Steps   []*Step   `json:"steps,omitempty"`
		Volumes []*Volume `json:"volumes,omitempty"`
	}

	// Pool provides the pipeline pool. This provides the
	Pool struct {
		Name string `json:"name,omitempty"`
		ID   string `json:"id,omitempty"`
		IP   string `json:"ip,omitempty"`
	}
	// Step defines a pipeline step.
	Step struct {
		Args       []string          `json:"args,omitempty"`
		Command    string            `json:"command,omitempty"`
		Detach     bool              `json:"detach,omitempty"`
		DependsOn  []string          `json:"depends_on,omitempty"`
		ErrPolicy  runtime.ErrPolicy `json:"err_policy,omitempty"`
		Envs       map[string]string `json:"environment,omitempty"`
		Files      []*File           `json:"files,omitempty"`
		Image      string            `json:"image,omitempty"`
		Name       string            `json:"name,omitempty"`
		RunPolicy  runtime.RunPolicy `json:"run_policy,omitempty"`
		Secrets    []*Secret         `json:"secrets,omitempty"`
		Volumes    []*VolumeMount    `json:"volumes,omitempty"`
		WorkingDir string            `json:"working_dir,omitempty"`
	}

	// Secret represents a secret variable.
	Secret struct {
		Name string `json:"name,omitempty"`
		Env  string `json:"env,omitempty"`
		Data []byte `json:"data,omitempty"`
		Mask bool   `json:"mask,omitempty"`
	}

	// Platform defines the target platform.
	Platform struct {
		OS      string `json:"os,omitempty"`
		Arch    string `json:"arch,omitempty"`
		Variant string `json:"variant,omitempty"`
		Version string `json:"version,omitempty"`
	}

	// Volume that can be mounted by containers.
	Volume struct {
		EmptyDir *VolumeEmptyDir `json:"temp,omitempty"`
		HostPath *VolumeHostPath `json:"host,omitempty"`
	}

	// VolumeMount describes a mounting of a Volume within a container.
	VolumeMount struct {
		Name string `json:"name,omitempty"`
		Path string `json:"path,omitempty"`
	}

	// VolumeEmptyDir mounts a temporary directory from the
	// host node's filesystem into the container. This can
	// be used as a shared scratch space.
	VolumeEmptyDir struct {
		ID        string            `json:"id,omitempty"`
		Name      string            `json:"name,omitempty"`
		Medium    string            `json:"medium,omitempty"`
		SizeLimit int64             `json:"size_limit,omitempty"`
		Labels    map[string]string `json:"labels,omitempty"`
	}

	// VolumeHostPath mounts a file or directory from the host node's filesystem into your container.
	VolumeHostPath struct {
		ID       string            `json:"id,omitempty"`
		Name     string            `json:"name,omitempty"`
		Path     string            `json:"path,omitempty"`
		Labels   map[string]string `json:"labels,omitempty"`
		ReadOnly bool              `json:"read_only,omitempty"`
	}

	// File defines a file that should be uploaded or
	// mounted somewhere in the step container or virtual
	// machine prior to command execution.
	File struct {
		Path  string `json:"path,omitempty"`
		Mode  uint32 `json:"mode,omitempty"`
		Data  []byte `json:"data,omitempty"`
		IsDir bool   `json:"is_dir,omitempty"`
	}
)

//
// implements the Spec interface
//

func (s *Spec) StepLen() int              { return len(s.Steps) }
func (s *Spec) StepAt(i int) runtime.Step { return s.Steps[i] }

//
// implements the Secret interface
//

func (s *Secret) GetName() string  { return s.Name }
func (s *Secret) GetValue() string { return string(s.Data) }
func (s *Secret) IsMasked() bool   { return s.Mask }

//
// implements the Step interface
//

func (s *Step) GetName() string                  { return s.Name }
func (s *Step) GetDependencies() []string        { return s.DependsOn }
func (s *Step) GetEnviron() map[string]string    { return s.Envs }
func (s *Step) SetEnviron(env map[string]string) { s.Envs = env }
func (s *Step) GetErrPolicy() runtime.ErrPolicy  { return s.ErrPolicy }
func (s *Step) GetRunPolicy() runtime.RunPolicy  { return s.RunPolicy }
func (s *Step) GetSecretAt(i int) runtime.Secret { return s.Secrets[i] }
func (s *Step) GetSecretLen() int                { return len(s.Secrets) }
func (s *Step) IsDetached() bool                 { return s.Detach }
func (s *Step) GetImage() string                 { return s.Image }
func (s *Step) Clone() runtime.Step {
	dst := new(Step)
	*dst = *s
	dst.Envs = environ.Combine(s.Envs)
	return dst
}
