package dlite

import (
	"github.com/wings-software/dlite/task"
)

// Task type constants for routing.
const (
	initTask      = "DLITE_CI_VM_INITIALIZE_TASK"
	executeTask   = "DLITE_CI_VM_EXECUTE_TASK"
	executeTaskV2 = "DLITE_CI_VM_EXECUTE_TASK_V2"
	cleanupTask   = "DLITE_CI_VM_CLEANUP_TASK"
	cleanupTaskV2 = "DLITE_CI_VM_CLEANUP_TASK_V2"
	capacityTask  = "DLITE_CI_VM_CAPACITY_TASK"
)

// routeMap returns the task handler routing map for the dlite poller.
func routeMap(c *dliteCommand) map[string]task.Handler {
	return map[string]task.Handler{
		initTask:      pollerMiddleware(&VMInitTask{c: c}),
		executeTask:   pollerMiddleware(&VMExecuteTask{c: c}),
		executeTaskV2: pollerMiddleware(&VMExecuteTask{c: c}),
		cleanupTask:   pollerMiddleware(&VMCleanupTask{c: c}),
		cleanupTaskV2: pollerMiddleware(&VMCleanupTask{c: c}),
		capacityTask:  pollerMiddleware(&VMCapacityTask{c: c}),
	}
}
