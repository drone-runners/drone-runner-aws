// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package daemon

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/poolfile"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/engine"
	"github.com/drone-runners/drone-runner-aws/engine/compiler"
	"github.com/drone-runners/drone-runner-aws/engine/linter"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
	"github.com/drone-runners/drone-runner-aws/internal/le"
	"github.com/drone-runners/drone-runner-aws/internal/match"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool"
	"github.com/drone/runner-go/client"
	"github.com/drone/runner-go/environ/provider"
	"github.com/drone/runner-go/handler/router"
	"github.com/drone/runner-go/logger"
	loghistory "github.com/drone/runner-go/logger/history"
	"github.com/drone/runner-go/pipeline"
	"github.com/drone/runner-go/pipeline/reporter/history"
	"github.com/drone/runner-go/pipeline/reporter/remote"
	"github.com/drone/runner-go/pipeline/runtime"
	"github.com/drone/runner-go/poller"
	"github.com/drone/runner-go/registry"
	"github.com/drone/runner-go/secret"
	"github.com/drone/runner-go/server"
	"github.com/drone/signal"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"gopkg.in/alecthomas/kingpin.v2"
)

// empty context.
var nocontext = context.Background()

type daemonCommand struct {
	envFile string
	pool    string
}

func (c *daemonCommand) run(*kingpin.ParseContext) error {
	// load environment variables from file.
	err := godotenv.Load(c.envFile)
	if err != nil {
		return err
	}

	// load the configuration from the environment
	env, err := fromEnviron()
	if err != nil {
		return err
	}

	// TODO: These can be set in the env struct as `required: "true"` instead
	// once we have separate configs for the daemon and the delegate
	if env.Client.Host == "" {
		return errors.New("missing DRONE_RPC_HOST")
	}
	if env.Client.Secret == "" {
		return errors.New("missing DRONE_RPC_SECRET")
	}

	// setup the global logrus logger.
	SetupLogger(&env)

	ctx, cancel := context.WithCancel(nocontext)
	defer cancel()

	// listen for termination signals to gracefully shutdown
	// the runner daemon.
	ctx = signal.WithContextFunc(ctx, func() {
		println("daemon: received signal, terminating process")
		cancel()
	})

	cli := client.New(
		env.Client.Address,
		env.Client.Secret,
		env.Client.SkipVerify,
	)
	if env.Client.Dump {
		cli.Dumper = logger.StandardDumper(
			env.Client.DumpBody,
		)
	}
	cli.Logger = logger.Logrus(
		logrus.NewEntry(
			logrus.StandardLogger(),
		),
	)
	// generate le cert files if needed
	err = le.GenerateLECerts(env.Runner.Name, env.DefaultPoolSettings.CertificateFolder)
	if err != nil {
		logrus.WithError(err).
			Errorln("delegate: failed to generate certificates")
		return err
	}
	// read cert files into memory
	env.DefaultPoolSettings.CaCertFile, env.DefaultPoolSettings.CertFile, env.DefaultPoolSettings.KeyFile, err = le.ReadLECerts(env.DefaultPoolSettings.CertificateFolder)
	if err != nil {
		logrus.WithError(err).
			Errorln("daemon: failed to read certificates")
		return err
	}
	// we have enough information for default pool settings
	defaultPoolSettings := vmpool.DefaultSettings{
		RunnerName:     env.Runner.Name,
		LiteEnginePath: env.DefaultPoolSettings.LiteEnginePath,
		CaCertFile:     env.DefaultPoolSettings.CaCertFile,
		CertFile:       env.DefaultPoolSettings.CertFile,
		KeyFile:        env.DefaultPoolSettings.KeyFile,
	}

	poolManager := &vmpool.Manager{}
	poolManager.SetGlobalCtx(ctx)

	poolFile, err := config.ProcessPoolFile(c.pool)
	if err != nil {
		logrus.WithError(err).
			Errorln("daemon: unable to parse pool file")
		os.Exit(1) //nolint:gocritic // failing fast before we do any work.
	}

	cloudInitParams := &cloudinit.Params{
		LiteEnginePath: defaultPoolSettings.LiteEnginePath,
		CaCertFile:     defaultPoolSettings.CaCertFile,
		CertFile:       defaultPoolSettings.CertFile,
		KeyFile:        defaultPoolSettings.KeyFile,
	}

	pools, err := poolfile.MapPool(poolFile, &defaultPoolSettings, cloudInitParams)
	if err != nil {
		logrus.WithError(err).
			Errorln("daemon: unable to process pool file")
		os.Exit(1)
	}
	err = poolManager.Add(pools...)
	if err != nil {
		logrus.WithError(err).
			Errorln("daemon: unable to add to the pool")
		os.Exit(1)
	}

	if poolManager.Count() == 0 {
		logrus.Infoln("daemon: no instance pools found... aborting")
		os.Exit(1)
	}

	err = poolManager.CheckProvider(ctx)
	if err != nil {
		logrus.WithError(err).
			Errorln("daemon: cannot connect to cloud provider")
		return err
	}

	opts := engine.Opts{
		Repopulate: true,
	}

	engInstance, engineErr := engine.New(opts, poolManager, &defaultPoolSettings)
	if engineErr != nil {
		logrus.WithError(engineErr).
			Fatalln("daemon: cannot load the engine")
	}

	remoteInstance := remote.New(cli)
	tracer := history.New(remoteInstance)
	hook := loghistory.New()
	logrus.AddHook(hook)

	daemonLint := linter.New()
	daemonLint.PoolManager = poolManager
	runner := &runtime.Runner{
		Client:   cli,
		Machine:  env.Runner.Name,
		Reporter: tracer,
		Lookup:   resource.Lookup,
		Lint:     daemonLint.Lint,
		Match: match.Func(
			env.Limit.Repos,
			env.Limit.Events,
			env.Limit.Trusted,
		),
		Compiler: &compiler.Compiler{
			Environ: provider.Combine(
				provider.Static(env.Runner.Environ),
				provider.External(
					env.Environ.Endpoint,
					env.Environ.Token,
					env.Environ.SkipVerify,
				),
			),
			NetworkOpts: env.Runner.NetworkOpts,
			Secret: secret.Combine(
				secret.StaticVars(
					env.Runner.Secrets,
				),
				secret.External(
					env.Secret.Endpoint,
					env.Secret.Token,
					env.Secret.SkipVerify,
				),
			),
			PoolManager: poolManager,
			Registry: registry.Combine(
				registry.File(
					env.Docker.Config,
				),
				registry.External(
					env.Registry.Endpoint,
					env.Registry.Token,
					env.Registry.SkipVerify,
				),
			),
		},
		Exec: runtime.NewExecer(
			tracer,
			remoteInstance,
			pipeline.NopUploader(),
			engInstance,
			env.Runner.Procs,
		).Exec,
	}

	pollerInstance := &poller.Poller{
		Client:   cli,
		Dispatch: runner.Run,
		Filter: &client.Filter{
			Kind: resource.Kind,
			Type: resource.Type,
		},
	}

	var g errgroup.Group
	serverInstance := server.Server{
		Addr: env.Server.Port,
		Handler: router.New(tracer, hook, router.Config{
			Username: env.Dashboard.Username,
			Password: env.Dashboard.Password,
			Realm:    env.Dashboard.Realm,
		}),
	}

	logrus.WithField("addr", env.Server.Port).
		Infoln("daemon: starting the server")

	g.Go(func() error {
		return serverInstance.ListenAndServe(ctx)
	})

	// CheckProvider the server and block until a successful connection to the server has been established.
	for {
		pingErr := cli.Ping(ctx, env.Runner.Name)
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if ctx.Err() != nil {
			break
		}
		if pingErr != nil {
			logrus.WithError(pingErr).
				Errorln("daemon: cannot ping the remote drone server")
			time.Sleep(time.Second)
		} else {
			logrus.Infoln("daemon: successfully pinged the remote drone server")
			break
		}
	}

	g.Go(func() error {
		logrus.WithField("capacity", env.Runner.Capacity).
			WithField("endpoint", env.Client.Address).
			WithField("kind", resource.Kind).
			WithField("type", resource.Type).
			Infoln("daemon: polling the remote drone server")

		pollerInstance.Poll(ctx, env.Runner.Capacity)
		return nil
	})

	// if there is no keyfiles lets remove any old instances.
	if !env.DefaultPoolSettings.ReusePool {
		cleanErr := poolManager.CleanPools(ctx, true, true)
		if cleanErr != nil {
			logrus.WithError(cleanErr).
				Errorln("daemon: unable to clean pools")
		} else {
			logrus.Infoln("daemon: pools cleaned")
		}
	}

	err = poolManager.BuildPools(ctx)
	if err != nil {
		logrus.WithError(err).
			Errorln("daemon: unable to build pool")
		os.Exit(1)
	}
	logrus.Infoln("daemon: pool created")

	if !env.DefaultPoolSettings.ReusePool {
		g.Go(func() error {
			<-ctx.Done()
			// clean up pool on termination
			cleanErr := poolManager.CleanPools(context.Background(), true, true)
			if cleanErr != nil {
				logrus.WithError(cleanErr).
					Errorln("daemon: unable to clean pools")
			} else {
				logrus.Infoln("daemon: pools cleaned")
			}
			return cleanErr
		})
	}

	err = g.Wait()
	if err != nil {
		logrus.WithError(err).
			Errorln("daemon: shutting down the server")
	}
	return err
}

func SetupLogger(c *Config) {
	logger.Default = logger.Logrus(
		logrus.NewEntry(
			logrus.StandardLogger(),
		),
	)
	if c.Debug {
		logrus.SetLevel(logrus.DebugLevel)
	}
	if c.Trace {
		logrus.SetLevel(logrus.TraceLevel)
	}
}

// Register the daemon command.
func Register(app *kingpin.Application) {
	c := new(daemonCommand)

	cmd := app.Command("daemon", "starts the runner daemon").
		Default().
		Action(c.run)
	cmd.Flag("envfile", "load the environment variable file").
		Default("").
		StringVar(&c.envFile)
	cmd.Flag("pool", "file to seed the pool").
		Default(".drone_pool.yml").
		StringVar(&c.pool)
}
