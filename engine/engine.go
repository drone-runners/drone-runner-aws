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

// create an aws instance for the pool, it will not perform build specific setup.
func (eng *Engine) Provision(ctx context.Context, poolInfo *Pool, addBuildingTag bool) (id, ip string, err error) { //nolint:funlen
	// create creds
	creds := platform.Credentials{
		Client: poolInfo.Account.AccessKeyID,
		Secret: poolInfo.Account.AccessKeySecret,
		Region: poolInfo.Account.Region,
	}
	// add some tags
	awsTags := poolInfo.Instance.Tags
	awsTags["drone"] = "drone-runner-aws"
	awsTags["creator"] = eng.opts.RunnerName
	if addBuildingTag {
		awsTags["status"] = "build in progress"
	} else {
		awsTags["pool"] = poolInfo.Name
	}
	// provisioning information
	provArgs := platform.ProvisionArgs{
		Image:         poolInfo.Instance.AMI,
		IamProfileArn: poolInfo.Instance.IAMProfileARN,
		Size:          poolInfo.Instance.Type,
		Region:        poolInfo.Account.Region,
		Userdata:      poolInfo.Instance.UserData,
		// Tags:
		Tags: awsTags,
		// network
		Subnet:    poolInfo.Instance.Network.SubnetID,
		Groups:    poolInfo.Instance.Network.SecurityGroups,
		Device:    poolInfo.Instance.Device.Name,
		PrivateIP: poolInfo.Instance.Network.PrivateIP,
		// disk
		VolumeType: poolInfo.Instance.Disk.Type,
		VolumeSize: poolInfo.Instance.Disk.Size,
		VolumeIops: poolInfo.Instance.Disk.Iops,
	}
	// create the instance
	startTime := time.Now()
	logger.FromContext(ctx).
		WithField("ami", poolInfo.Instance.AMI).
		WithField("pool", awsTags["pool"]).
		WithField("adhoc", addBuildingTag).
		Debug("provision: creating instance")
	instance, createErr := platform.Create(ctx, creds, &provArgs)
	if createErr != nil {
		logger.FromContext(ctx).
			WithError(createErr).
			WithField("ami", poolInfo.Instance.AMI).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			Debug("provision: failed to create the instance")
		return "", "", createErr
	}
	logger.FromContext(ctx).
		WithField("ami", poolInfo.Instance.AMI).
		WithField("pool", awsTags["pool"]).
		WithField("adhoc", addBuildingTag).
		WithField("id", instance.ID).
		WithField("ip", instance.IP).
		WithField("time(seconds)", (time.Since(startTime)).Seconds()).
		Info("provision: created the instance")
	// we have a system
	poolInfo.Instance.ID = instance.ID
	poolInfo.Instance.IP = instance.IP
	// establish an ssh connection with the server
	client, dialErr := ssh.DialRetry(
		ctx,
		poolInfo.Instance.IP,
		poolInfo.Instance.User,
		poolInfo.Instance.PrivateKey,
	)
	if dialErr != nil {
		logger.FromContext(ctx).
			WithError(dialErr).
			WithField("ami", poolInfo.Instance.AMI).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", poolInfo.Instance.IP).
			WithField("id", poolInfo.Instance.ID).
			WithField("error", dialErr).
			Debug("provision: failed to create client for ssh")
		return "", "", dialErr
	}
	defer client.Close()
	logger.FromContext(ctx).
		WithField("ami", poolInfo.Instance.AMI).
		WithField("pool", awsTags["pool"]).
		WithField("adhoc", addBuildingTag).
		WithField("ip", poolInfo.Instance.IP).
		WithField("id", poolInfo.Instance.ID).
		Debug("provision: Instance responding")
	clientftp, err := sftp.NewClient(client)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ami", poolInfo.Instance.AMI).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", poolInfo.Instance.IP).
			WithField("id", poolInfo.Instance.ID).
			Debug("provision: failed to create sftp client")
		return "", "", err
	}
	if clientftp != nil {
		defer clientftp.Close()
	}
	// setup common things, no matter what pipeline would use it
	mkdirErr := mkdir(clientftp, poolInfo.Root, 0777) //nolint:gomnd // r/w/x for all users
	if mkdirErr != nil {
		logger.FromContext(ctx).
			WithError(mkdirErr).
			WithField("ami", poolInfo.Instance.AMI).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", poolInfo.Instance.IP).
			WithField("id", poolInfo.Instance.ID).
			WithField("path", poolInfo.Root).
			Error("provision: cannot create workspace directory")
		return "", "", mkdirErr
	}
	// create docker network
	session, sessionErr := client.NewSession()
	if sessionErr != nil {
		logger.FromContext(ctx).
			WithError(sessionErr).
			WithField("ami", poolInfo.Instance.AMI).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", poolInfo.Instance.IP).
			WithField("id", poolInfo.Instance.ID).
			Debug("provision: failed to create session")
		return "", "", sessionErr
	}
	defer session.Close()
	// keep checking until docker is ok
	dockerErr := ssh.RetryApplication(ctx, client, "docker ps")
	if dockerErr != nil {
		logger.FromContext(ctx).
			WithError(dockerErr).
			WithField("ami", poolInfo.Instance.AMI).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", poolInfo.Instance.IP).
			WithField("id", poolInfo.Instance.ID).
			Debug("provision: docker failed to start in a timely fashion")
		return "", "", err
	}
	// create docker network
	networkCommand := "docker network create myNetwork"
	if poolInfo.Platform.OS == "windows" {
		networkCommand = "docker network create --driver nat myNetwork"
	}
	err = session.Run(networkCommand)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ami", poolInfo.Instance.AMI).
			WithField("pool", awsTags["pool"]).
			WithField("adhoc", addBuildingTag).
			WithField("ip", poolInfo.Instance.IP).
			WithField("id", poolInfo.Instance.ID).
			WithField("command", networkCommand).
			Error("provision: unable to create docker network")
		return "", "", err
	}
	logger.FromContext(ctx).
		WithField("ami", poolInfo.Instance.AMI).
		WithField("pool", awsTags["pool"]).
		WithField("adhoc", addBuildingTag).
		WithField("ip", poolInfo.Instance.IP).
		WithField("id", poolInfo.Instance.ID).
		Info("provision: complete")
	return poolInfo.Instance.ID, poolInfo.Instance.IP, nil
}

