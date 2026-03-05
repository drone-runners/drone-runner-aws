package drivers

import (
	"context"
	"sync"

	"github.com/drone/runner-go/logger"

	"github.com/drone-runners/drone-runner-aws/types"
)

// BuildPools populates all pools with instances.
func (m *Manager) BuildPools(ctx context.Context) error {
	query := types.QueryParams{RunnerName: m.runnerName}
	return m.forEach(ctx, m.GetTLSServerName(), &query, m.buildPoolWithMutex)
}

// buildPoolWithMutex wraps buildPool with mutex locking.
func (m *Manager) buildPoolWithMutex(ctx context.Context, pool *poolEntry, tlsServerName string, query *types.QueryParams) error {
	pool.Lock()
	defer pool.Unlock()

	return m.buildPool(ctx, pool, tlsServerName, query, m.setupInstanceWithHibernate, nil)
}

// buildPool populates a pool with as many instances as it's needed for the pool.
func (m *Manager) buildPool(
	ctx context.Context,
	pool *poolEntry,
	tlsServerName string,
	query *types.QueryParams,
	setupInstanceWithHibernate func(
		context.Context,
		*poolEntry,
		string,
		string,
		*types.MachineConfig,
		*types.GitspaceAgentConfig,
		*types.StorageConfig,
		int64,
		*types.Platform,
	) (*types.Instance, error),
	setupInstanceAsync func(context.Context, string, string, *types.SetupInstanceParams),
) error {
	instBusy, instFree, instHibernating, _, err := m.list(ctx, pool, query)
	if err != nil {
		return err
	}
	instFree = append(instFree, instHibernating...)

	strategy := m.strategy
	if strategy == nil {
		strategy = Greedy{}
	}

	logr := logger.FromContext(ctx).
		WithField("driver", pool.Driver.DriverName()).
		WithField("pool", pool.Name)

	shouldCreate, shouldRemove := strategy.CountCreateRemove(
		pool.MinSize, pool.MaxSize,
		len(instBusy), len(instFree))

	if shouldRemove > 0 {
		instances := make([]*types.Instance, shouldRemove)
		for i := 0; i < shouldRemove; i++ {
			instances[i] = instFree[i]
		}

		err := pool.Driver.Destroy(ctx, instances)
		if err != nil {
			logr.WithError(err).Errorln("build pool: failed to destroy excess instances")
		}
	}

	if shouldCreate < 0 {
		return nil
	}

	wg := &sync.WaitGroup{}
	wg.Add(shouldCreate)

	for shouldCreate > 0 {
		go func(ctx context.Context, logr logger.Logger) {
			defer wg.Done()

			// generate certs cert
			inst, err := setupInstanceWithHibernate(ctx, pool, tlsServerName, "", nil, nil, nil, 0, nil)
			if err != nil {
				logr.WithError(err).Errorln("build pool: failed to create instance")
				if setupInstanceAsync != nil {
					logr.WithField("runner_name", m.runnerName).Infoln("build pool: creating instance asynchronously")
					setupInstanceAsync(ctx, pool.Name, m.runnerName, nil)
				}
				return
			}
			logr.
				WithField("pool", pool.Name).
				WithField("id", inst.ID).
				WithField("name", inst.Name).
				Infoln("build pool: created new instance")
		}(ctx, logr)
		shouldCreate--
	}

	wg.Wait()

	// Building pool variants if present
	if len(pool.PoolVariants) > 0 {
		logr.Infoln("build pool: building variant pools")
		m.buildPoolWithVariants(ctx, pool, tlsServerName, setupInstanceWithHibernate, setupInstanceAsync, logr)
	}

	return nil
}

// buildPoolWithVariants builds pool instances for each variant configuration.
// Simply creates the number of instances specified in variant.Pool without checking DB.
func (m *Manager) buildPoolWithVariants(
	ctx context.Context,
	pool *poolEntry,
	tlsServerName string,
	setupInstanceWithHibernate func(
		context.Context,
		*poolEntry,
		string,
		string,
		*types.MachineConfig,
		*types.GitspaceAgentConfig,
		*types.StorageConfig,
		int64,
		*types.Platform,
	) (*types.Instance, error),
	setupInstanceAsync func(context.Context, string, string, *types.SetupInstanceParams),
	logr logger.Logger,
) {
	// Process each variant and create instances
	for idx := range pool.PoolVariants {
		variant := &pool.PoolVariants[idx]
		// Get variant params (VariantID should already be set from YAML through embedding)
		variantParams := variant.SetupInstanceParams

		// Convert SetupInstanceParams to MachineConfig
		variantConfig := m.setupInstanceParamsToMachineConfig(&variantParams)

		logr = logr.
			WithField("variant_id", variantParams.VariantID).
			WithField("variant_index", idx)

		// Use variant's pool size (number of instances to create)
		instanceCount := variant.Pool
		if instanceCount <= 0 {
			logr.Infoln("build pool with variants: skipping variant with pool size 0")
			continue
		}

		logr.
			WithField("instance_count", instanceCount).
			Infoln("build pool with variants: creating instances for variant")

		// Create instances for this variant
		wg := &sync.WaitGroup{}
		wg.Add(instanceCount)

		for i := 0; i < instanceCount; i++ {
			go func(ctx context.Context, logr logger.Logger, variantParams *types.SetupInstanceParams, machineConfig *types.MachineConfig) {
				defer wg.Done()

				inst, err := setupInstanceWithHibernate(ctx, pool, tlsServerName, "", machineConfig, nil, nil, 0, nil)
				if err != nil {
					logr.WithError(err).Errorln("build pool with variants: failed to create instance")
					if setupInstanceAsync != nil {
						logr.WithField("runner_name", m.runnerName).Infoln("build pool with variants: creating instance asynchronously")
						setupInstanceAsync(ctx, pool.Name, m.runnerName, variantParams)
					}
					return
				}
				logr.
					WithField("pool", pool.Name).
					WithField("id", inst.ID).
					WithField("name", inst.Name).
					WithField("variant_id", machineConfig.VariantID).
					Infoln("build pool with variants: created new instance")
			}(ctx, logr, &variantParams, variantConfig)
		}

		wg.Wait()
	}
}
