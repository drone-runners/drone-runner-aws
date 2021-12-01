// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package daemon

import (
	"context"
	"os"
	"time"

	"github.com/drone-runners/drone-runner-aws/engine"
	"github.com/drone-runners/drone-runner-aws/engine/compiler"
	"github.com/drone-runners/drone-runner-aws/engine/linter"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/match"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool/cloudaws"

	"github.com/drone/runner-go/client"
	"github.com/drone/runner-go/environ/provider"
	"github.com/drone/runner-go/handler/router"
	"github.com/drone/runner-go/logger"
	loghistory "github.com/drone/runner-go/logger/history"
	"github.com/drone/runner-go/pipeline/reporter/history"
	"github.com/drone/runner-go/pipeline/reporter/remote"
	"github.com/drone/runner-go/pipeline/runtime"
	"github.com/drone/runner-go/poller"
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
	envfile  string
	Poolfile string
}

func (c *daemonCommand) run(*kingpin.ParseContext) error { //nolint:funlen,gocyclo // its complex but not too bad.
	// load environment variables from file.
	envErr := godotenv.Load(c.envfile)
	if envErr != nil {
		return envErr
	}

	// load the configuration from the environment
	config, err := FromEnviron()
	if err != nil {
		return err
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

	if (config.Settings.PrivateKeyFile != "" && config.Settings.PublicKeyFile == "") || (config.Settings.PrivateKeyFile == "" && config.Settings.PublicKeyFile != "") {
		logrus.Fatalln("daemon: specify a private key file and public key file or leave both settings empty to generate keys")
	}

	awsAccessSettings := &cloudaws.AccessSettings{
		AccessKey:      config.Settings.AwsAccessKeyID,
		AccessSecret:   config.Settings.AwsAccessKeySecret,
		Region:         config.Settings.AwsRegion,
		PrivateKeyFile: config.Settings.PrivateKeyFile,
		PublicKeyFile:  config.Settings.PublicKeyFile,
	}

	pools, poolFileErr := cloudaws.ProcessPoolFile(c.Poolfile, awsAccessSettings, config.Runner.Name)
	if poolFileErr != nil {
		logrus.WithError(poolFileErr).
			Errorln("daemon: unable to parse pool file")
		os.Exit(1) //nolint:gocritic // failing fast before we do any work.
	}

	poolManager := &vmpool.Manager{}
	err = poolManager.Add(pools...)
	if err != nil {
		return err
	}

	err = poolManager.Ping(ctx)
	if err != nil {
		logrus.WithError(err).
			Errorln("daemon: cannot connect to cloud provider")
		return err
	}

	opts := engine.Opts{
		PoolManager: poolManager,
		Repopulate:  true,
	}

	engInstance, engineErr := engine.New(opts)
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
		},
		Exec: runtime.NewExecer(
			tracer,
			remoteInstance,
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
	if !config.Settings.ReusePool {
		cleanErr := poolManager.CleanPools(ctx)
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

	g.Go(func() error {
		<-ctx.Done()
		// clean up pool on termination
		cleanErr := poolManager.CleanPools(ctx)
		if cleanErr != nil {
			logrus.WithError(cleanErr).
				Errorln("daemon: unable to clean pools")
		} else {
			logrus.Infoln("daemon: pools cleaned")
		}
		return cleanErr
	})

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
	cmd.Arg("envfile", "load the environment variable file").
		Default("").
		StringVar(&c.envfile)
	cmd.Arg("poolfile", "file to seed the aws pool").
		Default(".drone_pool.yml").
		StringVar(&c.Poolfile)
}
