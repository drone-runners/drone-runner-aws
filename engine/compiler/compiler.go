// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package compiler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"

	"github.com/buildkite/yaml"
	"github.com/drone-runners/drone-runner-aws/engine"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/sshkey"
	"github.com/drone-runners/drone-runner-aws/internal/userdata"

	"github.com/drone/runner-go/clone"
	"github.com/drone/runner-go/environ"
	"github.com/drone/runner-go/environ/provider"
	"github.com/drone/runner-go/manifest"
	"github.com/drone/runner-go/pipeline/runtime"
	"github.com/drone/runner-go/secret"

	"github.com/dchest/uniuri"
	"github.com/gosimple/slug"
)

const windowsString = "windows"

// random generator function
var random = func() string {
	return "drone-" + uniuri.NewLen(20) //nolint:gomnd
}

// Settings defines default settings.
type Settings struct {
	AwsAccessKeyID     string
	AwsAccessKeySecret string
	AwsRegion          string
	PrivateKeyFile     string
	PublicKeyFile      string
	PoolFile           string
}

// Compiler compiles the Yaml configuration file to an
// intermediate representation optimized for simple execution.
type Compiler struct {
	// Environ provides a set of environment variables that
	// should be added to each pipeline step by default.
	Environ provider.Provider

	// Secret returns a named secret value that can be injected
	// into the pipeline step.
	Secret secret.Provider

	// Settings provides global settings that apply to
	// all pipelines.
	Settings Settings
}

