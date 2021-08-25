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
	"sync"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/platform"
	"github.com/drone-runners/drone-runner-aws/internal/ssh"

	"github.com/drone/runner-go/logger"
	"github.com/drone/runner-go/pipeline/runtime"

	"github.com/pkg/sftp"
	cryptoSSH "golang.org/x/crypto/ssh"
)

type Pool struct {
	InstanceSpec *Spec
	PoolSize     int
}

// Opts configures the Engine.
type Opts struct {
	AwsMutex   *sync.Mutex
	RunnerName string
	Pools      map[string]Pool
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
func (e *Engine) Setup(ctx context.Context, specv runtime.Spec) error { //nolint:funlen,gocyclo // its complex but standard
	spec := specv.(*Spec)
	// create creds
	creds := platform.Credentials{
		Client: spec.Account.AccessKeyID,
		Secret: spec.Account.AccessKeySecret,
		Region: spec.Account.Region,
	}
	createInstance := true
	justSetup := false // this is set to true, if creating an instance for a pool, only do basic setup
	if spec.PoolCount != 0 {
		justSetup = true
	}
	if spec.Instance.Use != "" {
		found, id, ip, poolErr := platform.TryPool(ctx, creds, spec.Instance.Use, e.opts.AwsMutex)
		if poolErr != nil {
			logger.FromContext(ctx).
				WithError(poolErr).
				WithField("ami", spec.Instance.AMI).
				WithField("pool", spec.Instance.Use).
				Errorf("failed to use pool")
		}
		if found {
			// using the pool, use the provided keys
			logger.FromContext(ctx).
				WithField("ami", spec.Instance.AMI).
				WithField("pool", spec.Instance.Use).
				WithField("ip", ip).
				WithField("id", id).
				Debug("using pool instance")
			spec.Instance.ID = id
			spec.Instance.IP = ip
			createInstance = false
		} else {
			logger.FromContext(ctx).
				WithField("ami", spec.Instance.AMI).
				WithField("pool", spec.Instance.Use).
				Debug("unable to use pool, creating an adhoc instance")
			// do not use the build file to provision an adhoc instance, use the information from the pool file
			spec.Instance = e.opts.Pools[spec.Instance.Use].InstanceSpec.Instance
		}
	}
	// add some tags
	awsTags := spec.Instance.Tags
	awsTags["drone"] = "drone-runner-aws"
	awsTags["creator"] = e.opts.RunnerName
	if justSetup {
		// only set the poolname if we are spawning an instance from the pool file
		awsTags["pool"] = spec.PoolName
	}
	if spec.Instance.Use != "" {
		// tag so no other builds steal this instance. only happens when the pool is empty
		awsTags["status"] = "build in progress"
	}

	// provisioning information
	provArgs := platform.ProvisionArgs{
		Image:         spec.Instance.AMI,
		IamProfileArn: spec.Instance.IAMProfileARN,
		Size:          spec.Instance.Type,
		Region:        spec.Account.Region,
		Userdata:      spec.Instance.UserData,
		// Tags:
		Tags: awsTags,
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
	if createInstance {
		startTime := time.Now()
		logger.FromContext(ctx).
			WithField("ami", spec.Instance.AMI).
			WithField("pool", spec.Instance.Use).
			Debug("creating instance")

		instance, createErr := platform.Create(ctx, creds, &provArgs)
		if createErr != nil {
			logger.FromContext(ctx).
				WithError(createErr).
				WithField("ami", spec.Instance.AMI).
				WithField("pool", spec.Instance.Use).
				Debug("failed to create the instance")
			return createErr
		}
		logger.FromContext(ctx).
			WithField("ami", spec.Instance.AMI).
			WithField("pool", spec.Instance.Use).
			WithField("id", instance.ID).
			WithField("ip", instance.IP).
			WithField("time(seconds)", (time.Since(startTime)).Seconds()).
			Info("created the instance")

		spec.Instance.ID = instance.ID
		spec.Instance.IP = instance.IP
	}
	// establish an ssh connection with the server instance to setup the build environment (upload build scripts, etc)
	client, dialErr := ssh.DialRetry(
		ctx,
		spec.Instance.IP,
		spec.Instance.User,
		spec.Instance.PrivateKey,
	)
	if dialErr != nil {
		logger.FromContext(ctx).
			WithError(dialErr).
			WithField("ami", spec.Instance.AMI).
			WithField("pool", spec.Instance.Use).
			WithField("ip", spec.Instance.IP).
			WithField("id", spec.Instance.ID).
			WithField("error", dialErr).
			Debug("failed to create client for ssh")
		return dialErr
	}
	defer client.Close()

	logger.FromContext(ctx).
		WithField("ami", spec.Instance.AMI).
		WithField("pool", spec.Instance.Use).
		WithField("ip", spec.Instance.IP).
		WithField("id", spec.Instance.ID).
		Debug("Instance responding")

	clientftp, err := sftp.NewClient(client)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ami", spec.Instance.AMI).
			WithField("pool", spec.Instance.Use).
			WithField("ip", spec.Instance.IP).
			WithField("id", spec.Instance.ID).
			Debug("failed to create sftp client")
		return err
	}
	if clientftp != nil {
		defer clientftp.Close()
	}
	// setup common things, no matter what pipeline would use it
	if createInstance {
		mkdirErr := mkdir(clientftp, spec.Root, 0777) //nolint:gomnd // r/w/x for all users
		if mkdirErr != nil {
			logger.FromContext(ctx).
				WithError(mkdirErr).
				WithField("ami", spec.Instance.AMI).
				WithField("pool", spec.Instance.Use).
				WithField("ip", spec.Instance.IP).
				WithField("id", spec.Instance.ID).
				WithField("path", spec.Root).
				Error("cannot create workspace directory")
			return mkdirErr
		}
		// create docker network
		session, sessionErr := client.NewSession()
		if sessionErr != nil {
			logger.FromContext(ctx).
				WithError(sessionErr).
				WithField("ami", spec.Instance.AMI).
				WithField("pool", spec.Instance.Use).
				WithField("ip", spec.Instance.IP).
				WithField("id", spec.Instance.ID).
				Debug("failed to create session")
			return sessionErr
		}
		defer session.Close()
		// keep checking until docker is ok
		dockerErr := ssh.ApplicationRetry(ctx, client, "docker ps")
		if dockerErr != nil {
			logger.FromContext(ctx).
				WithError(dockerErr).
				WithField("ami", spec.Instance.AMI).
				WithField("pool", spec.Instance.Use).
				WithField("ip", spec.Instance.IP).
				WithField("id", spec.Instance.ID).
				Debug("docker failed to start in a timely fashion")
			return err
		}

		networkCommand := "docker network create myNetwork"
		if spec.Platform.OS == "windows" {
			networkCommand = "docker network create --driver nat myNetwork"
		}
		err = session.Run(networkCommand)
		if err != nil {
			logger.FromContext(ctx).
				WithError(err).
				WithField("ami", spec.Instance.AMI).
				WithField("pool", spec.Instance.Use).
				WithField("ip", spec.Instance.IP).
				WithField("id", spec.Instance.ID).
				WithField("command", networkCommand).
				Error("unable to create docker network")
			return err
		}
		logger.FromContext(ctx).
			WithField("ami", spec.Instance.AMI).
			WithField("pool", spec.Instance.Use).
			WithField("ip", spec.Instance.IP).
			WithField("id", spec.Instance.ID).
			Debug("generic setup complete")
	}
	// we are about to use the instance, this section contains pipeline specific info
	if !justSetup {
		// the pipeline specification may define global folders, such as the pipeline working directory, which must be created before pipeline execution begins.
		for _, file := range spec.Files {
			if !file.IsDir {
				continue
			}
			mkdirErr := mkdir(clientftp, file.Path, file.Mode)
			if mkdirErr != nil {
				logger.FromContext(ctx).
					WithError(mkdirErr).
					WithField("ami", spec.Instance.AMI).
					WithField("pool", spec.Instance.Use).
					WithField("ip", spec.Instance.IP).
					WithField("id", spec.Instance.ID).
					WithField("path", file.Path).
					Error("cannot create directory")
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
					WithField("ami", spec.Instance.AMI).
					WithField("pool", spec.Instance.Use).
					WithField("ip", spec.Instance.IP).
					WithField("id", spec.Instance.ID).
					Error("cannot write file")
				return uploadErr
			}
		}

		// create any folders needed for temporary volumes.
		for _, volume := range spec.Volumes {
			if volume.EmptyDir.ID != "" {
				err = mkdir(clientftp, volume.EmptyDir.ID, 0777) //nolint:gomnd // r/w/x for all users
				if err != nil {
					logger.FromContext(ctx).
						WithError(err).
						WithField("ami", spec.Instance.AMI).
						WithField("pool", spec.Instance.Use).
						WithField("ip", spec.Instance.IP).
						WithField("id", spec.Instance.ID).
						WithField("path", volume.EmptyDir.ID).
						Error("cannot create directory for temporary volume")
					return err
				}
			}
		}
		logger.FromContext(ctx).
			WithField("ami", spec.Instance.AMI).
			WithField("pool", spec.Instance.Use).
			WithField("ip", spec.Instance.IP).
			WithField("id", spec.Instance.ID).
			Debug("pipeline specific setup complete")
	}
	return nil
}

// Destroy the pipeline environment.
func (e *Engine) Destroy(ctx context.Context, specv runtime.Spec) error {
	spec := specv.(*Spec)
	//nolint: gocritic
	// fmt.Printf("\nkey\n%s\n", spec.Instance.PrivateKey) //nolint: gocritic
	// user := "root"
	// if spec.Platform.OS == "windows" {
	// 	user = "Administrator"
	// }
	// fmt.Printf("\nssh -i dev.pem %s@%s\n", user, spec.Instance.IP)
	// _ = os.Remove("dev.pem")
	// f, _ := os.OpenFile("dev.pem", os.O_RDWR|os.O_CREATE, 0400)
	// _, _ = f.WriteString(spec.Instance.PrivateKey)
	// _ = f.Close()

	logger.FromContext(ctx).
		WithField("ami", spec.Instance.AMI).
		WithField("pool", spec.Instance.Use).
		WithField("ip", spec.Instance.IP).
		WithField("id", spec.Instance.ID).
		Debug("destroying instance")

	// create creds
	creds := platform.Credentials{
		Client: spec.Account.AccessKeyID,
		Secret: spec.Account.AccessKeySecret,
		Region: spec.Account.Region,
	}
	instance := platform.Instance{
		ID: spec.Instance.ID,
		IP: spec.Instance.IP,
	}
	err := platform.Destroy(ctx, creds, &instance)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ami", spec.Instance.AMI).
			WithField("pool", spec.Instance.Use).
			WithField("ip", spec.Instance.IP).
			WithField("id", spec.Instance.ID).
			Debug("failed to destroy the instance")
		return err
	}

	// repopulate the build pool, if needed. This is in destroy, because if in Run, it will slow the build.
	// NB if we are destroying an adhoc instance from a pool (from an empty pool), this code will not be triggered because we overwrote spec.instance.
	// preventing too many instances being created for a pool
	if spec.Instance.Use != "" {
		poolCount, countPoolErr := platform.PoolCountFree(ctx, creds, spec.Instance.Use, e.opts.AwsMutex)
		if countPoolErr != nil {
			logger.FromContext(ctx).
				WithError(countPoolErr).
				WithField("ami", spec.Instance.AMI).
				WithField("pool", spec.Instance.Use).
				Errorf("failed to count pool")
		}

		if poolCount < e.opts.Pools[spec.Instance.Use].PoolSize {
			createInstanceErr := e.Setup(ctx, e.opts.Pools[spec.Instance.Use].InstanceSpec)
			if createInstanceErr != nil {
				logger.FromContext(ctx).
					WithError(createInstanceErr).
					WithField("ami", spec.Instance.AMI).
					WithField("pool", spec.Instance.Use).
					Errorf("failed to add back to the pool")
			} else {
				logger.FromContext(ctx).
					WithField("ami", spec.Instance.AMI).
					WithField("pool", spec.Instance.Use).
					Debug("added to the pool")
			}
		}
	}
	return nil
}

