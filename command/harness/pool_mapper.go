package harness

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/types"
)

// if pool mapping is defined in env config, it figures out the mapped pool name & returns it
// else returns the input pool
func fetchPool(accountID, inputPool string, p map[string]map[string]string) string {
	if accountID == "" {
		return inputPool
	}

	poolMap, ok := p[accountID]
	if !ok {
		return inputPool
	}

	v, ok := poolMap[inputPool]
	if !ok {
		return inputPool
	}

	logrus.WithField("old_pool", inputPool).
		WithField("updated_pool", v).
		Info("Updated the pool")
	return v
}

// requestPools returns the pools to try for a request in order and, with capability scheduling,
// the claim scope. The scope's pools replace the fallback pools; envFallbacks apply only when
// the request has none. params.AccountID is the owner ("free" for free accounts).
func requestPools(
	ctx context.Context,
	poolManager drivers.IManager,
	pool string,
	fallbacks, envFallbacks []string,
	params *types.ProvisionParams,
	mapAccountID string,
	poolMapByAccount map[string]map[string]string,
) ([]string, *types.ClaimScope) {
	if scope := claimScope(ctx, poolManager, pool, params, mapAccountID, poolMapByAccount); scope != nil {
		return scope.Pools, scope
	}
	if len(fallbacks) == 0 {
		fallbacks = envFallbacks
	}
	return append([]string{pool}, fallbacks...), nil
}

// targetPool returns the pool to provision in for a pool from requestPools. Scope pools are
// already mapped.
func targetPool(pool string, scope *types.ClaimScope, accountID string, poolMapByAccount map[string]map[string]string) string {
	if scope != nil {
		return pool
	}
	return fetchPool(accountID, pool, poolMapByAccount)
}

// claimScope returns where a request to pool may claim and create an instance, or nil when the
// pool manager serves it from the requested pool and its fallbacks.
func claimScope(
	ctx context.Context,
	poolManager drivers.IManager,
	pool string,
	params *types.ProvisionParams,
	accountID string,
	poolMapByAccount map[string]map[string]string,
) *types.ClaimScope {
	cp, ok := poolManager.(drivers.CapabilityProvisioner)
	if !ok {
		return nil
	}
	return cp.ClaimScope(ctx, pool, params, func(p string) string {
		return fetchPool(accountID, p, poolMapByAccount)
	})
}
