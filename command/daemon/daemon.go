// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package daemon

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/drone-runners/drone-runner-aws/engine"
	"github.com/drone-runners/drone-runner-aws/engine/compiler"
	"github.com/drone-runners/drone-runner-aws/engine/linter"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/match"
	"github.com/drone-runners/drone-runner-aws/internal/platform"

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
	poolfile string
}

func (c *daemonCommand) run(*kingpin.ParseContext) error { //nolint:funlen,gocyclo // its complex but not too bad.
	// load environment variables from file.
	envErr := godotenv.Load(c.envfile)
	if envErr != nil {
		return envErr
	}

	// load the configuration from the environment
	config, err := fromEnviron()
	if err != nil {
		return err
	}

	// setup the global logrus logger.
	setupLogger(&config)

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
	compilerSettings := compiler.Settings{
		AwsAccessKeyID:     config.Settings.AwsAccessKeyID,
		AwsAccessKeySecret: config.Settings.AwsAccessKeySecret,
		AwsRegion:          config.Settings.AwsRegion,
		PrivateKeyFile:     config.Settings.PrivateKeyFile,
		PublicKeyFile:      config.Settings.PublicKeyFile,
	}

	comp := &compiler.Compiler{
		Environ:  provider.Static(make(map[string]string)),
		Settings: compilerSettings,
		Secret:   secret.Combine(secret.Combine()),
	}

	pools, poolFileErr := comp.ProcessPoolFile(ctx, c.poolfile, &compilerSettings)
	if poolFileErr != nil {
		logrus.WithError(poolFileErr).
			Errorln("daemon: unable to parse pool")
		os.Exit(1) //nolint:gocritic // failing fast before we do any work.
	}

	var awsMutex sync.Mutex
	opts := engine.Opts{
		AwsMutex:   &awsMutex,
		RunnerName: config.Runner.Name,
		Pools:      pools,
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

	runner := &runtime.Runner{
		Client:   cli,
		Machine:  config.Runner.Name,
		Reporter: tracer,
		Lookup:   resource.Lookup,
		Lint:     linter.New().Lint,
		Match: match.Func(
			config.Limit.Repos,
			config.Limit.Events,
			config.Limit.Trusted,
		),
		Compiler: &compiler.Compiler{
			Settings: compiler.Settings{
				AwsAccessKeyID:     config.Settings.AwsAccessKeyID,
				AwsAccessKeySecret: config.Settings.AwsAccessKeySecret,
				AwsRegion:          config.Settings.AwsRegion,
				PrivateKeyFile:     config.Settings.PrivateKeyFile,
				PublicKeyFile:      config.Settings.PublicKeyFile,
			},
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

	// Connect to AWS making sure we can use creds provided.
	if config.Settings.AwsAccessKeyID != "" || config.Settings.AwsAccessKeySecret != "" {
		for {
			pingErr := engInstance.Ping(ctx, config.Settings.AwsAccessKeyID, config.Settings.AwsAccessKeySecret, config.Settings.AwsRegion)
			if pingErr == context.Canceled {
				break
			}
			if pingErr != nil {
				logrus.WithError(pingErr).
					Errorln("daemon: cannot connect to aws")
				time.Sleep(time.Second)
			} else {
				logrus.Infoln("daemon: successfully connected to aws")
				break
			}
		}
	}

	creds := platform.Credentials{
		Client: config.Settings.AwsAccessKeyID,
		Secret: config.Settings.AwsAccessKeySecret,
		Region: config.Settings.AwsRegion,
	}
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
		cleanErr := platform.CleanPools(ctx, creds, config.Runner.Name)
		if cleanErr != nil {
			logrus.WithError(cleanErr).
				Errorln("daemon: unable to clean pools")
		} else {
			logrus.Infoln("daemon: pools cleaned")
		}
	}

	// seed a pool
	if pools != nil {
		buildPoolErr := buildPools(ctx, pools, engInstance, creds, &awsMutex)
		if buildPoolErr != nil {
			logrus.WithError(buildPoolErr).
				Errorln("daemon: unable to build pool")
			os.Exit(1)
		}
		logrus.Infoln("daemon: pool created")
	}

	g.Go(func() error {
		<-ctx.Done()
		// clean up pool on termination
		cleanErr := platform.CleanPools(ctx, creds, config.Runner.Name)
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

func buildPools(ctx context.Context, pools map[string]engine.Pool, eng *engine.Engine, creds platform.Credentials, awsMutex *sync.Mutex) error {
	for i := range pools {
		poolcount, _ := platform.PoolCountFree(ctx, creds, pools[i].Name, awsMutex)
		for poolcount < pools[i].MaxPoolSize {
			poolInstance := pools[i]
			id, ip, setupErr := eng.Provision(ctx, &poolInstance, false)
			if setupErr != nil {
				return setupErr
			}
			logrus.Infof("buildPools: created instance %s %s %s", pools[i].Name, id, ip)
			poolcount++
		}
	}
	return nil
}

// helper function configures the global logger from
// the loaded configuration.
func setupLogger(config *Config) {
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
		StringVar(&c.poolfile)
}
