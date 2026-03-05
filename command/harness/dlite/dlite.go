package dlite

import (
	"context"
	"time"

	loghistory "github.com/drone/runner-go/logger/history"
	"github.com/sirupsen/logrus"
	"github.com/wings-software/dlite/delegate"
	"github.com/wings-software/dlite/poller"
	"github.com/wings-software/dlite/router"
	"gopkg.in/alecthomas/kingpin.v2"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/drone-runners/drone-runner-aws/types"
)

// dliteCommand represents the dlite command configuration.
type dliteCommand struct {
	envFile  string
	poolFile string

	// Runtime state
	runner       *harness.Runner
	vmService    *harness.VMService
	delegateInfo *poller.DelegateInfo
	poller       *poller.Poller
}

// RegisterDlite registers the dlite command with kingpin.
func RegisterDlite(app *kingpin.Application) {
	c := new(dliteCommand)

	cmd := app.Command("dlite", "starts the runner with polling enabled for accepting tasks").
		Action(c.run)
	cmd.Flag("envfile", "load the environment variable file").
		StringVar(&c.envFile)
	cmd.Flag("pool", "file to seed the pool").
		StringVar(&c.poolFile)
}

// run executes the dlite command.
func (c *dliteCommand) run(*kingpin.ParseContext) error {
	// Create runner with dlite mode.
	c.runner = harness.NewRunner(
		harness.WithMode(harness.ModeDlite),
		harness.WithPoolFile(c.poolFile),
	)

	// Load configuration.
	if err := c.runner.LoadConfig(c.envFile); err != nil {
		return err
	}

	// Initialize runner (context, signals, metrics).
	if err := c.runner.Initialize(); err != nil {
		return err
	}

	// Set hosted context value.
	ctx := context.WithValue(c.runner.Context(), types.Hosted, true)

	// Setup distributed pools (dlite always uses distributed mode).
	if err := c.setupDistributedPool(ctx); err != nil {
		return err
	}

	// Create VM service for handlers.
	c.vmService = harness.NewVMServiceFromRunner(c.runner)

	// Setup log history hook.
	hook := loghistory.New()
	logrus.AddHook(hook)

	// Get pool tags for poller registration.
	tags := c.runner.GetPoolTags()

	// Register the poller.
	if err := c.registerPoller(ctx, tags); err != nil {
		logrus.WithError(err).Error("could not register poller")
		return err
	}

	// Create HTTP handler.
	httpHandler := Handler(c.poller, c)

	// Run with both HTTP server and polling.
	return c.runner.RunWithPolling(httpHandler, c.pollFunc)
}

// setupDistributedPool initializes distributed pool management.
func (c *dliteCommand) setupDistributedPool(ctx context.Context) error {
	result, err := harness.SetupDistributedMode(
		harness.DistributedSetupConfig{
			Ctx:      ctx,
			Env:      c.runner.Config,
			PoolFile: c.poolFile,
			Metrics:  c.runner.Metrics,
		},
	)
	if err != nil {
		return err
	}

	c.runner.PoolManager = result.PoolManager
	c.runner.StageOwnerStore = result.StageOwnerStore
	c.runner.CapacityReservationStore = result.CapacityReservationStore
	c.runner.Scheduler = result.Scheduler
	c.runner.PoolConfig = result.PoolConfig

	// Register metrics.
	harness.RegisterDistributedMetrics(ctx, c.runner.Metrics, result, c.runner.Config.Runner.Name)

	return nil
}

// registerPoller creates and registers the poller with the manager.
func (c *dliteCommand) registerPoller(ctx context.Context, tags []string) error {
	r := router.NewRouter(routeMap(c))

	// Client to interact with the harness server.
	managerEndpoint := c.runner.Config.Dlite.ManagerEndpoint
	if c.runner.Config.Dlite.InternalManagerEndpoint != "" {
		managerEndpoint = c.runner.Config.Dlite.InternalManagerEndpoint
	}

	client := delegate.New(
		managerEndpoint,
		c.runner.Config.Dlite.AccountID,
		c.runner.Config.Dlite.AccountSecret,
		true,
		"",
	)

	c.poller = poller.New(
		c.runner.Config.Dlite.AccountID,
		c.runner.Config.Dlite.AccountSecret,
		c.runner.Config.Dlite.Name,
		tags,
		client,
		r,
	)

	info, err := c.poller.Register(ctx)
	if err != nil {
		return err
	}
	c.delegateInfo = info

	return nil
}

// pollFunc is the polling function passed to RunWithPolling.
func (c *dliteCommand) pollFunc() error {
	pollDuration := time.Duration(c.runner.Config.Dlite.PollIntervalMilliSecs) * time.Millisecond
	return c.poller.Poll(
		c.runner.Context(),
		c.runner.Config.Dlite.ParallelWorkers,
		c.delegateInfo.ID,
		pollDuration,
	)
}

// getPoolManager returns the appropriate pool manager based on distributed flag.
func (c *dliteCommand) getPoolManager(distributed bool) drivers.IManager {
	// In dlite mode, we always use the distributed pool manager.
	return c.runner.PoolManager
}

// getVMService returns the VM service.
func (c *dliteCommand) getVMService() *harness.VMService {
	return c.vmService
}

// getDelegateInfo returns the delegate info.
func (c *dliteCommand) getDelegateInfo() *poller.DelegateInfo {
	return c.delegateInfo
}

// getConfig returns the environment configuration.
func (c *dliteCommand) getConfig() interface{} {
	return c.runner.Config
}

// getMetrics returns the metrics.
func (c *dliteCommand) getMetrics() interface{} {
	return c.runner.Metrics
}
