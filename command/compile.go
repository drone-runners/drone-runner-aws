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

	"github.com/drone-runners/drone-runner-aws/command/internal"
	"github.com/drone-runners/drone-runner-aws/engine/compiler"
	"github.com/drone-runners/drone-runner-aws/engine/linter"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool/cloudaws"

	"github.com/drone/envsubst"
	"github.com/drone/runner-go/environ"
	"github.com/drone/runner-go/environ/provider"
	"github.com/drone/runner-go/manifest"
	"github.com/drone/runner-go/pipeline/runtime"
	"github.com/drone/runner-go/secret"

	"gopkg.in/alecthomas/kingpin.v2"
)

type compileCommand struct {
	*internal.Flags
	Source   *os.File
	Poolfile string
	Environ  map[string]string
	Secrets  map[string]string
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
	config, err := envsubst.Eval(string(rawsource), subf)
	if err != nil {
		return err
	}
	// parse and lint the configuration
	mnfst, err := manifest.ParseString(config)
	if err != nil {
		return err
	}
	// a configuration can contain multiple pipelines. get a specific pipeline resource for execution.
	resourceInstance, err := resource.Lookup(c.Stage.Name, mnfst)
	if err != nil {
		return err
	}
	// we have enough information for default pool settings
	defaultPoolSettings := vmpool.DefaultSettings{
		RunnerName:         runnerName,
		AwsAccessKeyID:     c.Environ["DRONE_SETTINGS_AWS_ACCESS_KEY_ID"],
		AwsAccessKeySecret: c.Environ["DRONE_SETTINGS_AWS_ACCESS_KEY_SECRET"],
	}
	// read the poolfile
	pools, poolFileErr := cloudaws.ProcessPoolFile(c.Poolfile, &defaultPoolSettings)
	if poolFileErr != nil {
		return poolFileErr
	}

	poolManager := &vmpool.Manager{}
	err = poolManager.Add(pools...)
	if err != nil {
		return err
	}

	// lint the pipeline and return an error if any linting rules are broken
	lint := linter.New()
	lint.PoolManager = poolManager
	err = lint.Lint(resourceInstance, c.Repo)
	if err != nil {
		return err
	}
	// compile the pipeline to an intermediate representation.
	comp := &compiler.Compiler{
		Environ:     provider.Static(c.Environ),
		Secret:      secret.StaticVars(c.Secrets),
		PoolManager: poolManager,
	}
	args := runtime.CompilerArgs{
		Pipeline: resourceInstance,
		Manifest: mnfst,
		Build:    c.Build,
		Netrc:    c.Netrc,
		Repo:     c.Repo,
		Stage:    c.Stage,
		System:   c.System,
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

	cmd.Arg("poolfile", "file to seed the aws pool").
		Default(".drone_pool.yml").
		StringVar(&c.Poolfile)

	cmd.Flag("secrets", "secret parameters").
		StringMapVar(&c.Secrets)

	cmd.Flag("environ", "environment variables").
		StringMapVar(&c.Environ)
	// shared pipeline flags
	c.Flags = internal.ParseFlags(cmd)
}
