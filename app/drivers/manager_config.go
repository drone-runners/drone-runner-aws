package drivers

import (
	"context"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

// ManagerConfig holds all configuration needed to create a Manager.
type ManagerConfig struct {
	// Context
	GlobalCtx context.Context

	// Stores
	InstanceStore            store.InstanceStore
	StageOwnerStore          store.StageOwnerStore
	CapacityReservationStore store.CapacityReservationStore
	FirewallStore            store.FirewallStore

	// Runner configuration
	RunnerName   string
	RunnerConfig types.RunnerConfig
	Tmate        types.Tmate
	Env          string

	// LiteEngine paths
	LiteEnginePath         string
	LiteEngineFallbackPath string

	// TPA endpoint for egress-control pools.
	TPAAddress string
	TPAPort    string

	// Binary URIs
	HarnessTestBinaryURI         string
	PluginBinaryURI              string
	PluginBinaryFallbackURI      string
	AutoInjectionBinaryURI       string
	AnnotationsBinaryURI         string
	AnnotationsBinaryFallbackURI string
	EnvmanBinaryURI              string
	EnvmanBinaryFallbackURI      string
	TmateBinaryURI               string
	TmateBinaryFallbackURI       string
	Hosted                       bool
	EnableLEDiagnostics          bool
	CapacityReservationTTL       int64 // seconds; GCP auto-deletes reservations after this duration
}

// NewManagerFromConfig creates a new Manager from a ManagerConfig.
func NewManagerFromConfig(cfg *ManagerConfig) *Manager {
	return &Manager{
		globalCtx:                    cfg.GlobalCtx,
		instanceStore:                cfg.InstanceStore,
		stageOwnerStore:              cfg.StageOwnerStore,
		capacityReservationStore:     cfg.CapacityReservationStore,
		firewallStore:                cfg.FirewallStore,
		runnerName:                   cfg.RunnerName,
		runnerConfig:                 cfg.RunnerConfig,
		tmate:                        cfg.Tmate,
		env:                          cfg.Env,
		liteEnginePath:               cfg.LiteEnginePath,
		liteEngineFallbackPath:       cfg.LiteEngineFallbackPath,
		tpaAddress:                   cfg.TPAAddress,
		tpaPort:                      cfg.TPAPort,
		harnessTestBinaryURI:         cfg.HarnessTestBinaryURI,
		pluginBinaryURI:              cfg.PluginBinaryURI,
		pluginBinaryFallbackURI:      cfg.PluginBinaryFallbackURI,
		autoInjectionBinaryURI:       cfg.AutoInjectionBinaryURI,
		annotationsBinaryURI:         cfg.AnnotationsBinaryURI,
		annotationsBinaryFallbackURI: cfg.AnnotationsBinaryFallbackURI,
		envmanBinaryURI:              cfg.EnvmanBinaryURI,
		envmanBinaryFallbackURI:      cfg.EnvmanBinaryFallbackURI,
		tmateBinaryURI:               cfg.TmateBinaryURI,
		tmateBinaryFallbackURI:       cfg.TmateBinaryFallbackURI,
		hosted:                       cfg.Hosted,
		enableLEDiagnostics:          cfg.EnableLEDiagnostics,
		capacityReservationTTL:       cfg.CapacityReservationTTL,
	}
}

// NewManagerConfigFromEnv creates a ManagerConfig from an EnvConfig.
// This is a convenience function for creating ManagerConfig from environment configuration.
func NewManagerConfigFromEnv(ctx context.Context, instanceStore store.InstanceStore, envConfig *config.EnvConfig) ManagerConfig {
	return ManagerConfig{
		GlobalCtx:                    ctx,
		InstanceStore:                instanceStore,
		RunnerName:                   envConfig.Runner.Name,
		RunnerConfig:                 types.RunnerConfig(envConfig.RunnerConfig),
		Tmate:                        types.Tmate(envConfig.Tmate),
		Env:                          envConfig.Settings.Env,
		LiteEnginePath:               envConfig.LiteEngine.Path,
		LiteEngineFallbackPath:       envConfig.LiteEngine.FallbackPath,
		TPAAddress:                   envConfig.TPA.Address,
		TPAPort:                      envConfig.TPA.Port,
		HarnessTestBinaryURI:         envConfig.Settings.HarnessTestBinaryURI,
		PluginBinaryURI:              envConfig.Settings.PluginBinaryURI,
		PluginBinaryFallbackURI:      envConfig.Settings.PluginBinaryFallbackURI,
		AutoInjectionBinaryURI:       envConfig.Settings.AutoInjectionBinaryURI,
		AnnotationsBinaryURI:         envConfig.Settings.AnnotationsBinaryURI,
		AnnotationsBinaryFallbackURI: envConfig.Settings.AnnotationsBinaryFallbackURI,
		EnvmanBinaryURI:              envConfig.Settings.EnvmanBinaryURI,
		EnvmanBinaryFallbackURI:      envConfig.Settings.EnvmanBinaryFallbackURI,
		TmateBinaryURI:               envConfig.Settings.TmateBinaryURI,
		TmateBinaryFallbackURI:       envConfig.Settings.TmateBinaryFallbackURI,
		EnableLEDiagnostics:          envConfig.Settings.EnableLEDiagnostics,
		CapacityReservationTTL:       envConfig.Settings.FreeCapacityMaxAgeMinutes * 60,
	}
}

// ManagerOption is a functional option for configuring a Manager.
type ManagerOption func(*Manager)

// WithGlobalContext sets the global context.
func WithGlobalContext(ctx context.Context) ManagerOption {
	return func(m *Manager) {
		m.globalCtx = ctx
	}
}

// WithInstanceStore sets the instance store.
func WithInstanceStore(s store.InstanceStore) ManagerOption {
	return func(m *Manager) {
		m.instanceStore = s
	}
}

// WithStageOwnerStore sets the stage owner store.
func WithStageOwnerStore(s store.StageOwnerStore) ManagerOption {
	return func(m *Manager) {
		m.stageOwnerStore = s
	}
}

// WithCapacityReservationStore sets the capacity reservation store.
func WithCapacityReservationStore(s store.CapacityReservationStore) ManagerOption {
	return func(m *Manager) {
		m.capacityReservationStore = s
	}
}

// WithFirewallStore sets the firewall store.
func WithFirewallStore(s store.FirewallStore) ManagerOption {
	return func(m *Manager) {
		m.firewallStore = s
	}
}

// WithRunnerName sets the runner name.
func WithRunnerName(name string) ManagerOption {
	return func(m *Manager) {
		m.runnerName = name
	}
}

// WithRunnerConfig sets the runner configuration.
func WithRunnerConfig(cfg types.RunnerConfig) ManagerOption {
	return func(m *Manager) {
		m.runnerConfig = cfg
	}
}

// WithTmate sets the tmate configuration.
func WithTmate(t *types.Tmate) ManagerOption {
	return func(m *Manager) {
		m.tmate = *t
	}
}

// WithEnv sets the environment.
func WithEnv(env string) ManagerOption {
	return func(m *Manager) {
		m.env = env
	}
}

// WithLiteEnginePaths sets the lite engine paths.
func WithLiteEnginePaths(path, fallbackPath string) ManagerOption {
	return func(m *Manager) {
		m.liteEnginePath = path
		m.liteEngineFallbackPath = fallbackPath
	}
}

// WithBinaryURIs sets all binary URIs at once.
func WithBinaryURIs(
	harnessTestURI,
	pluginURI, pluginFallbackURI,
	autoInjectionURI,
	annotationsURI, annotationsFallbackURI,
	envmanURI, envmanFallbackURI,
	tmateURI, tmateFallbackURI string,
) ManagerOption {
	return func(m *Manager) {
		m.harnessTestBinaryURI = harnessTestURI
		m.pluginBinaryURI = pluginURI
		m.pluginBinaryFallbackURI = pluginFallbackURI
		m.autoInjectionBinaryURI = autoInjectionURI
		m.annotationsBinaryURI = annotationsURI
		m.annotationsBinaryFallbackURI = annotationsFallbackURI
		m.envmanBinaryURI = envmanURI
		m.envmanBinaryFallbackURI = envmanFallbackURI
		m.tmateBinaryURI = tmateURI
		m.tmateBinaryFallbackURI = tmateFallbackURI
	}
}

// WithStrategy sets the provisioning strategy.
func WithStrategy(s Strategy) ManagerOption {
	return func(m *Manager) {
		m.strategy = s
	}
}

// NewManagerWithOptions creates a Manager using functional options.
func NewManagerWithOptions(opts ...ManagerOption) *Manager {
	m := &Manager{}
	for _, opt := range opts {
		opt(m)
	}
	return m
}
