package dlite

import (
	"context"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/store/database"
	loghistory "github.com/drone/runner-go/logger/history"
	"github.com/drone/signal"
	"github.com/wings-software/dlite/delegate"
	"github.com/wings-software/dlite/poller"
	"github.com/wings-software/dlite/router"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	taskInterval  = 3 * time.Second
	taskExecutors = 100
)

type dliteCommand struct {
	envFile         string
	env             config.EnvConfig
	delegateInfo    *poller.DelegateInfo
	poolFile        string
	poolManager     *drivers.Manager
	stageOwnerStore store.StageOwnerStore
}

func RegisterDlite(app *kingpin.Application) {
	c := new(dliteCommand)

	c.poolManager = &drivers.Manager{}

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
		t := pf.Instances[i].Platform.OS + "-" + pf.Instances[i].Platform.Arch
		tags = append(tags, t)
	}
	return tags
}

func (c *dliteCommand) startPoller(ctx context.Context, tags []string) error {
	r := router.NewRouter(routeMap(c))
	// Client to interact with the harness server
	client := delegate.New(c.env.Dlite.ManagerEndpoint, c.env.Dlite.AccountID, c.env.Dlite.AccountSecret, true)
	p := poller.New(c.env.Dlite.AccountID, c.env.Dlite.AccountSecret, c.env.Dlite.Name, tags, client, r)
	info, err := p.Register(ctx)
	if err != nil {
		return err
	}
	c.delegateInfo = info
	err = p.Poll(ctx, taskExecutors, info.ID, taskInterval)
	if err != nil {
		return err
	}
	return nil
}

func (c *dliteCommand) run(*kingpin.ParseContext) error {
	// load environment variables from file.
	envError := godotenv.Load(c.envFile)
	if envError != nil {
		logrus.WithError(envError).
			Warnf("dlite: failed to load environment variables from file: %s", c.envFile)
	}
	// load the configuration from the environment
	env, err := config.FromEnviron()
	if err != nil {
		return err
	}
	c.env = env
	// setup the global logrus logger.
	harness.SetupLogger(&c.env)
	db, err := database.ProvideDatabase(c.env.Database.Driver, c.env.Database.Datasource)
	if err != nil {
		logrus.WithError(err).Fatalln("Unable to start the database")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// listen for termination signals to gracefully shutdown the runner.
	ctx = signal.WithContextFunc(ctx, func() {
		println("received signal, terminating process")
		cancel()
	})

	instanceStore := database.ProvideInstanceStore(db)
	c.stageOwnerStore = database.NewStageOwnerStore(db)
	c.poolManager = drivers.New(ctx, instanceStore, c.env.Settings.LiteEnginePath, c.env.Runner.Name)

	poolConfig, err := harness.SetupPool(ctx, &c.env, c.poolManager, c.poolFile)
	if err != nil {
		return err
	}
	tags := parseTags(poolConfig)

	hook := loghistory.New()
	logrus.AddHook(hook)

	var g errgroup.Group
	g.Go(func() error {
		err = c.startPoller(ctx, tags)
		if err != nil {
			logrus.WithError(err).Error("could not start poller")
			return err
		}
		return nil
	})

	g.Go(func() error {
		return harness.Cleanup(ctx, &c.env, c.poolManager)
	})

	waitErr := g.Wait()
	if waitErr != nil {
		logrus.WithError(waitErr).
			Errorln("shutting down dlite")
		return waitErr
	}
	return nil
}
