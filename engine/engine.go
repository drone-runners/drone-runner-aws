// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/drone-runners/drone-runner-aws/internal/ssh"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool"

	"github.com/drone/runner-go/logger"
	"github.com/drone/runner-go/pipeline/runtime"

	"github.com/pkg/sftp"
	cryptoSSH "golang.org/x/crypto/ssh"
)

// Opts configures the Engine.
type Opts struct {
	Repopulate  bool
	PoolManager *vmpool.Manager
}

// Engine implements a pipeline engine.
type Engine struct {
	opts Opts
}

// New returns a new engine.
func New(opts Opts) (*Engine, error) {
	return &Engine{opts}, nil
}

// Setup the pipeline environment.
func (eng *Engine) Setup(ctx context.Context, specv runtime.Spec) error { //nolint:funlen,gocyclo // its complex but standard
	spec := specv.(*Spec)
	if spec.CloudInstance.PoolName == "" {
		return errors.New("setup: pool name is nil")
	}

	pool := eng.opts.PoolManager.Get(spec.CloudInstance.PoolName)
	if pool == nil {
		return fmt.Errorf("setup: pool %q not found", spec.CloudInstance.PoolName)
	}

	// lets see if there is anything in the pool
	instance, poolErr := pool.TryPool(ctx)
	if poolErr != nil {
		logger.FromContext(ctx).
			WithError(poolErr).
			WithField("ami", pool.GetInstanceType()).
			WithField("pool", spec.CloudInstance.PoolName).
			Errorf("setup: failed to use pool")
	}
	if instance != nil {
		// using the pool, use the provided keys
		logger.FromContext(ctx).
			WithField("ami", pool.GetInstanceType()).
			WithField("pool", spec.CloudInstance.PoolName).
			WithField("ip", instance.IP).
			WithField("id", instance.ID).
			Debug("setup: using pool instance")
		spec.CloudInstance.ID = instance.ID
		spec.CloudInstance.IP = instance.IP
	} else {
		logger.FromContext(ctx).
			WithField("ami", pool.GetInstanceType()).
			WithField("pool", spec.CloudInstance.PoolName).
			Debug("setup: pool empty, creating an adhoc instance")

		var provisionErr error
		instance, provisionErr = pool.Provision(ctx, true)
		if provisionErr != nil {
			return provisionErr
		}

		spec.CloudInstance.ID = instance.ID
		spec.CloudInstance.IP = instance.IP
	}
	// we are about to use the instance, this section contains pipeline specific info
	client, sshErr := ssh.Dial(
		spec.CloudInstance.IP,
		pool.GetUser(),
		pool.GetPrivateKey(),
	)
	if sshErr != nil {
		logger.FromContext(ctx).
			WithError(sshErr).
			WithField("ami", pool.GetInstanceType()).
			WithField("pool", spec.CloudInstance.PoolName).
			WithField("ip", spec.CloudInstance.IP).
			WithField("id", spec.CloudInstance.ID).
			WithField("error", sshErr).
			Debug("setup: failed to create client for ssh")
		return sshErr
	}
	defer client.Close()
	// keep checking until docker is ok
	dockerErr := ssh.RetryApplication(ctx, client, "docker ps")
	if dockerErr != nil {
		logger.FromContext(ctx).
			WithError(dockerErr).
			WithField("ami", pool.GetInstanceType()).
			WithField("pool", spec.CloudInstance.PoolName).
			WithField("ip", spec.CloudInstance.IP).
			WithField("id", spec.CloudInstance.ID).
			Debug("setup: docker failed to start in a timely fashion")
		return dockerErr
	}
	clientftp, clientErr := sftp.NewClient(client)
	if clientErr != nil {
		logger.FromContext(ctx).
			WithError(clientErr).
			WithField("ami", pool.GetInstanceType()).
			WithField("pool", spec.CloudInstance.PoolName).
			WithField("ip", spec.CloudInstance.IP).
			WithField("id", spec.CloudInstance.ID).
			Debug("setup: failed to create sftp client")
		return clientErr
	}
	defer clientftp.Close()
	// the pipeline specification may define global folders, such as the pipeline working directory, which must be created before pipeline execution begins.
	for _, file := range spec.Files {
		if !file.IsDir {
			continue
		}
		mkdirErr := mkdir(clientftp, file.Path, file.Mode)
		if mkdirErr != nil {
			logger.FromContext(ctx).
				WithError(mkdirErr).
				WithField("ami", pool.GetInstanceType()).
				WithField("pool", spec.CloudInstance.PoolName).
				WithField("ip", spec.CloudInstance.IP).
				WithField("id", spec.CloudInstance.ID).
				WithField("path", file.Path).
				Error("setup: cannot create directory")
			return mkdirErr
		}
	}

	// the pipeline specification may define global files such as authentication credentials that should be uploaded before pipeline execution begins.
	for _, file := range spec.Files {
		if file.IsDir {
			continue
		}
		uploadErr := upload(clientftp, file.Path, file.Data, file.Mode)
		if uploadErr != nil {
			logger.FromContext(ctx).
				WithError(uploadErr).
				WithField("ami", pool.GetInstanceType()).
				WithField("pool", spec.CloudInstance.PoolName).
				WithField("ip", spec.CloudInstance.IP).
				WithField("id", spec.CloudInstance.ID).
				Error("setup: cannot write file")
			return uploadErr
		}
	}

	// create any folders needed for temporary volumes.
	for _, volume := range spec.Volumes {
		if volume.EmptyDir.ID != "" {
			mkdirErr := mkdir(clientftp, volume.EmptyDir.ID, 0777) //nolint:gomnd // r/w/x for all users
			if mkdirErr != nil {
				logger.FromContext(ctx).
					WithError(mkdirErr).
					WithField("ami", pool.GetInstanceType()).
					WithField("pool", spec.CloudInstance.PoolName).
					WithField("ip", spec.CloudInstance.IP).
					WithField("id", spec.CloudInstance.ID).
					WithField("path", volume.EmptyDir.ID).
					Error("setup: cannot create directory for temporary volume")
				return mkdirErr
			}
		}
	}
	logger.FromContext(ctx).
		WithField("ami", pool.GetInstanceType()).
		WithField("pool", spec.CloudInstance.PoolName).
		WithField("ip", spec.CloudInstance.IP).
		WithField("id", spec.CloudInstance.ID).
		Debug("setup: complete")

	return nil
}

