package harness

import "github.com/drone-runners/drone-runner-aws/command/config"

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
		return v
	}
}
