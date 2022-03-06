// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package command

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/internal"
	"github.com/drone-runners/drone-runner-aws/engine"
	"github.com/drone-runners/drone-runner-aws/engine/compiler"
	"github.com/drone-runners/drone-runner-aws/engine/linter"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/poolfile"
	"github.com/drone/drone-go/drone"
	"github.com/drone/envsubst"
	"github.com/drone/runner-go/environ"
	"github.com/drone/runner-go/environ/provider"
	"github.com/drone/runner-go/logger"
	"github.com/drone/runner-go/manifest"
	"github.com/drone/runner-go/pipeline"
	"github.com/drone/runner-go/pipeline/runtime"
	"github.com/drone/runner-go/pipeline/streamer/console"
	"github.com/drone/runner-go/secret"
	"github.com/drone/signal"

	"github.com/mattn/go-isatty"
	"github.com/sirupsen/logrus"
	"gopkg.in/alecthomas/kingpin.v2"
)

type execCommand struct {
	*internal.Flags
	Source  *os.File
	Pool    string
	Include []string
	Exclude []string
	Environ map[string]string
	Secrets map[string]string
	Volumes []string
	Pretty  bool
	Procs   int64
	Debug   bool
	Trace   bool
	Dump    bool
}

func (c *execCommand) run(*kingpin.ParseContext) error { //nolint:gocyclo // its complex but not too bad.
	const runnerName = "exec"

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

	// string substitution function ensures that string
	// replacement variables are escaped and quoted if they
	// contain newlines.
	subf := func(k string) string {
		v := envs[k]
		if strings.Contains(v, "\n") {
			v = fmt.Sprintf("%q", v)
		}
		return v
	}

	// evaluates string replacement expressions and returns an
	// update configuration.
	env, err := envsubst.Eval(string(rawsource), subf)
	if err != nil {
		return err
	}

	// parse and lint the configuration.
	mnfst, err := manifest.ParseString(env)
	if err != nil {
		return err
	}

	// a configuration can contain multiple pipelines.
	// get a specific pipeline resource for execution.
	res, err := resource.Lookup(c.Stage.Name, mnfst)
	if err != nil {
		return err
	}
	// we have enough information for default pool settings
	defaultPoolSettings := drivers.DefaultSettings{
		RunnerName: runnerName,
	}

	poolFile, err := config.ParseFile(c.Pool)
	if err != nil {
		logrus.WithError(err).
			Errorln("exec: unable to parse pool file")
		return err
	}

	pools, err := poolfile.ProcessPool(poolFile, &defaultPoolSettings, nil)
	if err != nil {
		logrus.WithError(err).
			Errorln("exec: unable to process pool file")
		return err
	}

	poolManager := &drivers.Manager{}
	err = poolManager.Add(pools...)
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
		Registry:    nil,
	}

	// lint the pipeline and return an error if any linting rules are broken
	lint := linter.New()
	lint.PoolManager = poolManager
	err = lint.Lint(res, c.Repo)
	if err != nil {
		return err
	}

	// set the pools into the compiler
	comp.PoolManager = poolManager
	args := runtime.CompilerArgs{
		Pipeline: res,
		Manifest: mnfst,
		Build:    c.Build,
		Netrc:    c.Netrc,
		Repo:     c.Repo,
		Stage:    c.Stage,
		System:   c.System,
	}
	spec := comp.Compile(nocontext, args).(*engine.Spec)

	// include only steps that are in the include list,
	// if the list in non-empty.
	if len(c.Include) > 0 {
	I:
		for _, step := range spec.Steps {
			if step.Name == "clone" {
				continue
			}
			for _, name := range c.Include {
				if step.Name == name {
					continue I
				}
			}
			step.RunPolicy = runtime.RunNever
		}
	}

	// exclude steps that are in the exclude list,
	// if the list in non-empty.
	if len(c.Exclude) > 0 {
		for _, step := range spec.Steps {
			if step.Name == "clone" {
				continue
			}
			for _, name := range c.Exclude {
				if step.Name == name {
					step.RunPolicy = runtime.RunNever
					break
				}
			}
		}
	}

	// create a step object for each pipeline step.
	for _, step := range spec.Steps {
		if step.RunPolicy == runtime.RunNever {
			continue
		}
		c.Stage.Steps = append(c.Stage.Steps, &drone.Step{
			StageID:   c.Stage.ID,
			Number:    len(c.Stage.Steps) + 1,
			Name:      step.Name,
			Status:    drone.StatusPending,
			ErrIgnore: step.ErrPolicy == runtime.ErrIgnore,
		})
	}

	// configures the pipeline timeout.
	timeout := time.Duration(c.Repo.Timeout) * time.Minute
	ctx, cancel := context.WithTimeout(nocontext, timeout)
	defer cancel()

	// listen for operating system signals and cancel execution when received.
	ctx = signal.WithContextFunc(ctx, func() {
		println("received signal, terminating process")
		cancel()
	})

	state := &pipeline.State{
		Build:  c.Build,
		Stage:  c.Stage,
		Repo:   c.Repo,
		System: c.System,
	}

	// enable debug logging
	logrus.SetLevel(logrus.WarnLevel)
	if c.Debug {
		logrus.SetLevel(logrus.DebugLevel)
	}
	if c.Trace {
		logrus.SetLevel(logrus.TraceLevel)
	}
	logger.Default = logger.Logrus(
		logrus.NewEntry(
			logrus.StandardLogger(),
		),
	)

	engineInstance, err := engine.New(engine.Opts{
		Repopulate: false,
	}, poolManager, &defaultPoolSettings)
	if err != nil {
		return err
	}

	err = runtime.NewExecer(
		pipeline.NopReporter(),
		console.New(c.Pretty),
		pipeline.NopUploader(),
		engineInstance,
		c.Procs,
	).Exec(ctx, spec, state)

	if c.Dump {
		dump(state)
	}
	if err != nil {
		return err
	}
	switch state.Stage.Status {
	case drone.StatusError, drone.StatusFailing, drone.StatusKilled:
		os.Exit(1) //nolint:gocritic // failing out if something goes wrong
	}
	return nil
}

func dump(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func registerExec(app *kingpin.Application) {
	c := new(execCommand)
	c.Environ = map[string]string{}
	c.Secrets = map[string]string{}

	cmd := app.Command("exec", "executes a pipeline").
		Action(c.run)

	cmd.Arg("source", "source file location").
		Default(".drone.yml").
		FileVar(&c.Source)

	cmd.Arg("pool", "file to seed the aws pool").
		Default("pool.yml").
		StringVar(&c.Pool)

	cmd.Flag("secrets", "secret parameters").
		StringMapVar(&c.Secrets)

	cmd.Flag("volumes", "drone runner volumes").
		StringsVar(&c.Volumes)

	cmd.Flag("include", "include pipeline steps").
		StringsVar(&c.Include)

	cmd.Flag("exclude", "exclude pipeline steps").
		StringsVar(&c.Exclude)

	cmd.Flag("environ", "environment variables").
		StringMapVar(&c.Environ)

	cmd.Flag("debug", "enable debug logging").
		BoolVar(&c.Debug)

	cmd.Flag("trace", "enable trace logging").
		BoolVar(&c.Trace)

	cmd.Flag("dump", "dump the pipeline state to stdout").
		BoolVar(&c.Dump)

	cmd.Flag("pretty", "pretty print the output").
		Default(
			fmt.Sprint(
				isatty.IsTerminal(
					os.Stdout.Fd(),
				),
			),
		).BoolVar(&c.Pretty)
	// shared pipeline flags
	c.Flags = internal.ParseFlags(cmd)
}
