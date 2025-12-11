// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package command

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/drone/envsubst"
	"github.com/drone/runner-go/environ"
	"github.com/drone/runner-go/environ/provider"
	"github.com/drone/runner-go/manifest"
	"github.com/drone/runner-go/pipeline/runtime"
	"github.com/drone/runner-go/registry"
	"github.com/drone/runner-go/secret"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/poolfile"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/internal"
	"github.com/drone-runners/drone-runner-aws/engine/compiler"
	"github.com/drone-runners/drone-runner-aws/engine/linter"
	"github.com/drone-runners/drone-runner-aws/engine/resource"

	"github.com/sirupsen/logrus"
	"gopkg.in/alecthomas/kingpin.v2"
)

type compileCommand struct {
	*internal.Flags
	Source         *os.File
	Pool           string
	EnableAutoPool bool
	Environ        map[string]string
	Secrets        map[string]string
	Config         string
	Volumes        []string
}

func (c *compileCommand) run(*kingpin.ParseContext) error {
	const runnerName = "drone-runner"

	rawsource, err := io.ReadAll(c.Source)
	if err != nil {
		return err
	}

	envs := environ.Combine(
		c.Environ,
		environ.System(c.System),
		environ.Repo(c.Repo),
		environ.Build(c.Build),
		environ.Stage(c.Stage),
		environ.Link(c.Repo, c.Build, c.System),
		c.Build.Params,
	)
	// string substitution function ensures that string replacement variables are escaped and quoted if they contain newlines.
	subf := func(k string) string {
		v := envs[k]
		if strings.Contains(v, "\n") {
			v = fmt.Sprintf("%q", v)
		}
		return v
	}
	// evaluates string replacement expressions and returns an update configuration.
	env, err := envsubst.Eval(string(rawsource), subf)
	if err != nil {
		return err
	}
	// parse and lint the configuration
	mnfst, err := manifest.ParseString(env)
	if err != nil {
		return err
	}
	// a configuration can contain multiple pipelines. get a specific pipeline resource for execution.
	resourceInstance, err := resource.Lookup(c.Stage.Name, mnfst)
	if err != nil {
		return err
	}

	poolFile, err := config.ParseFile(c.Pool)
	if err != nil {
		logrus.WithError(err).
			Errorln("compile: unable to parse pool file")
		return err
	}

	configEnv, _ := config.FromEnviron()
	pools, err := poolfile.ProcessPool(poolFile, runnerName, configEnv.Passwords(), configEnv.DriverSettings())
	if err != nil {
		logrus.WithError(err).
			Errorln("compile: unable to process pool file")
		return err
	}

	poolManager := &drivers.Manager{}
	err = poolManager.Add(pools...)
	if err != nil {
		return err
	}

	// lint the pipeline and return an error if any linting rules are broken
	lint := linter.New(c.EnableAutoPool)
	lint.PoolManager = poolManager
	err = lint.Lint(resourceInstance, c.Repo)
	if err != nil {
		return err
	}
	// compile the pipeline to an intermediate representation.
	comp := &compiler.Compiler{
		Environ:     provider.Static(c.Environ),
		NetworkOpts: nil,
		Secret:      secret.StaticVars(c.Secrets),
		Volumes:     c.Volumes,
		PoolManager: poolManager,
		Registry:    registry.File(c.Config),
	}
	args := runtime.CompilerArgs{
		Pipeline: resourceInstance,
		Manifest: mnfst,
		Build:    c.Build,
		Netrc:    c.Netrc,
		Repo:     c.Repo,
		Stage:    c.Stage,
		System:   c.System,
		Secret:   secret.StaticVars(c.Secrets),
	}
	spec := comp.Compile(nocontext, args)
	// encode the pipeline in json format and print to the console for inspection.
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(spec)
	return nil
}

func registerCompile(app *kingpin.Application) {
	c := new(compileCommand)
	c.Environ = map[string]string{}
	c.Secrets = map[string]string{}

	cmd := app.Command("compile", "compile the yaml file").
		Action(c.run)

	cmd.Flag("source", "source file location").
		Default(".drone.yml").
		FileVar(&c.Source)

	cmd.Arg("pool", "file to seed the aws pool").
		Default("pool.yml").
		StringVar(&c.Pool)

	cmd.Flag("secrets", "secret parameters").
		StringMapVar(&c.Secrets)

	cmd.Flag("enable_autopool", "enable autopool").
		Default("false").
		BoolVar(&c.EnableAutoPool)

	// Check documentation of DRONE_RUNNER_VOLUMES to see how to
	// use this param.
	cmd.Flag("volumes", "drone runner volumes").
		StringsVar(&c.Volumes)

	cmd.Flag("environ", "environment variables").
		StringMapVar(&c.Environ)

	cmd.Flag("docker-config", "path to the docker config file").
		StringVar(&c.Config)

	c.Flags = internal.ParseFlags(cmd)
}
