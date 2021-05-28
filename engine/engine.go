// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/drone-runners/drone-runner-aws/internal/platform"
	"github.com/drone-runners/drone-runner-aws/internal/ssh"
	"github.com/drone/runner-go/logger"
	"github.com/drone/runner-go/pipeline/runtime"

	"github.com/pkg/sftp"
	cryptoSSH "golang.org/x/crypto/ssh"
)

// Opts configures the Engine.
type Opts struct {
}

// Engine implements a pipeline engine.
type Engine struct {
}

// New returns a new engine.
func New(opts Opts) (*Engine, error) {
	return &Engine{}, nil
}

// Setup the pipeline environment.
func (e *Engine) Setup(ctx context.Context, specv runtime.Spec) error {
	spec := specv.(*Spec)

	logger.FromContext(ctx).
		WithField("ami", spec.Instance.AMI).
		Debug("creating instance")

	// create creds
	creds := platform.Credentials{
		Client: spec.Account.AccessKeyID,
		Secret: spec.Account.AccessKeySecret,
		Region: spec.Account.Region,
	}
	// provisioning information
	provArgs := platform.ProvisionArgs{
		Key:           spec.Instance.KeyPair,
		Image:         spec.Instance.AMI,
		IamProfileArn: spec.Instance.IAMProfileARN,
		Name:          spec.Instance.User,
		Size:          spec.Instance.Type,
		Region:        spec.Account.Region,
		Userdata:      spec.Instance.UserData,
		// Tags: TODO
		// network
		Subnet:    spec.Instance.Network.SubnetID,
		Groups:    spec.Instance.Network.SecurityGroups,
		Device:    spec.Instance.Device.Name,
		PrivateIP: spec.Instance.Network.PrivateIP,
		// disk
		VolumeType: spec.Instance.Disk.Type,
		VolumeSize: spec.Instance.Disk.Size,
		VolumeIops: spec.Instance.Disk.Iops,
	}
	// create the instance
	instance, createErr := platform.Create(ctx, creds, provArgs)
	if createErr != nil {
		logger.FromContext(ctx).
			WithError(createErr).
			WithField("ami", spec.Instance.AMI).
			Debug("failed to create the instance")
		return createErr
	}
	logger.FromContext(ctx).
		WithField("ID", instance.ID).
		WithField("IP", instance.IP).
		Info("created the instance")
	spec.Instance.id = instance.ID
	spec.Instance.ip = instance.IP

	// establish an ssh connection with the server instance
	// to setup the build environment (upload build scripts, etc)

	client, err := ssh.DialRetry(
		ctx,
		spec.Instance.ip,
		spec.Instance.User,
		spec.Instance.PrivateKey,
	)
	if err != nil {
		logger.FromContext(ctx).
			WithError(createErr).
			WithField("ami", spec.Instance.AMI).
			WithField("error", err).
			Debug("failed to create client for ssh")
		return err
	}
	defer client.Close()

	clientftp, err := sftp.NewClient(client)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ip", instance.IP).
			WithField("id", instance.ID).
			Debug("failed to create sftp client")
		return err
	}
	if clientftp != nil {
		defer clientftp.Close()
	}

	// the pipeline workspace is created before pipeline
	// execution begins. All files and folders created during
	// pipeline execution are isolated to this workspace.
	err = mkdir(clientftp, spec.Root, 0777)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("path", spec.Root).
			Error("cannot create workspace directory")
		return err
	}

	// the pipeline specification may define global folders, such
	// as the pipeline working directory, wich must be created
	// before pipeline execution begins.
	for _, file := range spec.Files {
		if !file.IsDir {
			continue
		}
		err = mkdir(clientftp, file.Path, file.Mode)
		if err != nil {
			logger.FromContext(ctx).
				WithError(err).
				WithField("path", file.Path).
				Error("cannot create directory")
			return err
		}
	}

	// the pipeline specification may define global files such
	// as authentication credentials that should be uploaded
	// before pipeline execution begins.
	for _, file := range spec.Files {
		if file.IsDir {
			continue
		}
		err = upload(clientftp, file.Path, file.Data, file.Mode)
		if err != nil {
			logger.FromContext(ctx).
				WithError(err).
				Error("cannot write file")
			return err
		}
	}

	logger.FromContext(ctx).
		WithField("ip", instance.IP).
		WithField("id", instance.ID).
		Debug("server configuration complete")

	return nil
}

// Destroy the pipeline environment.
func (e *Engine) Destroy(ctx context.Context, specv runtime.Spec) error {
	spec := specv.(*Spec)

	logger.FromContext(ctx).
		WithField("ami", spec.Instance.AMI).
		Debug("destroying instance")

	// create creds
	creds := platform.Credentials{
		Client: spec.Account.AccessKeyID,
		Secret: spec.Account.AccessKeySecret,
		Region: spec.Account.Region,
	}
	instance := platform.Instance{
		ID: spec.Instance.id,
		IP: spec.Instance.ip,
	}
	err := platform.Destroy(ctx, creds, &instance)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ami", spec.Instance.AMI).
			Debug("failed to destroy the instance")
		return err
	}
	return nil
}