// Setup the pipeline environment.
func (eng *Engine) Setup(ctx context.Context, specv runtime.Spec) error { //nolint:funlen,gocyclo // its complex but standard
	spec := specv.(*Spec)
	// create creds
	creds := platform.Credentials{
		Client: spec.Pool.Account.AccessKeyID,
		Secret: spec.Pool.Account.AccessKeySecret,
		Region: spec.Pool.Account.Region,
	}
	// lets see if there is anything in the pool
	found, id, ip, poolErr := platform.TryPool(ctx, creds, spec.Pool.Name, eng.opts.AwsMutex)
	if poolErr != nil {
		logger.FromContext(ctx).
			WithError(poolErr).
			WithField("ami", spec.Pool.Instance.AMI).
			WithField("pool", spec.Pool.Name).
			Errorf("setup: failed to use pool")
	}
	if found {
		// using the pool, use the provided keys
		logger.FromContext(ctx).
			WithField("ami", spec.Pool.Instance.AMI).
			WithField("pool", spec.Pool.Name).
			WithField("ip", ip).
			WithField("id", id).
			Debug("setup: using pool instance")
		spec.Pool.Instance.ID = id
		spec.Pool.Instance.IP = ip
	} else {
		logger.FromContext(ctx).
			WithField("ami", spec.Pool.Instance.AMI).
			WithField("pool", spec.Pool.Name).
			Debug("setup: pool empty, creating an adhoc instance")
		var provisionErr error
		spec.Pool.Instance.ID, spec.Pool.Instance.IP, provisionErr = eng.Provision(ctx, &spec.Pool, true)
		if provisionErr != nil {
			return provisionErr
		}
	}
	// we are about to use the instance, this section contains pipeline specific info
	client, sshErr := ssh.Dial(
		spec.Pool.Instance.IP,
		spec.Pool.Instance.User,
		spec.Pool.Instance.PrivateKey,
	)
	if sshErr != nil {
		logger.FromContext(ctx).
			WithError(sshErr).
			WithField("ami", spec.Pool.Instance.AMI).
			WithField("pool", spec.Pool.Name).
			WithField("ip", spec.Pool.Instance.IP).
			WithField("id", spec.Pool.Instance.ID).
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
			WithField("ami", spec.Pool.Instance.AMI).
			WithField("pool", spec.Pool.Name).
			WithField("ip", spec.Pool.Instance.IP).
			WithField("id", spec.Pool.Instance.ID).
			Debug("setup: docker failed to start in a timely fashion")
		return dockerErr
	}
	clientftp, clientErr := sftp.NewClient(client)
	if clientErr != nil {
		logger.FromContext(ctx).
			WithError(clientErr).
			WithField("ami", spec.Pool.Instance.AMI).
			WithField("pool", spec.Pool.Name).
			WithField("ip", spec.Pool.Instance.IP).
			WithField("id", spec.Pool.Instance.ID).
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
				WithField("ami", spec.Pool.Instance.AMI).
				WithField("pool", spec.Pool.Name).
				WithField("ip", spec.Pool.Instance.IP).
				WithField("id", spec.Pool.Instance.ID).
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
				WithField("ami", spec.Pool.Instance.AMI).
				WithField("pool", spec.Pool.Name).
				WithField("ip", spec.Pool.Instance.IP).
				WithField("id", spec.Pool.Instance.ID).
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
					WithField("ami", spec.Pool.Instance.AMI).
					WithField("pool", spec.Pool.Name).
					WithField("ip", spec.Pool.Instance.IP).
					WithField("id", spec.Pool.Instance.ID).
					WithField("path", volume.EmptyDir.ID).
					Error("setup: cannot create directory for temporary volume")
				return mkdirErr
			}
		}
	}
	logger.FromContext(ctx).
		WithField("ami", spec.Pool.Instance.AMI).
		WithField("pool", spec.Pool.Name).
		WithField("ip", spec.Pool.Instance.IP).
		WithField("id", spec.Pool.Instance.ID).
		Debug("setup: complete")

	return nil
}

