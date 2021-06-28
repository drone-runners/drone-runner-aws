// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package daemon

import (
	"context"
	"io/ioutil"
	"os"
	"sync"
	"time"

	"github.com/drone-runners/drone-runner-aws/engine"
	"github.com/drone-runners/drone-runner-aws/engine/compiler"
	"github.com/drone-runners/drone-runner-aws/engine/linter"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/match"
	"github.com/drone-runners/drone-runner-aws/internal/platform"

	"github.com/drone/drone-go/drone"
	"github.com/drone/runner-go/client"
	"github.com/drone/runner-go/environ/provider"
	"github.com/drone/runner-go/handler/router"
	"github.com/drone/runner-go/logger"
	loghistory "github.com/drone/runner-go/logger/history"
	"github.com/drone/runner-go/manifest"
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
	poolfile *os.File
}

func (c *daemonCommand) run(*kingpin.ParseContext) error {
	// load environment variables from file.
	godotenv.Load(c.envfile)

	// load the configuration from the environment
	config, err := fromEnviron()
	if err != nil {
		return err
	}

	// setup the global logrus logger.
	setupLogger(config)

	ctx, cancel := context.WithCancel(nocontext)
	defer cancel()

	// listen for termination signals to gracefully shutdown
	// the runner daemon.
	ctx = signal.WithContextFunc(ctx, func() {
		println("received signal, terminating process")
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
		logrus.Fatalln("specify a private key file and public key file or leave both settings empty to generate keys")
	}
	var awsMutex sync.Mutex
	var pools map[string]engine.Pool
	// read pool file, if it exists
	rawPool, readPoolFileErr := ioutil.ReadAll(c.poolfile)
	if readPoolFileErr == nil {
		logrus.Infoln("pool file exists")
		configSettings := compiler.Settings{
			AwsAccessKeyID:     config.Settings.AwsAccessKeyID,
			AwsAccessKeySecret: config.Settings.AwsAccessKeySecret,
			AwsRegion:          config.Settings.AwsRegion,
			PrivateKeyFile:     config.Settings.PrivateKeyFile,
			PublicKeyFile:      config.Settings.PublicKeyFile,
		}
		pools, err = processPoolFile(rawPool, configSettings)
		if err != nil {
			logrus.WithError(err).
				Errorln("unable to parse pool")
			os.Exit(1)
		}
	}

	opts := engine.Opts{
		AwsMutex:   &awsMutex,
		RunnerName: config.Runner.Name,
		Pools:      pools,
	}

	engine, err := engine.New(opts)
	if err != nil {
		logrus.WithError(err).
			Fatalln("cannot load the engine")
	}

	remote := remote.New(cli)
	tracer := history.New(remote)
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
			remote,
			engine,
			config.Runner.Procs,
		).Exec,
	}

	poller := &poller.Poller{
		Client:   cli,
		Dispatch: runner.Run,
		Filter: &client.Filter{
			Kind: resource.Kind,
			Type: resource.Type,
		},
	}

	var g errgroup.Group
	server := server.Server{
		Addr: config.Server.Port,
		Handler: router.New(tracer, hook, router.Config{
			Username: config.Dashboard.Username,
			Password: config.Dashboard.Password,
			Realm:    config.Dashboard.Realm,
		}),
	}

	logrus.WithField("addr", config.Server.Port).
		Infoln("starting the server")

	g.Go(func() error {
		return server.ListenAndServe(ctx)
	})

	// Connect to AWS making sure we can use creds provided.
	if config.Settings.AwsAccessKeyID != "" || config.Settings.AwsAccessKeySecret != "" {
		for {
			err := engine.Ping(ctx, config.Settings.AwsAccessKeyID, config.Settings.AwsAccessKeySecret, config.Settings.AwsRegion)
			if err == context.Canceled {
				break
			}
			if err != nil {
				logrus.WithError(err).
					Errorln("cannot connect to aws")
				time.Sleep(time.Second)
			} else {
				logrus.Infoln("successfully connected to aws")
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
		err := cli.Ping(ctx, config.Runner.Name)
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			logrus.WithError(err).
				Errorln("cannot ping the remote server")
			time.Sleep(time.Second)
		} else {
			logrus.Infoln("successfully pinged the remote server")
			break
		}
	}

	g.Go(func() error {
		logrus.WithField("capacity", config.Runner.Capacity).
			WithField("endpoint", config.Client.Address).
			WithField("kind", resource.Kind).
			WithField("type", resource.Type).
			Infoln("polling the remote server")

		poller.Poll(ctx, config.Runner.Capacity)
		return nil
	})

	// if there is no keyfiles lets remove any old instances.
	if !config.Settings.ReusePool {
		cleanErr := platform.CleanPools(ctx, creds)
		if cleanErr != nil {
			logrus.WithError(cleanErr).
				Errorln("unable to clean pools")
		} else {
			logrus.Infoln("pools cleaned")
		}
	}

	// seed a pool
	if pools != nil {
		buildPoolErr := buildPools(ctx, pools, engine, creds, &awsMutex)
		if buildPoolErr != nil {
			logrus.WithError(buildPoolErr).
				Errorln("unable to build pool")
			os.Exit(1)
		}
		logrus.Infoln("pool created")
	}

	g.Go(func() error {
		<-ctx.Done()
		// clean up pool on termination
		cleanErr := platform.CleanPools(ctx, creds)
		if cleanErr != nil {
			logrus.WithError(cleanErr).
				Errorln("unable to clean pools")
		} else {
			logrus.Infoln("pools cleaned")
		}
		return cleanErr
	})

	err = g.Wait()
	if err != nil {
		logrus.WithError(err).
			Errorln("shutting down the server")
	}
	return err
}

