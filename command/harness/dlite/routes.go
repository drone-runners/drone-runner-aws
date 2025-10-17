package dlite

import (
	"github.com/wings-software/dlite/task"
)

const (
	initTask      = "DLITE_CI_VM_INITIALIZE_TASK"
	executeTask   = "DLITE_CI_VM_EXECUTE_TASK"
	executeTaskV2 = "DLITE_CI_VM_EXECUTE_TASK_V2"
	cleanupTask   = "DLITE_CI_VM_CLEANUP_TASK"
	cleanupTaskV2 = "DLITE_CI_VM_CLEANUP_TASK_V2"
	capacityTask  = "DLITE_CI_VM_CAPACITY_TASK"
)

func routeMap(c *dliteCommand) map[string]task.Handler {
	return map[string]task.Handler{
		initTask:      pollerMiddleware(&VMInitTask{c}),
		executeTask:   pollerMiddleware(&VMExecuteTask{c}),
		executeTaskV2: pollerMiddleware(&VMExecuteTask{c}),
		cleanupTask:   pollerMiddleware(&VMCleanupTask{c}),
		cleanupTaskV2: pollerMiddleware(&VMCleanupTask{c}),
		capacityTask:  pollerMiddleware(&VMCapacityTask{c}),
	}
}