// Compile compiles the configuration file.
func (c *Compiler) Compile(ctx context.Context, args runtime.CompilerArgs) runtime.Spec { //nolint:funlen,gocritic,gocyclo // its complex but standard
	pipeline := args.Pipeline.(*resource.Pipeline)
	spec := &engine.Spec{}
	// read pool file first.
	targetPool := pipeline.Pool.Use
	pools, poolFileErr := c.ProcessPoolFile(ctx, &c.Settings)
	if poolFileErr != nil {
		log.Printf("unable to read pool file '%s': %s", c.Settings.PoolFile, poolFileErr)
		os.Exit(1)
	}
	// move the pool from the `pool file` into the spec of this pipeline.
	spec.Pool = pools[targetPool]
	// if we dont match lets exit
	if spec.Pool.Name != targetPool {
		log.Printf("unable to find pool '%s' in pool file '%s'", targetPool, c.Settings.PoolFile)
		return spec
	}
	spec.Root = pools[targetPool].Root
	//
	pipelineOS := spec.Pool.Platform.OS
	// creates a home directory in the root.
	// note: mkdirall fails on windows so we need to create all directories in the tree.
	homedir := join(pipelineOS, spec.Root, "home", "drone")
	spec.Files = append(spec.Files, &engine.File{
		Path:  join(pipelineOS, spec.Root, "home"),
		Mode:  0700,
		IsDir: true,
	}, &engine.File{
		Path:  homedir,
		Mode:  0700,
		IsDir: true,
	})

	// creates a source directory in the root.
	// note: mkdirall fails on windows so we need to create all
	// directories in the tree.
	sourcedir := join(pipelineOS, spec.Root, "drone", "src")
	spec.Files = append(spec.Files, &engine.File{
		Path:  join(pipelineOS, spec.Root, "drone"),
		Mode:  0700,
		IsDir: true,
	}, &engine.File{
		Path:  sourcedir,
		Mode:  0700,
		IsDir: true,
	}, &engine.File{
		Path:  join(pipelineOS, spec.Root, "opt"),
		Mode:  0700,
		IsDir: true,
	})

	// creates the netrc file
	if args.Netrc != nil && args.Netrc.Password != "" {
		netrcfile := getNetrc(pipelineOS)
		netrcpath := join(pipelineOS, homedir, netrcfile)
		netrcdata := fmt.Sprintf(
			"machine %s login %s password %s",
			args.Netrc.Machine,
			args.Netrc.Login,
			args.Netrc.Password,
		)
		spec.Files = append(spec.Files, &engine.File{
			Path: netrcpath,
			Mode: 0600,
			Data: []byte(netrcdata),
		})
	}

	// list the global environment variables
	globals, _ := c.Environ.List(ctx, &provider.Request{
		Build: args.Build,
		Repo:  args.Repo,
	})

	// create the default environment variables.
	envs := environ.Combine(
		provider.ToMap(
			provider.FilterUnmasked(globals),
		),
		args.Build.Params,
		pipeline.Environment,
		environ.Proxy(),
		environ.System(args.System),
		environ.Repo(args.Repo),
		environ.Build(args.Build),
		environ.Stage(args.Stage),
		environ.Link(args.Repo, args.Build, args.System),
		clone.Environ(clone.Config{
			SkipVerify: pipeline.Clone.SkipVerify,
			Trace:      pipeline.Clone.Trace,
			User: clone.User{
				Name:  args.Build.AuthorName,
				Email: args.Build.AuthorEmail,
			},
		}),
		map[string]string{
			"HOME":                homedir,
			"HOMEPATH":            homedir, // for windows
			"USERPROFILE":         homedir, // for windows
			"DRONE_HOME":          sourcedir,
			"DRONE_WORKSPACE":     sourcedir,
			"GIT_TERMINAL_PROMPT": "0",
		},
	)

	match := manifest.Match{
		Action:   args.Build.Action,
		Cron:     args.Build.Cron,
		Ref:      args.Build.Ref,
		Repo:     args.Repo.Slug,
		Instance: args.System.Host,
		Target:   args.Build.Deploy,
		Event:    args.Build.Event,
		Branch:   args.Build.Target,
	}

	// create the clone step, maybe
	if !pipeline.Clone.Disable {
		clonepath := join(pipelineOS, spec.Root, "opt", getExt(pipelineOS, "clone"))
		clonefile := genScript(pipelineOS,
			clone.Commands(
				clone.Args{
					Branch: args.Build.Target,
					Commit: args.Build.After,
					Ref:    args.Build.Ref,
					Remote: args.Repo.HTTPURL,
				},
			),
		)
		cmd, args := getCommand(pipelineOS, clonepath)
		spec.Steps = append(spec.Steps, &engine.Step{
			Name:      "clone",
			Args:      args,
			Command:   cmd,
			Envs:      envs,
			RunPolicy: runtime.RunAlways,
			Files: []*engine.File{
				{
					Path: clonepath,
					Mode: 0700,
					Data: []byte(clonefile),
				},
			},
			Secrets:    []*engine.Secret{},
			WorkingDir: sourcedir,
		})
	}

	// create volumes map, name of volume and real life path
	var pipeLineVolumeMap = make(map[string]string, len(pipeline.Volumes))
	for _, v := range pipeline.Volumes {
		path := ""
		if v.EmptyDir != nil {
			path = join(pipelineOS, spec.Root, random())
			// we only need to pass temporary volumes through to engine, to have the folders created
			src := new(engine.Volume)
			src.EmptyDir = &engine.VolumeEmptyDir{
				ID:   path,
				Name: v.Name,
			}
			spec.Volumes = append(spec.Volumes, src)
		} else if v.HostPath != nil {
			path = v.HostPath.Path
		} else {
			continue
		}
		pipeLineVolumeMap[v.Name] = path
	}

	// services are the same as steps, but are executed first and are detached.
	for _, src := range pipeline.Services {
		src.Detach = true
	}
	// combine steps + services
	combinedSteps := append(pipeline.Services, pipeline.Steps...) //nolint:gocritic // creating a new slice is ok
	// create steps
	for _, src := range combinedSteps {
		buildslug := slug.Make(src.Name)
		buildpath := join(pipelineOS, spec.Root, "opt", getExt(pipelineOS, buildslug))
		stepEnv := environ.Combine(envs, environ.Expand(convertStaticEnv(src.Environment)))
		// if there is an image associated with the step build a docker cli
		var buildfile string
		if src.Image == "" {
			buildfile = genScript(pipelineOS, src.Commands)
		} else {
			buildfile = genDockerCommandLine(pipelineOS, sourcedir, src, stepEnv, pipeLineVolumeMap)
		}

		cmd, args := getCommand(pipelineOS, buildpath)
		dst := &engine.Step{
			Name:      src.Name,
			Args:      args,
			Command:   cmd,
			Detach:    src.Detach,
			DependsOn: src.DependsOn,
			Envs:      stepEnv,
			RunPolicy: runtime.RunOnSuccess,
			Files: []*engine.File{
				{
					Path: buildpath,
					Mode: 0700,
					Data: []byte(buildfile),
				},
			},
			Secrets:    convertSecretEnv(src.Environment),
			WorkingDir: sourcedir,
		}
		spec.Steps = append(spec.Steps, dst)

		// set the pipeline step run policy. steps run on success by default, but may be optionally configured to run on failure.
		if isRunAlways(src) {
			dst.RunPolicy = runtime.RunAlways
		} else if isRunOnFailure(src) {
			dst.RunPolicy = runtime.RunOnFailure
		}

		// if the pipeline step has unmet conditions the step is automatically skipped.
		if !src.When.Match(match) {
			dst.RunPolicy = runtime.RunNever
		}
	}

	if !isGraph(spec) {
		configureSerial(spec)
	} else if !pipeline.Clone.Disable {
		configureCloneDeps(spec)
	} else if pipeline.Clone.Disable {
		removeCloneDeps(spec)
	}

	for _, step := range spec.Steps {
		for _, s := range step.Secrets {
			actualSecret, ok := c.findSecret(ctx, args, s.Name)
			if ok {
				s.Data = []byte(actualSecret)
			}
		}
	}

	return spec
}

