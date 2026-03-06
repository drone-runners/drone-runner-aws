package delegate

import (
	loghistory "github.com/drone/runner-go/logger/history"
	"github.com/sirupsen/logrus"
	"gopkg.in/alecthomas/kingpin.v2"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/store/database"
)

// delegateCommand represents the delegate command configuration.
type delegateCommand struct {
	envFile  string
	poolFile string
}

// RegisterDelegate registers the delegate command with kingpin.
func RegisterDelegate(app *kingpin.Application) {
	c := new(delegateCommand)

	cmd := app.Command("delegate", "starts the delegate").
		Action(c.run)
	cmd.Flag("envfile", "load the environment variable file").
		StringVar(&c.envFile)
	cmd.Flag("pool", "file to seed the pool").
		StringVar(&c.poolFile)
}

// run executes the delegate command.
func (c *delegateCommand) run(*kingpin.ParseContext) error {
	// Create runner with delegate mode.
	runner := harness.NewRunner(
		harness.WithMode(harness.ModeDelegate),
		harness.WithPoolFile(c.poolFile),
	)

	// Load configuration.
	if err := runner.LoadConfig(c.envFile); err != nil {
		return err
	}

	// Initialize runner (context, signals, metrics).
	if err := runner.Initialize(); err != nil {
		return err
	}

	// Setup pools based on mode (standard or distributed).
	if err := c.setupPools(runner); err != nil {
		return err
	}

	// Setup log history hook.
	hook := loghistory.New()
	logrus.AddHook(hook)

	// Log startup info.
	logrus.WithField("addr", runner.Config.Server.Port).
		WithField("kind", resource.Kind).
		WithField("type", resource.Type).
		WithField("distributed_mode", runner.Config.Database.DistributedMode).
		Infoln("starting the server")

	// Create VM service and HTTP handlers.
	vmService := harness.NewVMServiceFromRunner(runner)
	handlers := harness.NewHTTPHandlers(vmService)

	// Run the server.
	return runner.Run(handlers.Router())
}

// setupPools initializes pools based on the distributed mode setting.
func (c *delegateCommand) setupPools(runner *harness.Runner) error {
	if runner.Config.Database.DistributedMode {
		return c.setupDistributedMode(runner)
	}
	return c.setupStandardMode(runner)
}

// setupStandardMode initializes the delegate in standard (non-distributed) mode.
func (c *delegateCommand) setupStandardMode(runner *harness.Runner) error {
	logrus.Infoln("delegate: starting in standard mode")

	instanceStore, stageOwnerStore, _, capacityReservationStore, _, err := database.ProvideStore(
		runner.Config.Database.Driver,
		runner.Config.Database.Datasource,
	)
	if err != nil {
		logrus.WithError(err).Fatalln("Unable to start the database")
		return err
	}

	runner.StageOwnerStore = stageOwnerStore
	runner.CapacityReservationStore = capacityReservationStore
	runner.PoolManager = drivers.New(runner.Context(), instanceStore, runner.Config)

	poolConfig, err := harness.SetupPoolWithEnv(runner.Context(), runner.Config, runner.PoolManager, c.poolFile)
	if err != nil {
		return err
	}
	runner.PoolConfig = poolConfig

	// Register standard metrics.
	runner.Metrics.AddMetricStore(&metric.Store{
		Store:       instanceStore,
		Query:       nil,
		Distributed: false,
	})

	return nil
}

// setupDistributedMode initializes the delegate in distributed mode.
func (c *delegateCommand) setupDistributedMode(runner *harness.Runner) error {
	logrus.Infoln("delegate: starting in distributed mode (using dlite internals)")

	result, err := harness.SetupDistributedMode(
		harness.DistributedSetupConfig{
			Ctx:      runner.Context(),
			Env:      runner.Config,
			PoolFile: c.poolFile,
			Metrics:  runner.Metrics,
		},
	)
	if err != nil {
		return err
	}

	runner.PoolManager = result.PoolManager
	runner.StageOwnerStore = result.StageOwnerStore
	runner.CapacityReservationStore = result.CapacityReservationStore
	runner.Scheduler = result.Scheduler
	runner.PoolConfig = result.PoolConfig

	// Register distributed metrics.
	harness.RegisterDistributedMetrics(runner.Context(), runner.Metrics, result, runner.Config.Runner.Name)

	return nil
}