func processPoolFile(poolFile []byte, compilerSettings compiler.Settings) (pools map[string]engine.Pool, err error) {
	pools = make(map[string]engine.Pool)
	//evaluates string replacement expressions and returns an update configuration.
	config := string(poolFile)
	// parse and lint the configuration.
	manifest, err := manifest.ParseString(config)
	if err != nil {
		return pools, err
	}

	// this is where we need to iterate over the number of pipelines
	for _, res := range manifest.Resources {
		// lint the pipeline and return an error if any
		// linting rules are broken
		lint := linter.New()
		err = lint.Lint(res, &drone.Repo{Trusted: true})
		if err != nil {
			return pools, err
		}

		// compile the pipeline to an intermediate representation.
		comp := &compiler.Compiler{
			Environ:  provider.Static(make(map[string]string)),
			Settings: compilerSettings,
			Secret:   secret.Combine(secret.Combine()),
		}

		args := runtime.CompilerArgs{
			Pipeline: res,
			Manifest: manifest,
			System:   &drone.System{},
			Repo:     &drone.Repo{},
			Build:    &drone.Build{},
			Stage:    &drone.Stage{},
		}
		spec := comp.Compile(nocontext, args).(*engine.Spec)
		// include only steps that are in the include list, if the list in non-empty.
		pools[spec.PoolName] = engine.Pool{
			InstanceSpec: spec,
			PoolSize:     spec.PoolCount,
		}
	}
	return pools, nil
}

func buildPools(ctx context.Context, pools map[string]engine.Pool, eng *engine.Engine, creds platform.Credentials, awsMutex *sync.Mutex) error {
	for poolname, pool := range pools {
		poolcount, _ := platform.PoolCountFree(ctx, creds, poolname, awsMutex)
		for poolcount < pool.PoolSize {
			setupErr := eng.Setup(ctx, pool.InstanceSpec)
			if setupErr != nil {
				return setupErr
			}
			poolcount++
		}
	}
	return nil
}

// helper function configures the global logger from
// the loaded configuration.
func setupLogger(config Config) {
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
		FileVar(&c.poolfile)
}
