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
		Root     string   `json:"root,omitempty"`
		Platform Platform `json:"platform,omitempty"`
		Account  Account  `json:"account,omitempty"`
		Instance Instance `json:"instance,omitempty"`
		Files    []*File  `json:"files,omitempty"`
		Steps    []*Step  `json:"steps,omitempty"`
	}

	// Account provides account settings
	Account struct {
		AccessKeyID     string `json:"access_key_id,omitempty"`
		AccessKeySecret string `json:"secret_access_key,omitempty"`
		Region          string `json:"region,omitempty"`
	}

	// Instance provides instance settings.
	Instance struct {
		AMI           string  `json:"ami,omitempty"`
		IAMProfileARN string  `json:"iam_profile_arn,omitempty"`
		Type          string  `json:"type,omitempty"`
		User          string  `json:"user,omitempty"`
		PrivateKey    string  `json:"private_key,omitempty"`
		PublicKey     string  `json:"public_key,omitempty"`
		UserData      string  `json:"user_data,omitempty"`
		Disk          Disk    `json:"disk,omitempty"`
		Network       Network `json:"network,omitempty"`
		// this is a keypair defined in AWS, it can make it easier to debug (optional)
		KeyPair string `json:"key_pair,omitempty"`
		Market  string `json:"market_type,omitempty"`
		Device  Device `json:"device,omitempty"`
		id      string
		ip      string
		// availability_zone
		// placement_group
		// tenancy
	}

	// Network provides network settings.
	Network struct {
		VPC               string   `json:"vpc,omitempty"`
		VPCSecurityGroups []string `json:"vpc_security_group_ids,omitempty"`
		SecurityGroups    []string `json:"security_groups,omitempty"`
		SubnetID          string   `json:"subnet_id,omitempty"`
		PrivateIP         bool     `json:"private_ip,omitempty"`

		// public_dns
		// private_dns
		// network_interface
	}

	// Disk provides disk size and type.
	Disk struct {
		Size int64  `json:"size,omitempty"`
		Type string `json:"type,omitempty"`
		Iops int64  `json:"iops,omitempty"`
	}

	// Device provides the device settings.
	Device struct {
		Name string `json:"name,omitempty"`
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
		Name       string            `json:"name,omitempty"`
		RunPolicy  runtime.RunPolicy `json:"run_policy,omitempty"`
		Secrets    []*Secret         `json:"secrets,omitempty"`
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
func (s *Step) Clone() runtime.Step {
	dst := new(Step)
	*dst = *s
	dst.Envs = environ.Combine(s.Envs)
	return dst
}
