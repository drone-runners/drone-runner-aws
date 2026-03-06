package harness

import (
	"context"
	"fmt"
	"net/http"

	"github.com/drone/runner-go/server"
	"github.com/drone/signal"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/scheduler"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/store"
)

// RunnerMode defines the operating mode of the runner.
type RunnerMode string

const (
	// ModeDelegate runs in delegate mode (HTTP listener only).
	ModeDelegate RunnerMode = "delegate"
	// ModeDlite runs in dlite mode (polling + HTTP).
	ModeDlite RunnerMode = "dlite"
)

// RunnerConfig holds configuration for the runner.
type RunnerConfig struct {
	EnvFile  string
	PoolFile string
	Mode     RunnerMode
}

// Runner encapsulates the common infrastructure for both delegate and dlite modes.
type Runner struct {
	Config      *config.EnvConfig
	PoolManager drivers.IManager
	Scheduler   *scheduler.Scheduler
	Metrics     *metric.Metrics

	// Stores
	StageOwnerStore          store.StageOwnerStore
	CapacityReservationStore store.CapacityReservationStore

	// Pool config loaded during setup
	PoolConfig *config.PoolFile

	// Internal state
	ctx        context.Context
	cancel     context.CancelFunc
	mode       RunnerMode
	poolFile   string
	errGroup   *errgroup.Group
	httpServer *server.Server
}

// RunnerOption is a functional option for configuring the Runner.
type RunnerOption func(*Runner)

// WithMode sets the runner mode.
func WithMode(mode RunnerMode) RunnerOption {
	return func(r *Runner) {
		r.mode = mode
	}
}

// WithPoolFile sets the pool file path.
func WithPoolFile(path string) RunnerOption {
	return func(r *Runner) {
		r.poolFile = path
	}
}