// Run runs the pipeline step.
func (e *Engine) Run(ctx context.Context, specv runtime.Spec, stepv runtime.Step, output io.Writer) (*runtime.State, error) {
	spec := specv.(*Spec)
	step := stepv.(*Step)

	client, err := ssh.Dial(
		spec.Instance.ip,
		spec.Instance.User,
		spec.Instance.PrivateKey,
	)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ami", spec.Instance.AMI).
			WithField("error", err).
			Debug("failed to create client for ssh")
		return nil, err
	}
	defer client.Close()

	clientftp, err := sftp.NewClient(client)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ip", spec.Instance.ip).
			WithField("id", spec.Instance.id).
			Debug("failed to create sftp client")
		return nil, err
	}
	defer clientftp.Close()

	// unlike os/exec there is no good way to set environment
	// the working directory or configure environment variables.
	// we work around this by pre-pending these configurations
	// to the pipeline execution script.
	for _, file := range step.Files {
		w := new(bytes.Buffer)
		writeWorkdir(w, step.WorkingDir)
		writeSecrets(w, spec.Platform.OS, step.Secrets)
		writeEnviron(w, spec.Platform.OS, step.Envs)
		w.Write(file.Data)
		err = upload(clientftp, file.Path, w.Bytes(), file.Mode)
		if err != nil {
			logger.FromContext(ctx).
				WithError(err).
				WithField("path", file.Path).
				Error("cannot write file")
			return nil, err
		}
	}

	session, err := client.NewSession()
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ip", spec.Instance.ip).
			WithField("id", spec.Instance.id).
			Debug("failed to create session")
		return nil, err
	}
	defer session.Close()

	session.Stdout = output
	session.Stderr = output
	cmd := step.Command + " " + strings.Join(step.Args, " ")

	log := logger.FromContext(ctx)
	log.Debug("ssh session started")

	done := make(chan error)
	go func() {
		done <- session.Run(cmd)
	}()

	select {
	case err = <-done:
	case <-ctx.Done():
		// BUG(bradrydzewski): openssh does not support the signal
		// command and will not signal remote processes. This may
		// be resolved in openssh 7.9 or higher. Please subscribe
		// to https://github.com/golang/go/issues/16597.
		if err := session.Signal(cryptoSSH.SIGKILL); err != nil {
			log.WithError(err).Debug("kill remote process")
		}

		log.Debug("ssh session killed")
		return nil, ctx.Err()
	}

	state := &runtime.State{
		ExitCode:  0,
		Exited:    true,
		OOMKilled: false,
	}
	if err != nil {
		state.ExitCode = 255
	}
	if exiterr, ok := err.(*cryptoSSH.ExitError); ok {
		state.ExitCode = exiterr.ExitStatus()
	}

	log.WithField("ssh.exit", state.ExitCode).
		Debug("ssh session finished")
	return state, err
}

func (e *Engine) Ping(ctx context.Context, accessKeyID, accessKeySecret, region string) error {
	// create creds
	creds := platform.Credentials{
		Client: accessKeyID,
		Secret: accessKeySecret,
		Region: region,
	}
	err := platform.Ping(ctx, creds)
	return err
}

func writeWorkdir(w io.Writer, path string) {
	fmt.Fprintf(w, "cd %s", path)
	fmt.Fprintln(w)
}

// helper function writes a shell command to the io.Writer that
// exports all secrets as environment variables.
func writeSecrets(w io.Writer, os string, secrets []*Secret) {
	for _, s := range secrets {
		writeEnv(w, os, s.Env, string(s.Data))
	}
}

// helper function writes a shell command to the io.Writer that
// exports the key value pairs as environment variables.
func writeEnviron(w io.Writer, os string, envs map[string]string) {
	var keys []string
	for k := range envs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		writeEnv(w, os, k, envs[k])
	}
}

// helper function writes a shell command to the io.Writer that
// exports and key value pair as an environment variable.
func writeEnv(w io.Writer, os, key, value string) {
	switch os {
	case "windows":
		fmt.Fprintf(w, "$Env:%s = %q", key, value)
		fmt.Fprintln(w)
	default:
		fmt.Fprintf(w, "export %s=%q", key, value)
		fmt.Fprintln(w)
	}
}

func upload(client *sftp.Client, path string, data []byte, mode uint32) error {
	f, err := client.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	err = f.Chmod(os.FileMode(mode))
	if err != nil {
		return err
	}
	return nil
}

func mkdir(client *sftp.Client, path string, mode uint32) error {
	err := client.MkdirAll(path)
	if err != nil {
		return err
	}
	return client.Chmod(path, os.FileMode(mode))
}