func (c *Compiler) compilePoolFile(rawPool engine.Pool) (engine.Pool, error) { //nolint:gocritic,gocyclo // its complex but standard
	pipelineOS := rawPool.Platform.OS
	// secrets and error here
	if rawPool.Account.AccessKeyID == "" {
		rawPool.Account.AccessKeyID = c.Settings.AwsAccessKeyID
	}
	if rawPool.Account.AccessKeySecret == "" {
		rawPool.Account.AccessKeySecret = c.Settings.AwsAccessKeySecret
	}
	// we need Access, error if its still empty
	if rawPool.Account.AccessKeyID == "" || rawPool.Account.AccessKeySecret == "" {
		return engine.Pool{}, fmt.Errorf("missing AWS access key or AWS secret. Add to .env file or pool file")
	}
	// try config first. then set the default region if not provided
	if rawPool.Account.Region == "" && c.Settings.AwsRegion != "" {
		rawPool.Account.Region = c.Settings.AwsRegion
	} else if rawPool.Account.Region == "" {
		rawPool.Account.Region = "us-east-1"
	}
	// set default instance type if not provided
	if rawPool.Instance.Type == "" {
		rawPool.Instance.Type = "t3.nano"
		if rawPool.Platform.Arch == "arm64" {
			rawPool.Instance.Type = "a1.medium"
		}
	}
	// put something into tags even if empty
	if rawPool.Instance.Tags == nil {
		rawPool.Instance.Tags = make(map[string]string)
	}
	// set the default disk size if not provided
	if rawPool.Instance.Disk.Size == 0 {
		rawPool.Instance.Disk.Size = 32
	}
	// set the default disk type if not provided
	if rawPool.Instance.Disk.Type == "" {
		rawPool.Instance.Disk.Type = "gp2"
	}
	// set the default iops
	if rawPool.Instance.Disk.Type == "io1" && rawPool.Instance.Disk.Iops == 0 {
		rawPool.Instance.Disk.Iops = 100
	}
	// set the default device
	if rawPool.Instance.Device.Name == "" {
		rawPool.Instance.Device.Name = "/dev/sda1"
	}
	// set the default ssh user. this user account is responsible for executing the pipeline script.
	switch {
	case rawPool.Instance.User == "" && rawPool.Platform.OS == windowsString:
		rawPool.Instance.User = "Administrator"
	case rawPool.Instance.User == "":
		rawPool.Instance.User = "root"
	}
	_, statErr := os.Stat(c.Settings.PrivateKeyFile)
	if os.IsNotExist(statErr) {
		// there are no key files
		publickey, privatekey, generateKeyErr := sshkey.GeneratePair()
		if generateKeyErr != nil {
			publickey = ""
			privatekey = ""
		}
		rawPool.Instance.PrivateKey = privatekey
		rawPool.Instance.PublicKey = publickey
	} else {
		body, privateKeyErr := os.ReadFile(c.Settings.PrivateKeyFile)
		if privateKeyErr != nil {
			log.Fatalf("unable to read file ``: %v", privateKeyErr)
		}
		rawPool.Instance.PrivateKey = string(body)

		body, publicKeyErr := os.ReadFile(c.Settings.PublicKeyFile)
		if publicKeyErr != nil {
			log.Fatalf("unable to read file: %v", publicKeyErr)
		}
		rawPool.Instance.PublicKey = string(body)
	}
	// generate the cloudinit file
	var userDataWithSSH string
	if rawPool.Platform.OS == windowsString {
		userDataWithSSH = userdata.Windows(userdata.Params{
			PublicKey: rawPool.Instance.PublicKey,
		})
	} else {
		// try using cloud init.
		userDataWithSSH = userdata.Linux(userdata.Params{
			PublicKey: rawPool.Instance.PublicKey,
		})
	}
	rawPool.Instance.UserData = userDataWithSSH
	// create the root directory
	rawPool.Root = tempdir(pipelineOS)

	return rawPool, nil
}

func (c *Compiler) ProcessPoolFile(ctx context.Context, compilerSettings *Settings) (foundPools map[string]engine.Pool, err error) {
	rawPool, readPoolFileErr := ioutil.ReadFile(compilerSettings.PoolFile)
	if readPoolFileErr != nil {
		errorMessage := fmt.Sprintf("unable to read file: %s", compilerSettings.PoolFile)
		return nil, fmt.Errorf(errorMessage, readPoolFileErr)
	}
	foundPools = make(map[string]engine.Pool)
	buf := bytes.NewBuffer(rawPool)
	dec := yaml.NewDecoder(buf)

	for {
		rawPool := new(engine.Pool)
		err := dec.Decode(rawPool)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		preppedPool, compilePoolFileErr := c.compilePoolFile(*rawPool)
		if compilePoolFileErr != nil {
			return nil, compilePoolFileErr
		}
		foundPools[rawPool.Name] = preppedPool
	}
	return foundPools, nil
}

// helper function attempts to find and return the named secret. from the secret provider.
func (c *Compiler) findSecret(ctx context.Context, args runtime.CompilerArgs, name string) (s string, ok bool) { //nolint:gocritic // its complex but standard
	if name == "" {
		return
	}
	// source secrets from the global secret provider and the repository secret provider.
	p := secret.Combine(
		args.Secret,
		c.Secret,
	)
	// fine the secret from the provider. please note we
	// currently ignore errors if the secret is not found,
	// which is something that we'll need to address in the
	// next major (breaking) release.
	found, _ := p.Find(ctx, &secret.Request{
		Name:  name,
		Build: args.Build,
		Repo:  args.Repo,
		Conf:  args.Manifest,
	})
	if found == nil {
		return
	}
	return found.Data, true
}
