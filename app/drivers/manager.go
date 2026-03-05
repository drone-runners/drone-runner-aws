package drivers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

var _ IManager = (*Manager)(nil)

type (
	// Manager manages VM pools and instances.
	Manager struct {
		globalCtx                    context.Context
		poolMap                      map[string]*poolEntry
		strategy                     Strategy
		cleanupTimer                 *time.Ticker
		runnerName                   string
		liteEnginePath               string
		instanceStore                store.InstanceStore
		stageOwnerStore              store.StageOwnerStore
		capacityReservationStore     store.CapacityReservationStore
		harnessTestBinaryURI         string
		pluginBinaryURI              string
		tmate                        types.Tmate
		autoInjectionBinaryURI       string
		liteEngineFallbackPath       string
		pluginBinaryFallbackURI      string
		runnerConfig                 types.RunnerConfig
		annotationsBinaryURI         string
		annotationsBinaryFallbackURI string
		envmanBinaryURI              string
		envmanBinaryFallbackURI      string
		tmateBinaryURI               string
		tmateBinaryFallbackURI       string
		env                          string
	}

	poolEntry struct {
		sync.Mutex
		Pool
	}
)

// New creates a new Manager from an EnvConfig.
// This is a convenience constructor that uses NewManagerFromConfig internally.
func New(
	globalContext context.Context,
	instanceStore store.InstanceStore,
	envConfig *config.EnvConfig,
) *Manager {
	cfg := NewManagerConfigFromEnv(globalContext, instanceStore, envConfig)
	return NewManagerFromConfig(cfg)
}

// NewManager creates a new Manager with all configuration parameters.
// Deprecated: Use NewManagerFromConfig or NewManagerWithOptions instead.
//
//nolint:gocritic
func NewManager(
	globalContext context.Context,
	instanceStore store.InstanceStore,
	stageOwnerStore store.StageOwnerStore,
	capacityReservationStore store.CapacityReservationStore,
	tmate types.Tmate,
	runnerName,
	liteEnginePath,
	harnessTestBinaryURI,
	pluginBinaryURI,
	autoInjectionBinaryURI,
	liteEngineFallbackPath,
	pluginBinaryFallbackURI string, runnerConfig types.RunnerConfig,
	annotationsBinaryURI string, annotationsBinaryFallbackURI string,
	envmanBinaryURI string, envmanBinaryFallbackURI string,
	tmateBinaryURI string, tmateBinaryFallbackURI string,
	env string,
) *Manager {
	cfg := ManagerConfig{
		GlobalCtx:                    globalContext,
		InstanceStore:                instanceStore,
		StageOwnerStore:              stageOwnerStore,
		CapacityReservationStore:     capacityReservationStore,
		Tmate:                        tmate,
		RunnerName:                   runnerName,
		RunnerConfig:                 runnerConfig,
		Env:                          env,
		LiteEnginePath:               liteEnginePath,
		LiteEngineFallbackPath:       liteEngineFallbackPath,
		HarnessTestBinaryURI:         harnessTestBinaryURI,
		PluginBinaryURI:              pluginBinaryURI,
		PluginBinaryFallbackURI:      pluginBinaryFallbackURI,
		AutoInjectionBinaryURI:       autoInjectionBinaryURI,
		AnnotationsBinaryURI:         annotationsBinaryURI,
		AnnotationsBinaryFallbackURI: annotationsBinaryFallbackURI,
		EnvmanBinaryURI:              envmanBinaryURI,
		EnvmanBinaryFallbackURI:      envmanBinaryFallbackURI,
		TmateBinaryURI:               tmateBinaryURI,
		TmateBinaryFallbackURI:       tmateBinaryFallbackURI,
	}
	return NewManagerFromConfig(cfg)
}

// GetPoolSpec returns the pool specification for a given pool name.
func (m *Manager) GetPoolSpec(poolName string) (interface{}, error) {
	entry := m.poolMap[poolName]
	if entry == nil {
		return nil, fmt.Errorf("manager: pool %s not found", poolName)
	}
	if entry.Spec == nil {
		return nil, fmt.Errorf("manager: pool %s does not have a stored spec", poolName)
	}
	return entry.Spec, nil
}

// Inspect returns platform, root directory, and driver name for a pool.
func (m *Manager) Inspect(name string) (platform types.Platform, rootDir, driver string) {
	entry := m.poolMap[name]
	if entry == nil {
		return
	}

	platform = entry.Platform
	rootDir = entry.Driver.RootDir()
	driver = entry.Driver.DriverName()

	return
}

// Exists returns true if a pool with given name exists.
func (m *Manager) Exists(name string) bool {
	return m.poolMap[name] != nil
}

// Count returns the number of pools.
func (m *Manager) Count() int {
	return len(m.poolMap)
}

// MatchPoolNameFromPlatform returns the pool name that matches the requested platform.
func (m *Manager) MatchPoolNameFromPlatform(requested *types.Platform) string {
	for _, pool := range m.poolMap {
		if pool.Platform.OS == requested.OS && pool.Platform.Arch == requested.Arch {
			return pool.Name
		}
	}
	return ""
}

// Add adds pools to the manager.
func (m *Manager) Add(pools ...Pool) error {
	if len(pools) == 0 {
		return nil
	}

	if m.poolMap == nil {
		m.poolMap = map[string]*poolEntry{}
	}

	for i := range pools {
		name := pools[i].Name
		if name == "" {
			return errors.New("pool must have a name")
		}

		if _, alreadyExists := m.poolMap[name]; alreadyExists {
			return fmt.Errorf("pool %q already defined", name)
		}

		m.poolMap[name] = &poolEntry{
			Mutex: sync.Mutex{},
			Pool:  pools[i],
		}
	}

	return nil
}

// forEach iterates over all pools and executes the provided function.
func (m *Manager) forEach(ctx context.Context,
	serverName string,
	query *types.QueryParams,
	f func(ctx context.Context, pool *poolEntry, serverName string, query *types.QueryParams) error) error {
	for _, pool := range m.poolMap {
		err := f(ctx, pool, serverName, query)
		if err != nil {
			return err
		}
	}

	return nil
}