// Destroy the pipeline environment.
func (eng *Engine) Destroy(ctx context.Context, specv runtime.Spec) error {
	spec := specv.(*Spec)
	//nolint: gocritic
	// fmt.Printf("\nssh -i dev.pem %s@%s\n", spec.Pool.Instance.User, spec.Pool.Instance.IP)
	// _ = os.Remove("dev.pem")
	// f, _ := os.OpenFile("dev.pem", os.O_RDWR|os.O_CREATE, 0400) //nolint: gomnd
	// _, _ = f.WriteString(spec.Pool.Instance.PrivateKey)
	// _ = f.Close()
	logger.FromContext(ctx).
		WithField("ami", spec.Pool.Instance.AMI).
		WithField("pool", spec.Pool.Name).
		WithField("ip", spec.Pool.Instance.IP).
		WithField("id", spec.Pool.Instance.ID).
		Debug("destroy: start")

	// create creds
	creds := platform.Credentials{
		Client: spec.Pool.Account.AccessKeyID,
		Secret: spec.Pool.Account.AccessKeySecret,
		Region: spec.Pool.Account.Region,
	}
	instance := platform.Instance{
		ID: spec.Pool.Instance.ID,
		IP: spec.Pool.Instance.IP,
	}
	err := platform.Destroy(ctx, creds, &instance)
	if err != nil {
		logger.FromContext(ctx).
			WithError(err).
			WithField("ami", spec.Pool.Instance.AMI).
			WithField("pool", spec.Pool.Name).
			WithField("ip", spec.Pool.Instance.IP).
			WithField("id", spec.Pool.Instance.ID).
			Debug("destroy: failed to destroy the instance")
		return err
	}

	// repopulate the build pool, if needed. This is in destroy, because if in Run, it will slow the build.
	// NB if we are destroying an adhoc instance from a pool (from an empty pool), this code will not be triggered because we overwrote spec.instance.
	// preventing too many instances being created for a pool
	if eng.opts.RunnerName != "exec" {
		poolCount, countPoolErr := platform.PoolCountFree(ctx, creds, spec.Pool.Name, eng.opts.AwsMutex)
		if countPoolErr != nil {
			logger.FromContext(ctx).
				WithError(countPoolErr).
				WithField("ami", spec.Pool.Instance.AMI).
				WithField("pool", spec.Pool.Name).
				Errorf("destroy: failed to checking pool")
		}
		if poolCount < spec.Pool.MaxPoolSize {
			id, ip, provisionErr := eng.Provision(ctx, &spec.Pool, false)
			if provisionErr != nil {
				logger.FromContext(ctx).
					WithError(provisionErr).
					WithField("ami", spec.Pool.Instance.AMI).
					WithField("pool", spec.Pool.Name).
					Errorf("destroy: failed to add back to the pool")
			} else {
				logger.FromContext(ctx).
					WithField("ami", spec.Pool.Instance.AMI).
					WithField("ip", ip).
					WithField("id", id).
					WithField("pool", spec.Pool.Name).
					Debug("destroy: add back to the pool")
			}
		}
	}
	logger.FromContext(ctx).
		WithField("ami", spec.Pool.Instance.AMI).
		WithField("pool", spec.Pool.Name).
		WithField("ip", spec.Pool.Instance.IP).
		WithField("id", spec.Pool.Instance.ID).
		Debug("destroy: complete")
	return nil
}