// Run runs the pipeline step.
func (e *Engine) Run(ctx context.Context, specv runtime.Spec, stepv runtime.Step, output io.Writer) (*runtime.State, error) { //nolint:funlen // its complex but standard
	spec := specv.(*Spec)
	step := stepv.(*Step)

	client, err := ssh.Dial(
		spec.Instance.IP,
		spec.Instance.User,
		spec.Instance.PrivateKey,
	)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ami", spec.Instance.AMI).
			WithField("pool", spec.Instance.Use).
			WithField("ip", spec.Instance.IP).
			WithField("id", spec.Instance.ID).
			WithField("error", err).
			Debug("failed to create client for ssh")
		return nil, err
	}
	defer client.Close()
	// keep checking until docker is ok
	dockerErr := ssh.ApplicationRetry(ctx, client, "docker ps")
	if dockerErr != nil {
		logger.FromContext(ctx).
			WithError(dockerErr).
			WithField("ami", spec.Instance.AMI).
			WithField("pool", spec.Instance.Use).
			WithField("ip", spec.Instance.IP).
			WithField("id", spec.Instance.ID).
			Debug("docker failed to start in a timely fashion")
		return nil, err
	}
	clientftp, err := sftp.NewClient(client)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ami", spec.Instance.AMI).
			WithField("pool", spec.Instance.Use).
			WithField("ip", spec.Instance.IP).
			WithField("id", spec.Instance.ID).
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
				WithField("ami", spec.Instance.AMI).
				WithField("pool", spec.Instance.Use).
				WithField("ip", spec.Instance.IP).
				WithField("id", spec.Instance.ID).
				WithField("path", file.Path).
				Error("cannot write file")
			return nil, err
		}
	}

	session, err := client.NewSession()
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ami", spec.Instance.AMI).
			WithField("pool", spec.Instance.Use).
			WithField("ip", spec.Instance.IP).
			WithField("id", spec.Instance.ID).
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
		if sigErr := session.Signal(cryptoSSH.SIGKILL); sigErr != nil {
			log.WithError(sigErr).Debug("kill remote process")
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
		WithField("ami", spec.Instance.AMI).
		WithField("pool", spec.Instance.Use).
		WithField("ip", spec.Instance.IP).
		WithField("id", spec.Instance.ID).
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
