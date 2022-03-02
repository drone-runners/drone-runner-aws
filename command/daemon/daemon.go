// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package daemon

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/drone-runners/drone-runner-aws/engine"
	"github.com/drone-runners/drone-runner-aws/engine/compiler"
	"github.com/drone-runners/drone-runner-aws/engine/linter"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/le"
	"github.com/drone-runners/drone-runner-aws/internal/match"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool/cloudaws"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool/google"

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
	envFile        string
	poolFile       string
	googlePoolFile string
}

func (c *daemonCommand) run(*kingpin.ParseContext) error {
	// load environment variables from file.
	err := godotenv.Load(c.envFile)
	if err != nil {
		return err
	}

	// load the configuration from the environment
	config, err := FromEnviron()
	if err != nil {
		return err
	}

	// TODO: These can be set in the config struct as `required: "true"` instead
	// once we have separate configs for the daemon and the delegate
	if config.Client.Host == "" {
		return errors.New("missing DRONE_RPC_HOST")
	}
	if config.Client.Secret == "" {
		return errors.New("missing DRONE_RPC_SECRET")
	}

	// setup the global logrus logger.
	SetupLogger(&config)

	ctx, cancel := context.WithCancel(nocontext)
	defer cancel()

	// listen for termination signals to gracefully shutdown
	// the runner daemon.
	ctx = signal.WithContextFunc(ctx, func() {
		println("daemon: received signal, terminating process")
		cancel()
	})

	cli := client.New(
		config.Client.Address,
		config.Client.Secret,
		config.Client.SkipVerify,
	)
	if config.Client.Dump {
		cli.Dumper = logger.StandardDumper(
			config.Client.DumpBody,
		)
	}
	cli.Logger = logger.Logrus(
		logrus.NewEntry(
			logrus.StandardLogger(),
		),
	)
	// generate le cert files if needed
	err = le.GenerateLECerts(config.Runner.Name, config.DefaultPoolSettings.CertificateFolder)
	if err != nil {
		logrus.WithError(err).
			Errorln("delegate: failed to generate certificates")
		return err
	}
	// read cert files into memory
	config.DefaultPoolSettings.CaCertFile, config.DefaultPoolSettings.CertFile, config.DefaultPoolSettings.KeyFile, err = le.ReadLECerts(config.DefaultPoolSettings.CertificateFolder)
	if err != nil {
		logrus.WithError(err).
			Errorln("daemon: failed to read certificates")
		return err
	}
	// we have enough information for default pool settings
	defaultPoolSettings := vmpool.DefaultSettings{
		RunnerName:          config.Runner.Name,
		AwsAccessKeyID:      config.DefaultPoolSettings.AwsAccessKeyID,
		AwsAccessKeySecret:  config.DefaultPoolSettings.AwsAccessKeySecret,
		AwsRegion:           config.DefaultPoolSettings.AwsRegion,
		AwsAvailabilityZone: config.DefaultPoolSettings.AwsAvailabilityZone,
		AwsKeyPairName:      config.DefaultPoolSettings.AwsKeyPairName,
		LiteEnginePath:      config.DefaultPoolSettings.LiteEnginePath,
		CaCertFile:          config.DefaultPoolSettings.CaCertFile,
		CertFile:            config.DefaultPoolSettings.CertFile,
		KeyFile:             config.DefaultPoolSettings.KeyFile,
	}

	poolManager := &vmpool.Manager{}
	poolManager.SetGlobalCtx(ctx)

	poolsAWS, err := cloudaws.ProcessPoolFile(c.poolFile, &defaultPoolSettings)
	if err != nil {
		logrus.WithError(err).
			Errorln("daemon: unable to parse aws pool file")
		os.Exit(1) //nolint:gocritic // failing fast before we do any work.
	}
	err = poolManager.Add(poolsAWS...)
	if err != nil {
		logrus.WithError(err).
			Errorln("daemon: unable to add to aws pools")
		os.Exit(1)
	}
	poolsGCP, err := google.ProcessPoolFile(c.googlePoolFile, &defaultPoolSettings)
	if err != nil {
		logrus.WithError(err).
			Errorln("daemon: unable to parse google pool file")
		os.Exit(1)
	}

	err = poolManager.Add(poolsGCP...)
	if err != nil {
		logrus.WithError(err).
			Errorln("daemon: unable to add to google pools")
		os.Exit(1)
	}

	if poolManager.Count() == 0 {
		logrus.Infoln("daemon: no instance pools found... aborting")
		os.Exit(1)
	}

	err = poolManager.Ping(ctx)
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
		Machine:  config.Runner.Name,
		Reporter: tracer,
		Lookup:   resource.Lookup,
		Lint:     daemonLint.Lint,
		Match: match.Func(
			config.Limit.Repos,
			config.Limit.Events,
			config.Limit.Trusted,
		),
		Compiler: &compiler.Compiler{
			Environ: provider.Combine(
				provider.Static(config.Runner.Environ),
				provider.External(
					config.Environ.Endpoint,
					config.Environ.Token,
					config.Environ.SkipVerify,
				),
			),
			Secret: secret.Combine(
				secret.StaticVars(
					config.Runner.Secrets,
				),
				secret.External(
					config.Secret.Endpoint,
					config.Secret.Token,
					config.Secret.SkipVerify,
				),
			),
			PoolManager: poolManager,
			Registry: registry.Combine(
				registry.File(
					config.Docker.Config,
				),
				registry.External(
					config.Registry.Endpoint,
					config.Registry.Token,
					config.Registry.SkipVerify,
				),
			),
		},
		Exec: runtime.NewExecer(
			tracer,
			remoteInstance,
			pipeline.NopUploader(),
			engInstance,
			config.Runner.Procs,
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
		Addr: config.Server.Port,
		Handler: router.New(tracer, hook, router.Config{
			Username: config.Dashboard.Username,
			Password: config.Dashboard.Password,
			Realm:    config.Dashboard.Realm,
		}),
	}

	logrus.WithField("addr", config.Server.Port).
		Infoln("daemon: starting the server")

	g.Go(func() error {
		return serverInstance.ListenAndServe(ctx)
	})

	// Ping the server and block until a successful connection to the server has been established.
	for {
		pingErr := cli.Ping(ctx, config.Runner.Name)
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
		logrus.WithField("capacity", config.Runner.Capacity).
			WithField("endpoint", config.Client.Address).
			WithField("kind", resource.Kind).
			WithField("type", resource.Type).
			Infoln("daemon: polling the remote drone server")

		pollerInstance.Poll(ctx, config.Runner.Capacity)
		return nil
	})

	// if there is no keyfiles lets remove any old instances.
	if !config.DefaultPoolSettings.ReusePool {
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

	if !config.DefaultPoolSettings.ReusePool {
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

// helper function configures the global logger from
// the loaded configuration.
func SetupLogger(config *Config) {
	logger.Default = logger.Logrus(
		logrus.NewEntry(
			logrus.StandardLogger(),
		),
	)
	if config.Debug {
		logrus.SetLevel(logrus.DebugLevel)
	}
	if config.Trace {
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
	cmd.Flag("poolfile", "file to seed the aws pool").
		Default(".drone_pool.yml").
		StringVar(&c.poolFile)
	cmd.Flag("pool_file_google", "file to seed the google pool").
		Default(".drone_pool_google.yml").
		StringVar(&c.googlePoolFile)
}