// Run runs the pipeline step.
func (eng *Engine) Run(ctx context.Context, specv runtime.Spec, stepv runtime.Step, output io.Writer) (*runtime.State, error) { //nolint:funlen // its complex but standard
	spec := specv.(*Spec)
	step := stepv.(*Step)

	client, clientErr := ssh.Dial(
		spec.Pool.Instance.IP,
		spec.Pool.Instance.User,
		spec.Pool.Instance.PrivateKey,
	)
	if clientErr != nil {
		logger.FromContext(ctx).
			WithError(clientErr).
			WithField("ami", spec.Pool.Instance.AMI).
			WithField("pool", spec.Pool.Name).
			WithField("ip", spec.Pool.Instance.IP).
			WithField("id", spec.Pool.Instance.ID).
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
			WithField("ami", spec.Pool.Instance.AMI).
			WithField("pool", spec.Pool.Name).
			WithField("ip", spec.Pool.Instance.IP).
			WithField("id", spec.Pool.Instance.ID).
			Debug("run: docker failed to start in a timely fashion")
		return nil, dockerErr
	}
	clientftp, ftpErr := sftp.NewClient(client)
	if ftpErr != nil {
		logger.FromContext(ctx).
			WithError(ftpErr).
			WithField("ami", spec.Pool.Instance.AMI).
			WithField("pool", spec.Pool.Name).
			WithField("ip", spec.Pool.Instance.IP).
			WithField("id", spec.Pool.Instance.ID).
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
		writeSecrets(w, spec.Pool.Platform.OS, step.Secrets)
		writeEnviron(w, spec.Pool.Platform.OS, step.Envs)
		w.Write(file.Data)
		uploadErr := upload(clientftp, file.Path, w.Bytes(), file.Mode)
		if uploadErr != nil {
			logger.FromContext(ctx).
				WithError(uploadErr).
				WithField("ami", spec.Pool.Instance.AMI).
				WithField("pool", spec.Pool.Name).
				WithField("ip", spec.Pool.Instance.IP).
				WithField("id", spec.Pool.Instance.ID).
				WithField("path", file.Path).
				Error("run: cannot write file")
			return nil, uploadErr
		}
	}

	session, sessionErr := client.NewSession()
	if sessionErr != nil {
		logger.FromContext(ctx).
			WithError(sessionErr).
			WithField("ami", spec.Pool.Instance.AMI).
			WithField("pool", spec.Pool.Name).
			WithField("ip", spec.Pool.Instance.IP).
			WithField("id", spec.Pool.Instance.ID).
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
		WithField("ami", spec.Pool.Instance.AMI).
		WithField("pool", spec.Pool.Name).
		WithField("ip", spec.Pool.Instance.IP).
		WithField("id", spec.Pool.Instance.ID).
		Debug("run: ssh session finished")
	return state, stepErr
}

func (eng *Engine) Ping(ctx context.Context, accessKeyID, accessKeySecret, region string) error {
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
