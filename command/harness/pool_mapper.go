package harness

import (
	"github.com/sirupsen/logrus"
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
