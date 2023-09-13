package dlite

import (
	"context"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/store/database"
	"github.com/drone-runners/drone-runner-aws/types"
	loghistory "github.com/drone/runner-go/logger/history"
	"github.com/drone/runner-go/server"
	"github.com/drone/signal"
	"github.com/wings-software/dlite/delegate"
	"github.com/wings-software/dlite/poller"
	"github.com/wings-software/dlite/router"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"gopkg.in/alecthomas/kingpin.v2"
)

type dliteCommand struct {
	envFile                string
	env                    config.EnvConfig
	delegateInfo           *poller.DelegateInfo
	poolFile               string
	poolManager            drivers.IManager
	distributedPoolManager drivers.IManager
	metrics                *metric.Metrics
}

func RegisterDlite(app *kingpin.Application) {
	c := new(dliteCommand)

	c.poolManager = &drivers.Manager{}
	c.distributedPoolManager = &drivers.DistributedManager{}

	cmd := app.Command("dlite", "starts the runner with polling enabled for accepting tasks").
		Action(c.run)
	cmd.Flag("envfile", "load the environment variable file").
		StringVar(&c.envFile)
	cmd.Flag("pool", "file to seed the pool").
		StringVar(&c.poolFile)
}

// Iterate over the list of pools and register the tags for the pools it supports
func parseTags(pf *config.PoolFile) []string {
	tags := []string{}
	for i := range pf.Instances {
		tags = append(tags, pf.Instances[i].Name)
	}
	return tags
}

// register metrics
func (c *dliteCommand) registerMetrics() {
	c.metrics = metric.RegisterMetrics()
}

func (c *dliteCommand) registerPoller(ctx context.Context, tags []string) (*poller.Poller, error) {
	r := router.NewRouter(routeMap(c))
	// Client to interact with the harness server
	// Additional certs are not needed to be passed in case of hosted
	client := delegate.New(c.env.Dlite.ManagerEndpoint, c.env.Dlite.AccountID, c.env.Dlite.AccountSecret, true, "")
	p := poller.New(c.env.Dlite.AccountID, c.env.Dlite.AccountSecret, c.env.Dlite.Name, tags, client, r)
	info, err := p.Register(ctx)
	if err != nil {
		return nil, err
	}
	c.delegateInfo = info
	return p, nil
}

func (c *dliteCommand) run(*kingpin.ParseContext) error {
	// load environment variables from file.
	if c.envFile != "" {
		envError := godotenv.Load(c.envFile)
		if envError != nil {
			logrus.WithError(envError).
				Warnf("dlite: failed to load environment variables from file: %s", c.envFile)
		}
	}
	// load the configuration from the environment
	env, err := config.FromEnviron()
	if err != nil {
		return err
	}
	if env.Settings.HarnessTestBinaryURI == "" {
		env.Settings.HarnessTestBinaryURI = "https://app.harness.io/storage/harness-download/harness-ti/split_tests"
	}
	c.env = env
	// setup the global logrus logger.
	harness.SetupLogger(&c.env)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// listen for termination signals to gracefully shutdown the runner.
	ctx = signal.WithContextFunc(ctx, func() {
		println("received signal, terminating process")
		cancel()
	})

	// Initialize metrics
	c.registerMetrics()

	poolConfig, err := c.setupPool(ctx)
	defer harness.Cleanup(&c.env, c.poolManager, true, true) //nolint: errcheck
	if err != nil {
		return err
	}

	if env.Postgres.Enabled {
		_, err = c.setupDistributedPool(ctx)
		defer harness.Cleanup(&c.env, c.distributedPoolManager, false, true) //nolint: errcheck
		if err != nil {
			return err
		}
	}

	// Update running count from all the stores
	c.metrics.UpdateRunningCount(ctx)

	tags := parseTags(poolConfig)

	hook := loghistory.New()
	logrus.AddHook(hook)

	// Register the poller
	p, err := c.registerPoller(ctx, tags)
	if err != nil {
		logrus.WithError(err).Error("could not register poller")
		return err
	}

	var g errgroup.Group

	g.Go(func() error {
		<-ctx.Done()
		err = harness.Cleanup(&c.env, c.poolManager, true, true)
		// only delete unused instances for distributed pool
		if derr := harness.Cleanup(&c.env, c.distributedPoolManager, false, true); derr != nil {
			err = derr
		}
		return err
	})

	g.Go(func() error {
		// Start the HTTP server
		s := server.Server{
			Addr:    c.env.Server.Port,
			Handler: Handler(p, c),
		}

		logrus.WithField("addr", s.Addr).
			Infoln("starting the server")

		return s.ListenAndServe(ctx)
	})

	g.Go(func() error {
		// Start the poller
		pollDuration := time.Duration(c.env.Dlite.PollIntervalMilliSecs) * time.Millisecond
		err = p.Poll(ctx, c.env.Dlite.ParallelWorkers, c.delegateInfo.ID, pollDuration)
		if err != nil {
			return err
		}
		return nil
	})

	waitErr := g.Wait()
	if waitErr != nil {
		logrus.WithError(waitErr).
			Errorln("shutting down dlite")
		return waitErr
	}
	return nil
}

func (c *dliteCommand) setupPool(ctx context.Context) (*config.PoolFile, error) {
	instanceStore, stageOwnerStore, err := database.ProvideStore(c.env.Database.Driver, c.env.Database.Datasource)
	if err != nil {
		logrus.WithError(err).Fatalln("Unable to start the database")
	}
	c.poolManager = drivers.NewManager(ctx, instanceStore, stageOwnerStore, &c.env)
	poolConfig, err := harness.SetupPool(ctx, &c.env, c.poolManager, c.poolFile)
	if err != nil {
		logrus.WithError(err).Error("could not setup pool")
		return poolConfig, err
	}
	c.metrics.AddMetricStore(&metric.MetricStore{
		Store:       instanceStore,
		Query:       nil,
		Distributed: false,
	})
	return poolConfig, nil
}

func (c *dliteCommand) setupDistributedPool(ctx context.Context) (*config.PoolFile, error) {
	instanceStore, stageOwnerStore, err := database.ProvideStore(c.env.Postgres.Driver, c.env.Postgres.Datasource)
	if err != nil {
		logrus.WithError(err).Fatalln("Unable to start the database")
		return nil, err
	}
	c.distributedPoolManager = drivers.NewDistributedManager(drivers.NewManager(ctx, instanceStore, stageOwnerStore, &c.env))
	poolConfig, err := harness.SetupPool(ctx, &c.env, c.distributedPoolManager, c.poolFile)
	if err != nil {
		logrus.WithError(err).Error("could not setup distributed pool")
		return poolConfig, err
	}
	c.metrics.AddMetricStore(&metric.MetricStore{
		Store: instanceStore,
		Query: &types.QueryParams{
			RunnerName: c.env.Runner.Name,
		},
		Distributed: true,
	})
	return poolConfig, nil
}

func (c *dliteCommand) getPoolManager(distributed bool) drivers.IManager {
	if distributed {
		return c.distributedPoolManager
	}
	return c.poolManager
}