// Destroy the pipeline environment.
func (eng *Engine) Destroy(ctx context.Context, specv runtime.Spec) error {
	spec := specv.(*Spec)
	//nolint: gocritic
	// fmt.Printf("\nssh -i dev.pem %s@%s\n", spec.CloudInstance.User, spec.CloudInstance.IP)
	// _ = os.Remove("dev.pem")
	// f, _ := os.OpenFile("dev.pem", os.O_RDWR|os.O_CREATE, 0400) //nolint: gomnd
	// _, _ = f.WriteString(spec.CloudInstance.PrivateKey)
	// _ = f.Close()
	logger.FromContext(ctx).
		WithField("pool", spec.CloudInstance.PoolName).
		WithField("ip", spec.CloudInstance.IP).
		WithField("id", spec.CloudInstance.ID).
		Debug("destroy: start")

	pool := eng.opts.PoolManager.Get(spec.CloudInstance.PoolName)

	// create creds
	instance := &vmpool.Instance{
		ID: spec.CloudInstance.ID,
		IP: spec.CloudInstance.IP,
	}
	err := pool.Destroy(ctx, instance)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ami", pool.GetInstanceType()).
			WithField("pool", spec.CloudInstance.PoolName).
			WithField("ip", spec.CloudInstance.IP).
			WithField("id", spec.CloudInstance.ID).
			Debug("destroy: failed to destroy the instance")
		return err
	}

	// repopulate the build pool, if needed. This is in destroy, because if in Run, it will slow the build.
	// NB if we are destroying an adhoc instance from a pool (from an empty pool), this code will not be triggered because we overwrote spec.instance.
	// preventing too many instances being created for a pool
	if eng.opts.Repopulate {
		poolCount, countPoolErr := pool.PoolCountFree(ctx)
		if countPoolErr != nil {
			logger.FromContext(ctx).
				WithError(countPoolErr).
				WithField("ami", pool.GetInstanceType()).
				WithField("pool", spec.CloudInstance.PoolName).
				Errorf("destroy: failed to checking pool")
		}
		if poolCount < pool.GetMaxSize() {
			var provisionErr error
			instance, provisionErr = pool.Provision(ctx, false)
			if provisionErr != nil {
				logger.FromContext(ctx).
					WithError(provisionErr).
					WithField("ami", pool.GetInstanceType()).
					WithField("pool", spec.CloudInstance.PoolName).
					Errorf("destroy: failed to add back to the pool")
			} else {
				logger.FromContext(ctx).
					WithField("ami", pool.GetInstanceType()).
					WithField("ip", instance.IP).
					WithField("id", instance.ID).
					WithField("pool", spec.CloudInstance.PoolName).
					Debug("destroy: add back to the pool")
			}
		}
	}
	logger.FromContext(ctx).
		WithField("ami", pool.GetInstanceType()).
		WithField("pool", spec.CloudInstance.PoolName).
		WithField("ip", spec.CloudInstance.IP).
		WithField("id", spec.CloudInstance.ID).
		Debug("destroy: complete")
	return nil
}

