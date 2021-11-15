// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package compiler

import (
	"context"
	"fmt"

	"github.com/drone-runners/drone-runner-aws/internal/vmpool"

	"github.com/drone-runners/drone-runner-aws/engine"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/oshelp"

	"github.com/drone/runner-go/clone"
	"github.com/drone/runner-go/environ"
	"github.com/drone/runner-go/environ/provider"
	"github.com/drone/runner-go/manifest"
	"github.com/drone/runner-go/pipeline/runtime"
	"github.com/drone/runner-go/secret"

	"github.com/dchest/uniuri"
	"github.com/gosimple/slug"
)

// random generator function
var random = func() string {
	return "drone-" + uniuri.NewLen(20) //nolint:gomnd
}

// Compiler compiles the Yaml configuration file to an
// intermediate representation optimized for simple execution.
type Compiler struct {
	// Environ provides a set of environment variables that should be added to each pipeline step by default.
	Environ provider.Provider
	// Secret returns a named secret value that can be injected into the pipeline step.
	Secret secret.Provider
	// Pools is a map of named pools that can be referenced by a pipeline.
	PoolManager *vmpool.Manager
}

// Compile compiles the configuration file.
func (c *Compiler) Compile(ctx context.Context, args runtime.CompilerArgs) runtime.Spec { //nolint:funlen,gocritic,gocyclo // its complex but standard
	pipeline := args.Pipeline.(*resource.Pipeline)
	spec := &engine.Spec{}

	targetPool := pipeline.Pool.Use
	pool := c.PoolManager.Get(targetPool)

	// move the pool from the `mapping of pools` into the spec of this pipeline.
	spec.CloudInstance.PoolName = targetPool
	spec.Root = pool.GetRootDir()

	pipelineOS := pool.GetOS()
	// creates a home directory in the root.
	// note: mkdirall fails on windows so we need to create all directories in the tree.
	homedir := oshelp.JoinPaths(pipelineOS, spec.Root, "home", "drone")
	spec.Files = append(spec.Files, &engine.File{
		Path:  oshelp.JoinPaths(pipelineOS, spec.Root, "home"),
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
	sourcedir := oshelp.JoinPaths(pipelineOS, spec.Root, "drone", "src")
	spec.Files = append(spec.Files, &engine.File{
		Path:  oshelp.JoinPaths(pipelineOS, spec.Root, "drone"),
		Mode:  0700,
		IsDir: true,
	}, &engine.File{
		Path:  sourcedir,
		Mode:  0700,
		IsDir: true,
	}, &engine.File{
		Path:  oshelp.JoinPaths(pipelineOS, spec.Root, "opt"),
		Mode:  0700,
		IsDir: true,
	})

	// creates the netrc file
	if args.Netrc != nil && args.Netrc.Password != "" {
		netrcfile := oshelp.GetNetrc(pipelineOS)
		netrcpath := oshelp.JoinPaths(pipelineOS, homedir, netrcfile)
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
		clonepath := oshelp.JoinPaths(pipelineOS, spec.Root, "opt", oshelp.GetExt(pipelineOS, "clone"))
		clonefile := oshelp.GenScript(pipelineOS,
			clone.Commands(
				clone.Args{
					Branch: args.Build.Target,
					Commit: args.Build.After,
					Ref:    args.Build.Ref,
					Remote: args.Repo.HTTPURL,
				},
			),
		)
		cmd, args := oshelp.GetCommand(pipelineOS, clonepath)
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
			path = oshelp.JoinPaths(pipelineOS, spec.Root, random())
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
		buildpath := oshelp.JoinPaths(pipelineOS, spec.Root, "opt", oshelp.GetExt(pipelineOS, buildslug))
		stepEnv := environ.Combine(envs, environ.Expand(convertStaticEnv(src.Environment)))
		// if there is an image associated with the step build a docker cli
		var buildfile string
		if src.Image == "" {
			buildfile = oshelp.GenScript(pipelineOS, src.Commands)
		} else {
			buildfile = oshelp.GenerateDockerCommandLine(pipelineOS, sourcedir, src, stepEnv, pipeLineVolumeMap)
		}

		cmd, args := oshelp.GetCommand(pipelineOS, buildpath)
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
