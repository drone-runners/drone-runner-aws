package harness

import (
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/sirupsen/logrus"
)

// if pool mapping is defined in env config, it figures out the mapped pool name & returns it
// else returns the input pool
func fetchPool(accountID, inputPool string, p config.PoolMapperByAccount) string {
	if accountID == "" {
		return inputPool
	}

	poolMap, ok := p[accountID]
	if !ok {
		return inputPool
	}

	if v, ok := poolMap[inputPool]; !ok {
		return inputPool
	} else {
		logrus.WithField("old_pool", inputPool).
			WithField("updated_pool", v).
			Info("Updated the pool")
		return v
	}
}