// Run runs the pipeline step.
func (eng *Engine) Run(ctx context.Context, specv runtime.Spec, stepv runtime.Step, output io.Writer) (*runtime.State, error) { //nolint:funlen // its complex but standard
	spec := specv.(*Spec)
	step := stepv.(*Step)

	pool := eng.opts.PoolManager.Get(spec.CloudInstance.PoolName)

	client, clientErr := ssh.Dial(
		spec.CloudInstance.IP,
		pool.GetUser(),
		pool.GetPrivateKey(),
	)
	if clientErr != nil {
		logger.FromContext(ctx).
			WithError(clientErr).
			WithField("ami", pool.GetInstanceType()).
			WithField("pool", spec.CloudInstance.PoolName).
			WithField("ip", spec.CloudInstance.IP).
			WithField("id", spec.CloudInstance.ID).
			WithField("error", clientErr).
			Debug("run: failed to create client for ssh")
		return nil, clientErr
	}
	defer client.Close()
	// keep checking until docker is ok
	dockerErr := ssh.RetryApplication(ctx, client, "docker ps")
	if dockerErr != nil {
		logger.FromContext(ctx).
			WithError(dockerErr).
			WithField("ami", pool.GetInstanceType()).
			WithField("pool", spec.CloudInstance.PoolName).
			WithField("ip", spec.CloudInstance.IP).
			WithField("id", spec.CloudInstance.ID).
			Debug("run: docker failed to start in a timely fashion")
		return nil, dockerErr
	}
	clientftp, ftpErr := sftp.NewClient(client)
	if ftpErr != nil {
		logger.FromContext(ctx).
			WithError(ftpErr).
			WithField("ami", pool.GetInstanceType()).
			WithField("pool", spec.CloudInstance.PoolName).
			WithField("ip", spec.CloudInstance.IP).
			WithField("id", spec.CloudInstance.ID).
			Debug("run: failed to create sftp client")
		return nil, ftpErr
	}
	defer clientftp.Close()

	// unlike os/exec there is no good way to set environment
	// the working directory or configure environment variables.
	// we work around this by pre-pending these configurations
	// to the pipeline execution script.
	for _, file := range step.Files {
		w := new(bytes.Buffer)
		writeWorkdir(w, step.WorkingDir)
		writeSecrets(w, pool.GetOS(), step.Secrets)
		writeEnviron(w, pool.GetOS(), step.Envs)
		w.Write(file.Data)
		uploadErr := upload(clientftp, file.Path, w.Bytes(), file.Mode)
		if uploadErr != nil {
			logger.FromContext(ctx).
				WithError(uploadErr).
				WithField("ami", pool.GetInstanceType()).
				WithField("pool", spec.CloudInstance.PoolName).
				WithField("ip", spec.CloudInstance.IP).
				WithField("id", spec.CloudInstance.ID).
				WithField("path", file.Path).
				Error("run: cannot write file")
			return nil, uploadErr
		}
	}

	session, sessionErr := client.NewSession()
	if sessionErr != nil {
		logger.FromContext(ctx).
			WithError(sessionErr).
			WithField("ami", pool.GetInstanceType()).
			WithField("pool", spec.CloudInstance.PoolName).
			WithField("ip", spec.CloudInstance.IP).
			WithField("id", spec.CloudInstance.ID).
			Debug("run: failed to create session")
		return nil, sessionErr
	}
	defer session.Close()

	session.Stdout = output
	session.Stderr = output
	cmd := step.Command + " " + strings.Join(step.Args, " ")

	log := logger.FromContext(ctx)
	log.Debug("run: ssh session started")

	done := make(chan error)
	go func() {
		done <- session.Run(cmd)
	}()

	var stepErr error
	select {
	case stepErr = <-done:
	case <-ctx.Done():
		// BUG(bradrydzewski): openssh does not support the signal
		// command and will not signal remote processes. This may
		// be resolved in openssh 7.9 or higher. Please subscribe
		// to https://github.com/golang/go/issues/16597.
		if sigErr := session.Signal(cryptoSSH.SIGKILL); sigErr != nil {
			log.WithError(sigErr).Debug("run: kill remote process")
		}

		log.Debug("run: ssh session killed")
		return nil, ctx.Err()
	}

	state := &runtime.State{
		ExitCode:  0,
		Exited:    true,
		OOMKilled: false,
	}
	if stepErr != nil {
		state.ExitCode = 255
	}
	if exiterr, ok := stepErr.(*cryptoSSH.ExitError); ok {
		state.ExitCode = exiterr.ExitStatus()
	}

	log.WithField("ssh.exit", state.ExitCode).
		WithField("ami", pool.GetInstanceType()).
		WithField("pool", spec.CloudInstance.PoolName).
		WithField("ip", spec.CloudInstance.IP).
		WithField("id", spec.CloudInstance.ID).
		Debug("run: ssh session finished")
	return state, stepErr
}

func writeWorkdir(w io.Writer, path string) {
	fmt.Fprintf(w, "cd %s", path)
	fmt.Fprintln(w)
}

// helper function writes a shell command to the io.Writer that
// exports all secrets as environment variables.
func writeSecrets(w io.Writer, osString string, secretSlice []*Secret) {
	for _, s := range secretSlice {
		writeEnv(w, osString, s.Env, string(s.Data))
	}
}

// helper function writes a shell command to the io.Writer that
// exports the key value pairs as environment variables.
func writeEnviron(w io.Writer, osString string, envs map[string]string) {
	var keys []string
	for k := range envs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		writeEnv(w, osString, k, envs[k])
	}
}

// helper function writes a shell command to the io.Writer that
// exports and key value pair as an environment variable.
func writeEnv(w io.Writer, osString, key, value string) {
	switch osString {
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
	if _, writeErr := f.Write(data); err != nil {
		return writeErr
	}
	chmodErr := f.Chmod(os.FileMode(mode))
	if chmodErr != nil {
		return chmodErr
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