// NewRunner creates a new Runner instance with the provided options.
func NewRunner(opts ...RunnerOption) *Runner {
	r := &Runner{
		mode: ModeDelegate,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// LoadConfig loads environment variables and configuration.
func (r *Runner) LoadConfig(envFile string) error {
	// Load environment variables from file if provided.
	if envFile != "" {
		if err := godotenv.Load(envFile); err != nil {
			logrus.WithError(err).
				Warnf("runner: failed to load environment variables from file: %s", envFile)
		}
	}

	// Load configuration from environment.
	env, err := config.FromEnviron()
	if err != nil {
		return fmt.Errorf("loading config from environment: %w", err)
	}

	// Apply default URIs if not set.
	r.applyDefaultURIs(&env)

	r.Config = &env
	return nil
}

// applyDefaultURIs sets default URIs for various binaries.
func (r *Runner) applyDefaultURIs(env *config.EnvConfig) {
	if env.Settings.HarnessTestBinaryURI == "" {
		env.Settings.HarnessTestBinaryURI = "https://app.harness.io/storage/harness-download/harness-ti/split_tests"
	}
	if env.Settings.AutoInjectionBinaryURI == "" {
		env.Settings.AutoInjectionBinaryURI = "https://app.harness.io/storage/harness-download/harness-ti/auto-injection/1.0.16"
	}
}

// Initialize sets up the runner context, signal handling, and metrics.
func (r *Runner) Initialize() error {
	if r.Config == nil {
		return fmt.Errorf("config not loaded; call LoadConfig first")
	}

	// Setup logger.
	SetupLogger(r.Config)

	// Create cancellable context.
	ctx, cancel := context.WithCancel(context.Background())
	r.ctx = ctx
	r.cancel = cancel

	// Setup signal handling.
	r.ctx = signal.WithContextFunc(r.ctx, func() {
		logrus.Infoln("received signal, terminating process")
		r.cancel()
	})

	// Initialize metrics.
	r.Metrics = metric.RegisterMetrics()

	return nil
}

// SetupPools initializes pool management based on the mode and configuration.
func (r *Runner) SetupPools() error {
	if r.Config == nil {
		return fmt.Errorf("config not loaded")
	}

	// Check if distributed mode is enabled.
	isDistributed := r.Config.Database.DistributedMode || r.mode == ModeDlite

	if isDistributed {
		return r.setupDistributedPools()
	}
	return r.setupStandardPools()
}

// setupDistributedPools initializes pools in distributed mode.
func (r *Runner) setupDistributedPools() error {
	logrus.Infoln("runner: starting in distributed mode")

	result, err := SetupDistributedMode(DistributedSetupConfig{
		Ctx:      r.ctx,
		Env:      r.Config,
		PoolFile: r.poolFile,
		Metrics:  r.Metrics,
	})
	if err != nil {
		return fmt.Errorf("setting up distributed mode: %w", err)
	}

	r.PoolManager = result.PoolManager
	r.StageOwnerStore = result.StageOwnerStore
	r.CapacityReservationStore = result.CapacityReservationStore
	r.Scheduler = result.Scheduler
	r.PoolConfig = result.PoolConfig

	// Register distributed metrics.
	RegisterDistributedMetrics(r.ctx, r.Metrics, result, r.Config.Runner.Name)

	return nil
}

// setupStandardPools initializes pools in standard (non-distributed) mode.
func (r *Runner) setupStandardPools() error {
	logrus.Infoln("runner: starting in standard mode")

	stores, err := r.provideStore()
	if err != nil {
		return fmt.Errorf("initializing database: %w", err)
	}

	r.StageOwnerStore = stores.StageOwnerStore
	r.CapacityReservationStore = stores.CapacityReservationStore
	r.PoolManager = drivers.New(r.ctx, stores.InstanceStore, r.Config)

	poolConfig, err := SetupPoolWithEnv(r.ctx, r.Config, r.PoolManager, r.poolFile)
	if err != nil {
		return fmt.Errorf("setting up pool: %w", err)
	}
	r.PoolConfig = poolConfig

	// Register standard metrics.
	r.Metrics.AddMetricStore(&metric.Store{
		Store:       stores.InstanceStore,
		Query:       nil,
		Distributed: false,
	})

	return nil
}

// storeResult holds the stores returned by provideStore.
type storeResult struct {
	InstanceStore            store.InstanceStore
	StageOwnerStore          store.StageOwnerStore
	OutboxStore              store.OutboxStore
	CapacityReservationStore store.CapacityReservationStore
	UtilizationHistoryStore  store.UtilizationHistoryStore
}

// provideStore creates database stores based on configuration.
func (r *Runner) provideStore() (*storeResult, error) {
	return nil, fmt.Errorf("use database.ProvideStore directly for standard mode")
}

// StartScheduler starts the scheduler if it was initialized.
func (r *Runner) StartScheduler() {
	if r.Scheduler != nil {
		r.Scheduler.Start()
		logrus.Infoln("runner: scheduler started")
	}
}

// StopScheduler stops the scheduler gracefully.
func (r *Runner) StopScheduler() {
	if r.Scheduler != nil {
		r.Scheduler.Stop()
		logrus.Infoln("runner: scheduler stopped")
	}
}

// StartHTTPServer starts the HTTP server with the provided handler.
func (r *Runner) StartHTTPServer(handler http.Handler) error {
	r.httpServer = &server.Server{
		Addr:    r.Config.Server.Port,
		Handler: handler,
	}

	logrus.WithField("addr", r.httpServer.Addr).
		WithField("mode", r.mode).
		Infoln("starting the server")

	return r.httpServer.ListenAndServe(r.ctx)
}

// Run starts the runner with the provided handler and blocks until shutdown.
func (r *Runner) Run(handler http.Handler) error {
	r.updateMetrics()
	r.StartScheduler()

	g, _ := errgroup.WithContext(r.ctx)
	r.errGroup = g

	// Cleanup on context cancellation.
	g.Go(func() error {
		<-r.ctx.Done()
		return r.cleanup()
	})

	// Stop scheduler on context cancellation.
	g.Go(func() error {
		<-r.ctx.Done()
		r.StopScheduler()
		return nil
	})

	// Start HTTP server.
	g.Go(func() error {
		return r.StartHTTPServer(handler)
	})

	return g.Wait()
}

// RunWithPolling starts the runner with both HTTP server and a custom polling function.
func (r *Runner) RunWithPolling(handler http.Handler, pollFunc func() error) error {
	r.updateMetrics()
	r.StartScheduler()

	g, _ := errgroup.WithContext(r.ctx)
	r.errGroup = g

	// Cleanup on context cancellation.
	g.Go(func() error {
		<-r.ctx.Done()
		return r.cleanup()
	})

	// Stop scheduler on context cancellation.
	g.Go(func() error {
		<-r.ctx.Done()
		r.StopScheduler()
		return nil
	})

	// Start HTTP server.
	g.Go(func() error {
		return r.StartHTTPServer(handler)
	})

	// Start polling.
	g.Go(pollFunc)

	return g.Wait()
}

// updateMetrics updates the running and warm pool counts.
func (r *Runner) updateMetrics() {
	r.Metrics.UpdateRunningCount(r.ctx)
	if r.IsDistributed() {
		r.Metrics.UpdateWarmPoolCount(r.ctx)
	}
}

// cleanup performs cleanup operations on shutdown.
func (r *Runner) cleanup() error {
	shouldCleanBusyVMs := true
	if r.PoolManager != nil && r.PoolManager.GetRunnerConfig().HA {
		shouldCleanBusyVMs = false
	}
	return Cleanup(r.Config.Settings.ReusePool, r.PoolManager, shouldCleanBusyVMs, true)
}

// Context returns the runner's context.
func (r *Runner) Context() context.Context {
	return r.ctx
}

// Cancel cancels the runner's context, initiating shutdown.
func (r *Runner) Cancel() {
	if r.cancel != nil {
		r.cancel()
	}
}

// IsDistributed returns whether the runner is operating in distributed mode.
func (r *Runner) IsDistributed() bool {
	return r.Config.Database.DistributedMode || r.mode == ModeDlite
}

// GetPoolTags extracts pool names as tags for poller registration.
func (r *Runner) GetPoolTags() []string {
	if r.PoolConfig == nil {
		return nil
	}
	tags := make([]string, 0, len(r.PoolConfig.Instances))
	for i := range r.PoolConfig.Instances {
		tags = append(tags, r.PoolConfig.Instances[i].Name)
	}
	return tags
}
